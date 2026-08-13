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

	"github.com/JoottunAtish/ShellForge/internal/store"
)

// Entry is one recorded command.
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

// ScopeKind selects which slice of journal history Commands reads.
//
// This mirrors verify.ScopeKind field for field and value for value; it is
// not that type. internal/journal is layer 2 and internal/verify is layer
// 3, so this package's production code cannot name a type from
// internal/verify. See verifycontract_test.go for the adapter that bridges
// the two, and for the drift test that keeps this mirror honest.
type ScopeKind string

const (
	// ScopeLevel is every command run since the level started.
	ScopeLevel ScopeKind = "level"

	// ScopeLastN is the most recent N commands.
	ScopeLastN ScopeKind = "last_n"

	// ScopeLast is only the most recent command.
	ScopeLast ScopeKind = "last"
)

// Scope selects which commands Commands reads. N is meaningful only when
// Kind is ScopeLastN.
type Scope struct {
	Kind ScopeKind
	N    int
}

// commandsTimeout bounds the query Commands issues internally. Commands has
// no ctx parameter because verify.JournalReader fixes that signature, so
// Commands creates its own bounded context instead, keeping a wedged read
// from stalling verification indefinitely.
const commandsTimeout = 2 * time.Second

// Journal records and replays the learner's commands, backed by a
// store.Store.
type Journal struct {
	s *store.Store

	mu  sync.Mutex
	err error // last error swallowed by Commands
}

// New returns a Journal backed by s. s must already be open and migrated.
func New(s *store.Store) *Journal {
	return &Journal{s: s}
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

// Commands returns the commands in scope, oldest first, satisfying the
// method set verify.JournalReader requires: see verifycontract_test.go.
//
// A query failure never panics and is never printed. It is recorded and
// retrievable through Err, and Commands returns an empty slice.
// Verification never uses the journal to decide pass or fail (see doc.go),
// so a degraded command log costs only the efficiency bonus, never
// correctness.
func (j *Journal) Commands(scope Scope) []string {
	ctx, cancel := context.WithTimeout(context.Background(), commandsTimeout)
	defer cancel()

	cmds, err := j.commands(ctx, scope)

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

func (j *Journal) commands(ctx context.Context, scope Scope) ([]string, error) {
	switch scope.Kind {
	case ScopeLevel:
		levelID, err := j.newestLevelID(ctx)
		if err != nil {
			return nil, err
		}
		if levelID == "" {
			return []string{}, nil
		}
		rows, err := j.s.DB().QueryContext(ctx,
			"SELECT raw FROM events WHERE level_id = ? ORDER BY seq ASC, id ASC", levelID)
		if err != nil {
			return nil, fmt.Errorf("query scope level: %w", err)
		}
		return scanCommands(rows)

	case ScopeLastN:
		if scope.N <= 0 {
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
			scope.N,
		)
		if err != nil {
			return nil, fmt.Errorf("query scope last_n: %w", err)
		}
		return scanCommands(rows)

	case ScopeLast:
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
		return nil, fmt.Errorf("unknown scope kind %q", scope.Kind)
	}
}

// newestLevelID returns the level_id of the most recently recorded event,
// or "" with no error when the events table is empty. Ordered by id, the
// row's global insertion order, for the same reason ScopeLastN and
// ScopeLast are: seq alone has no cross-level meaning.
func (j *Journal) newestLevelID(ctx context.Context) (string, error) {
	var levelID string
	err := j.s.DB().QueryRowContext(ctx,
		"SELECT level_id FROM events ORDER BY id DESC LIMIT 1").Scan(&levelID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("find newest level: %w", err)
	}
	return levelID, nil
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
