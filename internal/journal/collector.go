package journal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sync"

	"github.com/JoottunAtish/ShellForge/internal/platform"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

// journalFileName is the file images/rc/instrument.bash appends to on every
// PROMPT_COMMAND, inside the per-level state directory it calls SF_STATE.
const journalFileName = "journal.tsv"

// maxRecordsPerSince bounds how many records one Since call parses and
// returns. It bounds the parse dimension and nothing else: how many bytes one
// call reads out of the sandbox is not bounded here, or anywhere.
//
// journal.tsv is learner-controlled: `yes >> journal.tsv` inside the sandbox
// appends without limit, and a learner playing an Act VI level may well try
// exactly that. maxTSVLine already bounds one line to 64 KiB; this bounds how
// many of those lines one call is willing to parse, so the parsing work one
// call does, and the slice it returns, stay fixed no matter how large the
// file grows.
//
// The bytes that call reads do not stay fixed. runtime.Session.PullFile is a
// whole-file read with no offset and no length, so one Since call over a
// journal.tsv the learner has grown to 500 MiB still pulls all 500 MiB into
// memory and then parses the first 1000 records of it. Nothing in this
// package can narrow that: a ranged read is a runtime.Session interface
// change. TODO(v0.2): give PullFile a ranged form so a huge journal.tsv costs
// a bounded read as well as a bounded parse.
//
// 1000 is three orders of magnitude above a level's par, and a caller that
// drains before every check and again at teardown catches up across calls
// rather than losing anything: a call that hits the bound leaves the rest for
// the next one, in order, with no gap.
const maxRecordsPerSince = 1000

// Collector reads the in-sandbox journal.tsv and turns it into Entries.
//
// It takes a runtime.Session because reading a file out of the sandbox is a
// session operation, which is exactly the dependency the PTY multiplexer does
// not have and deliberately is not given: internal/pty stays a byte pump, so
// the command text arrives through this file rather than through the marker
// stream. The TSV is the contract between the sandbox and the host; see
// ReadTSV for the format and its three forgiveness rules.
//
// A Collector is safe for concurrent use. It is stateful by design: it
// remembers how far through the file it has read, so successive Since calls
// are incremental.
//
// # Lifetime: one Collector per attempt
//
// That remembered position is a byte offset into one particular journal.tsv,
// so a Collector belongs to one attempt at one level and must not outlive
// it. Construct a new one whenever the file it reads starts again from
// empty, which is every level reset and every fresh attempt: a level reset
// recreates journal.tsv, and a Collector carried across that boundary starts
// the new file at the old file's offset. Since detects the shrink and
// re-syncs from byte 0 (see its doc comment), so the failure is a batch of
// commands re-emitted or skipped rather than corrupted, but it is still a
// wrong answer that a fresh Collector does not have. Seq is another reason:
// it is monotonic for the life of the Collector, so reusing one across
// attempts continues the previous attempt's numbering into the new one.
type Collector struct {
	s    runtime.Session
	path string

	mu sync.Mutex
	// offset is how many bytes of journal.tsv have already been consumed.
	// It always sits immediately after a '\n' of the content it was measured
	// against, so a record split across two reads is never consumed half
	// way. It is meaningful only for as long as the file keeps growing from
	// the same content: a file that shrinks has invalidated it, which is why
	// Since resets it to 0 the moment it sees len(b) fall below it.
	offset int
	// seq counts the records returned so far, and stamps Entry.Seq. It is
	// monotonic for the life of the Collector rather than restarting per
	// call, because Journal.Level orders by seq: a per-call sequence would
	// make three drains of one level read back interleaved.
	seq int64
}

// NewCollector returns a Collector that reads journal.tsv from stateDir
// inside the sandbox s. See Collector for the lifetime rule: one per attempt,
// never carried across a level reset.
//
// stateDir is a sandbox path, so the file path is joined with path.Join and
// never filepath.Join: on a Windows host filepath.Join would produce
// backslashes for a Linux path that has to be read inside the sandbox.
//
// stateDir is refused, never adjusted, when it is not somewhere the sandbox
// state directory may live: empty, relative, holding a .. segment, or outside
// platform.LearnerHomePrefix. It is caller-supplied configuration today
// rather than pack-supplied data, so this is defence in depth rather than a
// gate on untrusted input, but it is the same lexical allowlist
// internal/content/setup puts the same value through before joining a level
// id onto it, and the joined path here does end up as an argv element once a
// runtime backend builds a `docker cp` or a `wsl.exe cat` out of it. Refusing
// at construction turns a misconfigured state directory into one error a
// caller can report, rather than a read that fails once per Since for the
// rest of the session with nobody looking at the result.
//
// platform.UnsafeLevelRoot is layer 0, so this import points downward and
// needs no archtest table edit.
func NewCollector(s runtime.Session, stateDir string) (*Collector, error) {
	if reason := platform.UnsafeLevelRoot(stateDir); reason != nil {
		return nil, fmt.Errorf("refusing %q as the sandbox state directory: %w", stateDir, reason)
	}
	return &Collector{s: s, path: path.Join(stateDir, journalFileName)}, nil
}

// Since returns every record in journal.tsv after the last one Since already
// returned, oldest first, stamped with levelID and with a monotonic Seq.
//
// It is safe to call while the learner is mid-command. A final line with no
// terminator is an incomplete record, so it is left where it is and returned
// by a later call once the shell has finished writing it; a line that is
// malformed or over-long is skipped for good, and the records around it still
// arrive, exactly as ReadTSV describes.
//
// At most maxRecordsPerSince records are returned per call. The rest wait for
// the next call, in order.
//
// Nothing here can fail a level or a check. A journal.tsv that does not exist
// yet, which is every level until the learner's first command, reports no
// records and no error. Any other read failure reports no records and an
// error naming the operation and nothing else: no command text, no Entry.
// Which of the two a backend produces for a missing file is not pinned by the
// runtime contract (the Docker backend reports a non-zero `docker cp` exit
// rather than anything wrapping fs.ErrNotExist), so a caller must degrade to
// zero commands on both. See (*game.JournalSink).Drain, which does.
//
// A journal.tsv that shrinks, which a learner can arrange by truncating or
// recreating their own file, returns nothing for the call that notices, and
// re-syncs to byte 0 of the new content for the call after it. Both halves
// matter. Returning nothing is what stops a truncation from re-emitting the
// tail of the old file as if it were new. Re-syncing is what stops the
// offset from freezing at a byte position with no relationship to a line
// boundary in the file that is there now: once the shrunken file grows back
// past that stale offset, reading from it would land mid-record and hand
// ReadTSV whatever happened to be four tab separated fields from there,
// silently dropping every command written since the shrink or admitting a
// garbled one. The cost of re-syncing is that a record the truncation left in
// place can be collected twice, which is the better of the two: a duplicate
// row is a wrong count in a bonus that is already knowingly gameable, and a
// garbled row is a wrong command in the learner's own history.
//
// Detection is only as good as a length comparison, and a length comparison
// cannot see a file that shrank and regrew past its old length between two
// calls. That is not fixable from a byte offset alone, and it is the second
// reason a Collector belongs to one attempt: see Collector's lifetime rule.
func (c *Collector) Since(ctx context.Context, levelID string) ([]Entry, error) {
	b, err := c.s.PullFile(ctx, c.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read the command journal from the sandbox: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.offset > len(b) {
		// The file shrank under us. Drop the offset rather than keep it: it
		// counts bytes of content that no longer exists, so leaving it in
		// place would make the next read that finds the file grown past it
		// start mid-record. Returning nothing for this call is what keeps
		// the truncated remainder from being re-emitted immediately.
		c.offset = 0
		return nil, nil
	}
	window := completeRecords(b[c.offset:], maxRecordsPerSince)
	if len(window) == 0 {
		return nil, nil
	}

	entries, err := ReadTSV(bytes.NewReader(window))
	if err != nil {
		// bytes.Reader cannot fail, so this is unreachable today. It is
		// handled rather than ignored because ReadTSV's signature, not this
		// call site, is what guarantees that.
		return nil, fmt.Errorf("parse the command journal: %w", err)
	}
	c.offset += len(window)

	for i := range entries {
		c.seq++
		entries[i].Seq = c.seq
		entries[i].LevelID = levelID
		// UsedTab and UsedHistory stay false. The TSV has no column for
		// either: instrument.bash writes four fields, and a tab tap is a
		// host-side observation of the learner's keystrokes, not something
		// the shell records. internal/pty carries it on CommandEvent.UsedTab
		// instead. Joining the two streams is deliberately not done here:
		// correlating an OSC event with a TSV record by index drifts the
		// moment either side drops one.
	}
	return entries, nil
}

// completeRecords returns the prefix of w holding whole, terminated lines: at
// most maxRecords of them, and never a trailing fragment with no '\n'.
//
// It scans for at most maxRecords newlines, so the work it does is bounded by
// the records it is willing to return rather than by the size of w. The one
// exception is a w with fewer newlines than that, where the final search has
// to look at the rest of w to establish there is no further terminator; that
// search allocates nothing and copies nothing.
func completeRecords(w []byte, maxRecords int) []byte {
	end := 0
	for n := 0; n < maxRecords; n++ {
		i := bytes.IndexByte(w[end:], '\n')
		if i < 0 {
			break
		}
		end += i + 1
	}
	return w[:end]
}
