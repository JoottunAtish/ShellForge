package journal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/scope"
	"github.com/JoottunAtish/ShellForge/internal/store"
)

// Entry is one recorded command.
//
// Raw is redacted by String and GoString wherever fmt can reach either
// method: on an Entry value, a *Entry, a slice or map holding either, a
// struct with an Entry in an EXPORTED field, and inside fmt.Errorf. There
// is exactly one hole no method on Entry can close: an Entry (or *Entry)
// reached only through an UNEXPORTED field of some other type. Go's
// reflect package marks a value read through an unexported field as
// read-only, so fmt's CanInterface check fails for it and fmt falls back to
// printing the struct field by field instead of calling String, which
// prints Raw in full under %v, %+v, and %#v. No change to Entry, including
// giving Raw its own named string type with its own String method, closes
// this: the read-only flag propagates to every subfield reflect walks into.
// The rule this leaves for every caller: never embed an Entry or *Entry as
// an unexported field of a type you might format with fmt. Give it an
// exported field instead, or do not format the containing type at all.
type Entry struct {
	Seq         int64
	TS          time.Time
	LevelID     string
	Cwd         string
	Raw         string // secret material: never logged, never in a bug report
	Exit        int
	DurationMS  int64
	UsedTab     bool
	UsedHistory bool
}

// String implements fmt.Stringer. Every field is printed except Raw, whose
// presence is reported only as a byte count: see this package's doc.go for
// why Raw is treated as secret material. It is built with strings.Builder
// and strconv rather than fmt.Sprintf so that this package's own privacy
// guard, which forbids calling any fmt function but fmt.Errorf, holds even
// here.
//
// This redaction only ever runs when fmt actually calls String, which it
// cannot do for an Entry sitting behind an unexported field of some other
// type: see Entry's own doc comment for that one gap and the rule callers
// must follow instead.
func (e Entry) String() string {
	var b strings.Builder
	b.WriteString("Entry{Seq: ")
	b.WriteString(strconv.FormatInt(e.Seq, 10))
	b.WriteString(", TS: ")
	b.WriteString(e.TS.Format(time.RFC3339Nano))
	b.WriteString(", LevelID: ")
	b.WriteString(strconv.Quote(e.LevelID))
	b.WriteString(", Cwd: ")
	b.WriteString(strconv.Quote(e.Cwd))
	b.WriteString(", Raw: [redacted ")
	b.WriteString(strconv.Itoa(len(e.Raw)))
	b.WriteString(" bytes], Exit: ")
	b.WriteString(strconv.Itoa(e.Exit))
	b.WriteString(", DurationMS: ")
	b.WriteString(strconv.FormatInt(e.DurationMS, 10))
	b.WriteString(", UsedTab: ")
	b.WriteString(strconv.FormatBool(e.UsedTab))
	b.WriteString(", UsedHistory: ")
	b.WriteString(strconv.FormatBool(e.UsedHistory))
	b.WriteString("}")
	return b.String()
}

// GoString implements fmt.GoStringer, so that %#v also redacts Raw. Without
// this, %#v would use the default struct formatting and print Raw in full,
// bypassing String entirely.
func (e Entry) GoString() string {
	return e.String()
}

// commandsTimeout bounds the query Commands issues internally. Commands has
// no ctx parameter because verify.JournalReader fixes that signature, so
// Commands creates its own bounded context instead, keeping a wedged read
// from stalling verification indefinitely.
const commandsTimeout = 2 * time.Second

// errNoLevelSet is what scope.Level answers through Err when Commands is
// asked for scope.Level before SetLevel has ever been called. It is
// deliberately not a guess: see SetLevel and the Commands doc comment.
var errNoLevelSet = errors.New("journal: no level set; call SetLevel before verifying")

// Journal records and replays the learner's commands, backed by a
// store.Store.
type Journal struct {
	s *store.Store

	mu       sync.Mutex
	err      error  // last error swallowed by Commands
	levelID  string // set by SetLevel; meaningless unless levelSet
	sinceID  int64  // set by SetLevel; the events.id boundary for scope.Level
	levelSet bool   // whether SetLevel has ever been called
}

// New returns a Journal backed by s. s must already be open and migrated.
func New(s *store.Store) *Journal {
	return &Journal{s: s}
}

// SetLevel tells the Journal which level is under verification and where
// its attempt begins. Layer 4 calls this when a level starts or resets,
// before any check runs: levelID is the level whose commands scope.Level
// should answer with, and sinceID is the events.id boundary, exclusive, so
// a command recorded before this attempt started (an earlier attempt at
// the same level, or a different level entirely) never appears in
// scope.Level's answer. Passing the highest events.id recorded at the
// moment the level starts is what gives a fresh attempt a clean boundary;
// passing 0 includes every row ever recorded for levelID.
//
// SetLevel is the only way scope.Level learns which level it is answering
// for: Journal never infers it from the newest row in the table, because
// the newest row can belong to a different level than the one currently
// being verified.
func (j *Journal) SetLevel(levelID string, sinceID int64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.levelID = levelID
	j.sinceID = sinceID
	j.levelSet = true
}

// currentLevel returns the level and boundary SetLevel most recently
// recorded, and whether SetLevel has ever been called at all.
func (j *Journal) currentLevel() (levelID string, sinceID int64, ok bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.levelID, j.sinceID, j.levelSet
}

// Append records one entry with a single parameterized INSERT.
//
// TS is stored as REAL unix seconds with microsecond precision:
// float64(e.TS.UnixMicro())/1e6. A nanosecond component finer than a
// microsecond does not survive the round trip; that is a deliberate
// contract, not an accident, because time.Time nanoseconds do not survive
// float64 exactly and the TSV producer (see tsv.go) is microsecond grained
// anyway. Level reads it back with
// time.UnixMicro(int64(math.Round(v*1e6))).UTC(), and the rounding step is
// what makes a microsecond-precision value round trip exactly despite the
// float64 conversion.
func (j *Journal) Append(ctx context.Context, e Entry) error {
	ts := float64(e.TS.UnixMicro()) / 1e6
	_, err := j.s.DB().ExecContext(ctx,
		`INSERT INTO events (seq, ts, level_id, cwd, raw, exit_code, duration_ms, used_tab, used_history)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.Seq, ts, e.LevelID, e.Cwd, e.Raw, e.Exit, e.DurationMS, e.UsedTab, e.UsedHistory,
	)
	if err != nil {
		return fmt.Errorf("append journal entry: %w", err)
	}
	return nil
}

// Level returns every entry recorded for levelID, ordered by seq then by
// the row's own id, which breaks a tie between two entries recorded with
// the same seq.
func (j *Journal) Level(ctx context.Context, levelID string) ([]Entry, error) {
	rows, err := j.s.DB().QueryContext(ctx,
		`SELECT seq, ts, level_id, cwd, raw, exit_code, duration_ms, used_tab, used_history
		 FROM events WHERE level_id = ? ORDER BY seq ASC, id ASC`,
		levelID,
	)
	if err != nil {
		return nil, fmt.Errorf("query level %q: %w", levelID, err)
	}
	defer rows.Close()

	var entries []Entry
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan level %q: %w", levelID, err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read level %q: %w", levelID, err)
	}
	return entries, nil
}

// scanEvent reads one events row (seq, ts, level_id, cwd, raw, exit_code,
// duration_ms, used_tab, used_history, in that order) into an Entry,
// converting the REAL ts column back into a time.Time per the precision
// contract documented on Append.
func scanEvent(rows *sql.Rows) (Entry, error) {
	var (
		e  Entry
		ts float64
	)
	if err := rows.Scan(&e.Seq, &ts, &e.LevelID, &e.Cwd, &e.Raw, &e.Exit, &e.DurationMS, &e.UsedTab, &e.UsedHistory); err != nil {
		return Entry{}, err
	}
	e.TS = time.UnixMicro(int64(math.Round(ts * 1e6))).UTC()
	return e, nil
}

// Commands returns the commands sc selects, oldest first. The parameter type
// comes from internal/scope, layer 0, which internal/verify aliases as
// verify.Scope, so this method is exactly the method set
// verify.JournalReader requires without this package naming internal/verify
// at all: see doc.go, and verifycontract_test.go for the compile-time proof.
//
// The three kinds answer as follows.
//
// scope.Level is every command recorded for the level and attempt SetLevel
// most recently named: every events row whose level_id equals the stored
// level and whose id is strictly greater than the stored boundary, oldest
// first. Before SetLevel has ever been called it answers with no commands
// rather than guessing a level from the newest row, and Err says why.
//
// scope.LastN is the most recent N commands, globally, regardless of which
// level SetLevel names. It does not stop at a level boundary: doc.go
// explains why that is a decision rather than an oversight. A non-positive
// N selects nothing.
//
// scope.Last is only the most recent command, globally, with the same
// deliberate lack of a level boundary as scope.LastN.
//
// A query failure never panics and is never printed. It is recorded and
// retrievable through Err, and Commands returns an empty slice.
// Verification never uses the journal to decide pass or fail (see doc.go),
// so a degraded command log costs only the efficiency bonus, never
// correctness.
func (j *Journal) Commands(sc scope.Scope) []string {
	ctx, cancel := context.WithTimeout(context.Background(), commandsTimeout)
	defer cancel()

	cmds, err := j.commands(ctx, sc)

	j.mu.Lock()
	j.err = err
	j.mu.Unlock()

	if err != nil {
		return []string{}
	}
	return cmds
}

// Err returns the error, if any, from the most recent call to Commands.
func (j *Journal) Err() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.err
}

func (j *Journal) commands(ctx context.Context, sc scope.Scope) ([]string, error) {
	switch sc.Kind {
	case scope.Level:
		levelID, sinceID, ok := j.currentLevel()
		if !ok {
			return nil, errNoLevelSet
		}
		rows, err := j.s.DB().QueryContext(ctx,
			"SELECT raw FROM events WHERE level_id = ? AND id > ? ORDER BY seq ASC, id ASC",
			levelID, sinceID)
		if err != nil {
			return nil, fmt.Errorf("query scope level: %w", err)
		}
		return scanCommands(rows)

	case scope.LastN:
		if sc.N <= 0 {
			return []string{}, nil
		}
		// Ordered by id, the auto-increment row identity, not by seq: seq
		// resets per level, so ordering globally by seq interleaves two
		// levels' commands by their per-level position instead of by when
		// they actually ran. id is assigned in insertion order and is the
		// only column here with a global, monotonic meaning.
		rows, err := j.s.DB().QueryContext(ctx,
			`SELECT raw FROM (
			   SELECT raw, id FROM events ORDER BY id DESC LIMIT ?
			 ) ORDER BY id ASC`,
			sc.N,
		)
		if err != nil {
			return nil, fmt.Errorf("query scope last_n: %w", err)
		}
		return scanCommands(rows)

	case scope.Last:
		var raw string
		err := j.s.DB().QueryRowContext(ctx,
			"SELECT raw FROM events ORDER BY id DESC LIMIT 1").Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return []string{}, nil
		}
		if err != nil {
			return nil, fmt.Errorf("query scope last: %w", err)
		}
		return []string{raw}, nil

	default:
		return nil, fmt.Errorf("unknown scope kind %q", sc.Kind)
	}
}

func scanCommands(rows *sql.Rows) ([]string, error) {
	defer rows.Close()
	cmds := []string{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan command: %w", err)
		}
		cmds = append(cmds, raw)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read commands: %w", err)
	}
	return cmds, nil
}
