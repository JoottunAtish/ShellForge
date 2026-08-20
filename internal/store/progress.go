package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrInvalidStatus is returned when a caller passes a LevelStatus that is
// not one of the five values validStatus accepts. It refuses the write
// before any SQL runs.
var ErrInvalidStatus = errors.New("store: invalid level status")

// ErrInvalidOutcome is returned when a caller passes an Outcome that is not
// one of the three values validOutcome accepts. It refuses the write before
// any transaction opens.
var ErrInvalidOutcome = errors.New("store: invalid attempt outcome")

// ErrNoSuchAttempt is returned by FinishAttempt when no attempt row exists
// with the given id. Nothing is written.
var ErrNoSuchAttempt = errors.New("store: no such attempt")

// ErrAttemptClosed is returned by FinishAttempt when the attempt already
// has a non-null ended_at. An attempt can be finished exactly once; nothing
// is written on a second call.
var ErrAttemptClosed = errors.New("store: attempt already closed")

// Profile is one learner profile row.
type Profile struct {
	ID        int64
	Name      string
	CreatedAt time.Time
}

// LevelStatus is the state of one level for one profile.
type LevelStatus string

// The five level statuses level_state.status accepts. Any other value is
// refused by validStatus before it reaches SQL.
const (
	StatusLocked     LevelStatus = "locked"
	StatusAvailable  LevelStatus = "available"
	StatusInProgress LevelStatus = "in_progress"
	StatusPassed     LevelStatus = "passed"
	StatusSkipped    LevelStatus = "skipped"
)

// Outcome is how one attempt ended.
type Outcome string

// The three attempt outcomes FinishAttempt accepts. Any other value is
// refused by validOutcome before a transaction opens.
const (
	OutcomePassed    Outcome = "passed"
	OutcomeAbandoned Outcome = "abandoned"
	OutcomeSkipped   Outcome = "skipped"
)

// LevelState is one profile's recorded progress on one level. Stale is true
// when the state was read against a level_version different from the one
// stored, in which case BestScore is reported as zero rather than the
// stored value from a level definition that no longer matches.
type LevelState struct {
	ProfileID     int64
	PackID        string
	LevelID       string
	LevelVersion  int
	Status        LevelStatus
	BestScore     int
	Attempts      int
	HintsUsed     int
	CommandsUsed  int
	FirstPassedAt time.Time
	LastAttemptAt time.Time
	TotalSeconds  int
	Stale         bool
}

// Attempt is one recorded attempt at a level.
type Attempt struct {
	ID           int64
	ProfileID    int64
	LevelID      string
	StartedAt    time.Time
	EndedAt      time.Time
	Outcome      Outcome
	Score        int
	HintsUsed    int
	CommandsUsed int
}

// validStatus reports whether status is one of the five values level_state
// accepts.
func validStatus(status LevelStatus) bool {
	switch status {
	case StatusLocked, StatusAvailable, StatusInProgress, StatusPassed, StatusSkipped:
		return true
	default:
		return false
	}
}

// validOutcome reports whether outcome is one of the three values attempt
// accepts.
func validOutcome(outcome Outcome) bool {
	switch outcome {
	case OutcomePassed, OutcomeAbandoned, OutcomeSkipped:
		return true
	default:
		return false
	}
}

// nullableUnix converts t to a sql.NullInt64 holding unix seconds, or an
// invalid value when t is the zero time. A zero time.Time never means "unix
// epoch" in this package; it always means "not recorded".
func nullableUnix(t time.Time) sql.NullInt64 {
	if t.IsZero() {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: t.Unix(), Valid: true}
}

// timeFromNullable converts a sql.NullInt64 read back from a unix-seconds
// column into a time.Time, returning the zero time.Time when n is not
// valid. A valid value is always converted to UTC, so callers must compare
// with Equal or Unix, never ==.
func timeFromNullable(n sql.NullInt64) time.Time {
	if !n.Valid {
		return time.Time{}
	}
	return time.Unix(n.Int64, 0).UTC()
}

// EnsureProfile returns the single profile row this database holds,
// creating it with name if none exists yet. Once a profile exists, later
// calls return that same row and ignore name: this database holds exactly
// one profile, and its identity is fixed by whichever call created it.
func (s *Store) EnsureProfile(ctx context.Context, name string) (Profile, error) {
	read := func() (Profile, error) {
		var p Profile
		var createdAt sql.NullInt64
		err := s.db.QueryRowContext(ctx,
			"SELECT id, name, created_at FROM profile ORDER BY id ASC LIMIT 1",
		).Scan(&p.ID, &p.Name, &createdAt)
		if err != nil {
			return Profile{}, err
		}
		p.CreatedAt = timeFromNullable(createdAt)
		return p, nil
	}

	if p, err := read(); err == nil {
		return p, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Profile{}, fmt.Errorf("read profile: %w", err)
	}

	if _, err := s.db.ExecContext(ctx,
		"INSERT INTO profile (name, created_at) VALUES (?, ?) ON CONFLICT(name) DO NOTHING",
		name, time.Now().Unix(),
	); err != nil {
		return Profile{}, fmt.Errorf("insert profile %q: %w", name, err)
	}

	p, err := read()
	if err != nil {
		return Profile{}, fmt.Errorf("read profile after insert: %w", err)
	}
	return p, nil
}

// levelStateColumns is the column list every level_state read shares, kept
// in one place so LevelState and LevelStates cannot drift apart.
const levelStateColumns = `profile_id, level_id, pack_id, level_version, status, best_score,
	attempts, hints_used, commands_used, first_passed_at, last_attempt_at, total_seconds`

// scanLevelState scans one level_state row from a query built against
// levelStateColumns, in that order, into a LevelState. Stale is never set
// here: callers that know the level_version they expect apply that
// comparison themselves.
func scanLevelState(scan func(dest ...any) error) (LevelState, error) {
	var st LevelState
	var packID, status sql.NullString
	var firstPassedAt, lastAttemptAt sql.NullInt64
	if err := scan(&st.ProfileID, &st.LevelID, &packID, &st.LevelVersion, &status, &st.BestScore,
		&st.Attempts, &st.HintsUsed, &st.CommandsUsed, &firstPassedAt, &lastAttemptAt, &st.TotalSeconds); err != nil {
		return LevelState{}, err
	}
	st.PackID = packID.String
	st.Status = LevelStatus(status.String)
	st.FirstPassedAt = timeFromNullable(firstPassedAt)
	st.LastAttemptAt = timeFromNullable(lastAttemptAt)
	return st, nil
}

// LevelState returns profileID's recorded progress on levelID, comparing
// the stored level_version against levelVersion. If no row exists, it
// returns a zero LevelState and found is false. If the stored version
// differs from levelVersion, the returned state has Stale set and
// BestScore zeroed, since a best score computed against a different level
// definition is not comparable to one computed against this one; every
// other field, including Attempts and HintsUsed, is reported as stored.
func (s *Store) LevelState(ctx context.Context, profileID int64, levelID string, levelVersion int) (LevelState, bool, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+levelStateColumns+" FROM level_state WHERE profile_id = ? AND level_id = ?",
		profileID, levelID)
	st, err := scanLevelState(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return LevelState{}, false, nil
	}
	if err != nil {
		return LevelState{}, false, fmt.Errorf("read level_state for %q: %w", levelID, err)
	}
	if st.LevelVersion != levelVersion {
		st.Stale = true
		st.BestScore = 0
	}
	return st, true, nil
}

// LevelStates returns every level_state row for profileID within packID,
// keyed by level_id. Stale is always false in this view: staleness is only
// meaningful when a caller supplies the level_version it expects, which
// LevelStates does not take.
func (s *Store) LevelStates(ctx context.Context, profileID int64, packID string) (map[string]LevelState, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+levelStateColumns+" FROM level_state WHERE profile_id = ? AND pack_id = ?",
		profileID, packID)
	if err != nil {
		return nil, fmt.Errorf("query level_state for pack %q: %w", packID, err)
	}
	defer rows.Close()

	out := make(map[string]LevelState)
	for rows.Next() {
		st, err := scanLevelState(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan level_state row for pack %q: %w", packID, err)
		}
		out[st.LevelID] = st
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate level_state rows for pack %q: %w", packID, err)
	}
	return out, nil
}

// SetLevelStatus upserts the status, pack_id, and level_version for
// profileID's row on levelID. It validates status before touching SQL and
// returns an error wrapping ErrInvalidStatus, naming the rejected value, on
// an unrecognized one.
func (s *Store) SetLevelStatus(ctx context.Context, profileID int64, packID, levelID string, levelVersion int, status LevelStatus) error {
	if !validStatus(status) {
		return fmt.Errorf("set level status %q: %w", string(status), ErrInvalidStatus)
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO level_state (profile_id, level_id, pack_id, level_version, status)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(profile_id, level_id) DO UPDATE SET
			status = excluded.status,
			pack_id = excluded.pack_id,
			level_version = excluded.level_version`,
		profileID, levelID, packID, levelVersion, string(status),
	); err != nil {
		return fmt.Errorf("set level status for %q: %w", levelID, err)
	}
	return nil
}

// StartAttempt records a new attempt row for profileID on levelID and
// advances level_state to in_progress, incrementing attempts by one. It
// returns the new attempt's id. attempts is incremented only here, never
// anywhere else in this package.
func (s *Store) StartAttempt(ctx context.Context, profileID int64, packID, levelID string, levelVersion int, startedAt time.Time) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin start attempt transaction for %q: %w", levelID, err)
	}
	defer func() {
		_ = tx.Rollback() // no-op once Commit has succeeded; matters only on an early return
	}()

	startedUnix := nullableUnix(startedAt)

	res, err := tx.ExecContext(ctx,
		"INSERT INTO attempt (profile_id, level_id, started_at) VALUES (?, ?, ?)",
		profileID, levelID, startedUnix)
	if err != nil {
		return 0, fmt.Errorf("insert attempt for %q: %w", levelID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read new attempt id for %q: %w", levelID, err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO level_state (profile_id, level_id, pack_id, level_version, status, attempts, last_attempt_at)
		VALUES (?, ?, ?, ?, 'in_progress', 1, ?)
		ON CONFLICT(profile_id, level_id) DO UPDATE SET
			attempts = attempts + 1,
			status = 'in_progress',
			last_attempt_at = excluded.last_attempt_at,
			level_version = excluded.level_version,
			pack_id = excluded.pack_id`,
		profileID, levelID, packID, levelVersion, startedUnix,
	); err != nil {
		return 0, fmt.Errorf("upsert level_state for %q: %w", levelID, err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit start attempt for %q: %w", levelID, err)
	}
	return id, nil
}

// FinishAttempt closes the attempt row identified by id with the outcome
// and counters carried in a, and folds those counters into the level_state
// row for that attempt's profile and level. It validates a.Outcome before
// opening any transaction, returning an error wrapping ErrInvalidOutcome
// naming the rejected value. It returns an error wrapping ErrNoSuchAttempt
// if id names no attempt, and one wrapping ErrAttemptClosed if that attempt
// was already finished; neither writes anything, because both are detected
// before the UPDATE runs and the whole call is one transaction that only
// ever commits once.
func (s *Store) FinishAttempt(ctx context.Context, id int64, a Attempt) error {
	if !validOutcome(a.Outcome) {
		return fmt.Errorf("finish attempt %d: outcome %q: %w", id, string(a.Outcome), ErrInvalidOutcome)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin finish attempt %d transaction: %w", id, err)
	}
	defer func() {
		_ = tx.Rollback() // no-op once Commit has succeeded; matters only on an early return
	}()

	var storedEndedAt, storedStartedAt sql.NullInt64
	var profileID int64
	var levelID string
	row := tx.QueryRowContext(ctx,
		"SELECT ended_at, started_at, profile_id, level_id FROM attempt WHERE id = ?", id)
	if err := row.Scan(&storedEndedAt, &storedStartedAt, &profileID, &levelID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("finish attempt %d: %w", id, ErrNoSuchAttempt)
		}
		return fmt.Errorf("read attempt %d: %w", id, err)
	}
	if storedEndedAt.Valid {
		return fmt.Errorf("finish attempt %d: %w", id, ErrAttemptClosed)
	}

	endedUnix := nullableUnix(a.EndedAt)

	if _, err := tx.ExecContext(ctx,
		"UPDATE attempt SET ended_at = ?, outcome = ?, score = ?, hints_used = ?, commands_used = ? WHERE id = ?",
		endedUnix, string(a.Outcome), a.Score, a.HintsUsed, a.CommandsUsed, id,
	); err != nil {
		return fmt.Errorf("update attempt %d: %w", id, err)
	}

	// elapsedSeconds and firstPassedCandidate are computed in Go, from the
	// started_at this SELECT just read and the outcome the caller passed,
	// rather than reasoned about inside the UPDATE below: level_state has
	// no started_at column of its own to compare against, and computing
	// the CASE in SQL would need the same "is this a pass" test twice.
	var elapsedSeconds int64
	if endedUnix.Valid && storedStartedAt.Valid {
		elapsedSeconds = endedUnix.Int64 - storedStartedAt.Int64
	}

	var firstPassedCandidate sql.NullInt64
	var newStatus LevelStatus
	switch a.Outcome {
	case OutcomePassed:
		firstPassedCandidate = endedUnix
		newStatus = StatusPassed
	case OutcomeSkipped:
		newStatus = StatusSkipped
	default:
		newStatus = StatusInProgress
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE level_state SET
			best_score = MAX(best_score, ?),
			first_passed_at = COALESCE(first_passed_at, ?),
			hints_used = hints_used + ?,
			commands_used = commands_used + ?,
			total_seconds = total_seconds + ?,
			last_attempt_at = ?,
			status = ?
		WHERE profile_id = ? AND level_id = ?`,
		a.Score,
		firstPassedCandidate,
		a.HintsUsed,
		a.CommandsUsed,
		elapsedSeconds,
		endedUnix,
		string(newStatus),
		profileID, levelID,
	); err != nil {
		return fmt.Errorf("update level_state for attempt %d: %w", id, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit finish attempt %d: %w", id, err)
	}
	return nil
}

// TotalXP returns the sum of best_score across every level_state row for
// profileID within packID, or zero if there are none.
func (s *Store) TotalXP(ctx context.Context, profileID int64, packID string) (int, error) {
	var xp int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COALESCE(SUM(best_score), 0) FROM level_state WHERE profile_id = ? AND pack_id = ?",
		profileID, packID,
	).Scan(&xp); err != nil {
		return 0, fmt.Errorf("sum best_score for pack %q: %w", packID, err)
	}
	return xp, nil
}
