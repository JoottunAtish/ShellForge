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

// EnsureProfile returns the single profile row this database holds. The
// row is created, with name, only when the profile table is empty: the
// insert and the emptiness check are one atomic statement, so this
// database always holds exactly one profile even when two callers make
// their first call concurrently with different names. Later calls return
// that same row and ignore name.
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
		"INSERT INTO profile (name, created_at) SELECT ?, ? WHERE NOT EXISTS (SELECT 1 FROM profile)",
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
// anywhere else in this package. Marking the level in_progress here,
// unconditionally, is the ticket-specified behavior: a fresh attempt
// always supersedes whatever status came before it, passed included.
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
		if elapsedSeconds < 0 {
			// A caller clock adjustment could place ended before started;
			// never let that drive total_seconds negative.
			elapsedSeconds = 0
		}
	}

	// Outcome maps to level status. Abandoned leaves the level in_progress:
	// this API records what happened and does not keep a previously passed
	// level marked passed across a later attempt. Preserving passed across
	// re-attempts is the orchestrator's policy, out of scope for this
	// ticket; best_score and first_passed_at are preserved regardless.
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

// AddHintsUsed adds n to level_state.hints_used for profileID's record of
// levelID, creating the row if there is not one yet. An n at or below zero
// writes nothing.
//
// It takes a count rather than incrementing by one because a single act can
// spend more than one tier: revealing a level's solution buys every tier
// below it in one go, and recording that as one hint would let the learner
// re-buy the tiers they had already paid for on their next attempt.
//
// It is called the moment the learner confirms a hint, not when the level
// ends. Charging on take is what makes the ladder mean something: a learner
// who takes three hints and abandons the level has still spent them when
// they come back, and the tier they are offered on re-entry is the one
// after the last they paid for.
//
// It deliberately does NOT touch the attempt row. FinishAttempt folds its
// own Attempt.HintsUsed into this same column, so a caller that uses this
// method must pass zero there or the same hint is counted twice. The
// orchestrator does exactly that, and says so at the call site.
//
// TODO(v0.2): the consequence is that attempt.hints_used stays zero for
// every attempt. Nothing reads that column today. Recording it needs either
// a second counter that FinishAttempt writes without folding, or a
// FinishAttempt that takes "already recorded" as a flag; both are a change
// to a tested contract that this is not the ticket for.
func (s *Store) AddHintsUsed(ctx context.Context, profileID int64, packID, levelID string, levelVersion, n int) error {
	if n <= 0 {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO level_state (profile_id, level_id, pack_id, level_version, status, hints_used)
		VALUES (?, ?, ?, ?, 'in_progress', ?)
		ON CONFLICT(profile_id, level_id) DO UPDATE SET
			hints_used = hints_used + excluded.hints_used`,
		profileID, levelID, packID, levelVersion, n,
	); err != nil {
		return fmt.Errorf("record %d hints taken on level %q: %w", n, levelID, err)
	}
	return nil
}

// Achievement is one profile's record of one achievement.
//
// UnlockedAt is the zero time.Time when the achievement is not yet earned,
// never the unix epoch: this package's own rule, shared with LevelState's
// FirstPassedAt. Progress is whatever the rule that owns this key last
// stored, which is a rule-private counter and means nothing outside it.
type Achievement struct {
	Key        string
	UnlockedAt time.Time
	Progress   float64
}

// Unlocked reports whether this achievement has been earned.
func (a Achievement) Unlocked() bool { return !a.UnlockedAt.IsZero() }

// Achievements returns every achievement row recorded for profileID, keyed
// by achievement key. A profile with no rows returns an empty map and no
// error, which is what a fresh install looks like.
func (s *Store) Achievements(ctx context.Context, profileID int64) (map[string]Achievement, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT key, unlocked_at, progress FROM achievement WHERE profile_id = ?", profileID)
	if err != nil {
		return nil, fmt.Errorf("read achievements for profile %d: %w", profileID, err)
	}
	defer rows.Close()

	out := make(map[string]Achievement)
	for rows.Next() {
		var (
			key        string
			unlockedAt sql.NullInt64
			progress   sql.NullFloat64
		)
		if err := rows.Scan(&key, &unlockedAt, &progress); err != nil {
			return nil, fmt.Errorf("scan an achievement row for profile %d: %w", profileID, err)
		}
		out[key] = Achievement{
			Key:        key,
			UnlockedAt: timeFromNullable(unlockedAt),
			Progress:   progress.Float64,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read achievements for profile %d: %w", profileID, err)
	}
	return out, nil
}

// SaveProgress records an achievement rule's private counter, creating the
// row if there is not one yet. It never clears unlocked_at: a counter that
// keeps rising after the achievement was earned must not un-earn it.
func (s *Store) SaveProgress(ctx context.Context, profileID int64, key string, progress float64) error {
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO achievement (profile_id, key, progress) VALUES (?, ?, ?)
		ON CONFLICT(profile_id, key) DO UPDATE SET progress = excluded.progress`,
		profileID, key, progress,
	); err != nil {
		return fmt.Errorf("save progress for achievement %q: %w", key, err)
	}
	return nil
}

// Unlock marks an achievement earned at time at, and reports whether this
// call is the one that earned it.
//
// The bool is what makes an unlock happen exactly once. A second call, in
// this session or after a restart, writes nothing and returns false, so a
// caller can publish its "you earned this" event on the true and never
// announce the same badge twice. The single UPDATE's WHERE clause is what
// decides it, so two callers racing cannot both see true.
func (s *Store) Unlock(ctx context.Context, profileID int64, key string, at time.Time) (bool, error) {
	if _, err := s.db.ExecContext(ctx,
		"INSERT OR IGNORE INTO achievement (profile_id, key) VALUES (?, ?)",
		profileID, key,
	); err != nil {
		return false, fmt.Errorf("create the row for achievement %q: %w", key, err)
	}

	res, err := s.db.ExecContext(ctx,
		"UPDATE achievement SET unlocked_at = ? WHERE profile_id = ? AND key = ? AND unlocked_at IS NULL",
		nullableUnix(at), profileID, key)
	if err != nil {
		return false, fmt.Errorf("unlock achievement %q: %w", key, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read whether achievement %q was already unlocked: %w", key, err)
	}
	return n == 1, nil
}
