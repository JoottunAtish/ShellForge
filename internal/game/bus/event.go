package bus

import "time"

// Kind identifies the concrete type of an Event without a type assertion, so
// a subscriber can switch on it before deciding whether to inspect the event
// further.
type Kind string

// The seven domain events the game layer publishes. LevelReset is a seventh
// event, diverging from ARCHITECTURE 4.5, which lists six; SESSION-PROMPTS
// Day 4 Session G lists seven and is correct. See the commit message for the
// documentation-ratification note.
const (
	KindLevelStarted        Kind = "level_started"
	KindCommandExecuted     Kind = "command_executed"
	KindCheckRun            Kind = "check_run"
	KindHintTaken           Kind = "hint_taken"
	KindLevelPassed         Kind = "level_passed"
	KindLevelReset          Kind = "level_reset"
	KindAchievementUnlocked Kind = "achievement_unlocked"
)

// Event is anything the bus can Publish and deliver. Kind reports which of
// the seven concrete event types a value is, and When reports the time the
// event occurred. The unexported kind method closes the interface: only the
// seven types declared in this file can satisfy Event, so a Publish call
// outside this package can never introduce an eighth kind the rest of the
// game layer does not know how to handle.
type Event interface {
	Kind() Kind
	When() time.Time
	kind()
}

// LevelStarted is published when a learner begins an attempt at a level.
type LevelStarted struct {
	LevelID   string
	AttemptID int64
	Attempt   int // how many times this level has been attempted, including this one
	At        time.Time
}

func (e LevelStarted) Kind() Kind      { return KindLevelStarted }
func (e LevelStarted) When() time.Time { return e.At }
func (LevelStarted) kind()             {}

// CommandExecuted is published for every command the learner runs inside the
// sandbox. Raw is the literal command text and is secret material: it may
// contain a password, a token, or other input the learner typed, so nothing
// that reports on a subscriber failure (see WithErrorHandler) may include it.
type CommandExecuted struct {
	LevelID     string
	AttemptID   int64
	Seq         int64
	Raw         string // may be empty: see the journal ticket
	ExitCode    int
	Cwd         string
	Duration    time.Duration
	UsedTab     bool
	UsedHistory bool
	At          time.Time
}

func (e CommandExecuted) Kind() Kind      { return KindCommandExecuted }
func (e CommandExecuted) When() time.Time { return e.At }
func (CommandExecuted) kind()             {}

// CheckRun is published each time the learner's progress is verified,
// whether through the explicit check command or an automatic recheck.
type CheckRun struct {
	LevelID          string
	AttemptID        int64
	Passed           bool
	ObjectivesPassed int
	ObjectivesTotal  int
	At               time.Time
}

func (e CheckRun) Kind() Kind      { return KindCheckRun }
func (e CheckRun) When() time.Time { return e.At }
func (CheckRun) kind()             {}

// HintTaken is published when a learner spends a hint on a level.
type HintTaken struct {
	LevelID   string
	AttemptID int64
	Tier      int  // 1-based index into the level's hints
	Cost      int  // XP, as authored
	Revealed  bool // true when this tier revealed the solution
	At        time.Time
}

func (e HintTaken) Kind() Kind      { return KindHintTaken }
func (e HintTaken) When() time.Time { return e.At }
func (HintTaken) kind()             {}

// LevelPassed is published the moment every required objective of a level is
// satisfied.
type LevelPassed struct {
	LevelID      string
	AttemptID    int64
	Score        int
	HintsUsed    int
	CommandsUsed int
	FirstTry     bool
	At           time.Time
}

func (e LevelPassed) Kind() Kind      { return KindLevelPassed }
func (e LevelPassed) When() time.Time { return e.At }
func (LevelPassed) kind()             {}

// LevelReset is published when a learner discards their progress on a level
// and starts it over.
type LevelReset struct {
	LevelID   string
	AttemptID int64
	At        time.Time
}

func (e LevelReset) Kind() Kind      { return KindLevelReset }
func (e LevelReset) When() time.Time { return e.At }
func (LevelReset) kind()             {}

// AchievementUnlocked is published when the learner earns an achievement.
// Key identifies which one; the achievement's own text lives with the
// achievement definition, not on this event.
type AchievementUnlocked struct {
	Key string
	At  time.Time
}

func (e AchievementUnlocked) Kind() Kind      { return KindAchievementUnlocked }
func (e AchievementUnlocked) When() time.Time { return e.At }
func (AchievementUnlocked) kind()             {}
