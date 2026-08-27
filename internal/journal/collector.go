package journal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sync"

	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

// journalFileName is the file images/rc/instrument.bash appends to on every
// PROMPT_COMMAND, inside the per-level state directory it calls SF_STATE.
const journalFileName = "journal.tsv"

// maxRecordsPerSince bounds how many records one Since call parses and
// returns. journal.tsv is learner-controlled: `yes >> journal.tsv` inside the
// sandbox appends without limit, and a learner playing an Act VI level may
// well try exactly that. maxTSVLine already bounds one line to 64 KiB; this
// bounds the other dimension, so one call's parsing work and the slice it
// returns stay fixed no matter how large the file grows.
//
// 1000 is three orders of magnitude above a level's par, and a caller that
// drains before every check and again at teardown catches up across calls
// rather than losing anything: a call that hits the bound leaves the rest for
// the next one, in order, with no gap.
//
// The bound is on records parsed, not on bytes read. One Since call still
// pulls the whole file into memory, because runtime.Session.PullFile is a
// whole-file read with no offset or length. TODO(v0.2): give PullFile a
// ranged form so a huge journal.tsv costs a bounded read as well as a bounded
// parse.
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
type Collector struct {
	s    runtime.Session
	path string

	mu sync.Mutex
	// offset is how many bytes of journal.tsv have already been consumed.
	// It always sits immediately after a '\n', so a record split across two
	// reads is never consumed half way.
	offset int
	// seq counts the records returned so far, and stamps Entry.Seq. It is
	// monotonic for the life of the Collector rather than restarting per
	// call, because Journal.Level orders by seq: a per-call sequence would
	// make three drains of one level read back interleaved.
	seq int64
}

// NewCollector returns a Collector that reads journal.tsv from stateDir
// inside the sandbox s.
//
// stateDir is a sandbox path, so the file path is joined with path.Join and
// never filepath.Join: on a Windows host filepath.Join would produce
// backslashes for a Linux path that has to be read inside the sandbox.
func NewCollector(s runtime.Session, stateDir string) *Collector {
	return &Collector{s: s, path: path.Join(stateDir, journalFileName)}
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
// A journal.tsv that shrinks, which a learner can arrange by truncating their
// own file, returns nothing further rather than re-emitting commands already
// recorded.
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
