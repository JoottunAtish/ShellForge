package verify

// The shape the engine hands the UI, and the rules that decide each field.
//
// docs/LEVEL-FORMAT.md section 5 is the contract this file implements, field
// for field. TestLevelResultMarshalsToLevelFormatSection5 pins it against the
// document rather than against this declaration, so a convenience field added
// here later fails a test instead of quietly changing a published shape.

// LevelResult is the outcome of running every check in one level.
//
// It carries no score. Scoring, XP, the hint penalty and the efficiency bonus
// are internal/game's, on Day 4 of the build plan, and they need the learner's
// history and the progress database, neither of which this layer can see.
// docs/LEVEL-FORMAT.md section 5 does show a `score` object, and the game layer
// produces it by embedding this type in its own envelope and adding the field:
// Go inlines an embedded struct's fields when marshalling, so the published
// document is section 5 exactly, with no tag in this file changing. The
// omission here is a layer boundary, not an oversight.
type LevelResult struct {
	// LevelID is the id of the level these results are for, copied from
	// Env.LevelID.
	LevelID string `json:"level_id"`

	// Passed is true only when every blocking objective passed. Blocking
	// means neither optional nor severity: warn, so a bonus objective the
	// learner missed and an anti-pattern warning both leave this true.
	Passed bool `json:"passed"`

	// Objectives is one entry per top-level check that corresponds to a
	// checklist line, in declaration order. A severity: warn check has no
	// checklist line and produces a note instead, so it does not appear here.
	//
	// Always non-nil, so the JSON is [] rather than null. A UI iterating a
	// null gets zero objectives either way, but a shell script reading the
	// document with `jq '.objectives | length'` gets an error on null and 0 on
	// [], and the second is the honest answer.
	Objectives []ObjectiveResult `json:"objectives"`

	// PrimaryFailure is the first failing blocking objective, or nil when
	// none failed. It is the one on_fail the learner is shown: telling
	// somebody five things are wrong when the second follows from the first
	// is how a beginner decides the game is broken rather than their answer.
	//
	// It holds a copy rather than a pointer into Objectives, so a caller that
	// sorts or filters the slice cannot make this field point at the wrong
	// entry.
	PrimaryFailure *ObjectiveResult `json:"primary_failure,omitempty"`

	// Notes are the messages of results that did not pass and do not block:
	// optional bonus objectives, and severity: warn anti-pattern checks.
	// Authored notes come first, in declaration order, then any note the
	// engine itself added about a budget or a cancellation.
	//
	// Always non-nil, for the reason given on Objectives.
	Notes []string `json:"notes"`

	// DurationMS is how long the whole run took, in milliseconds.
	DurationMS int64 `json:"duration_ms"`
}

// ObjectiveResult is one checklist line, joined from a check's Result and the
// objective text the level author wrote.
type ObjectiveResult struct {
	// ID is the objective's id, which is also the check's id: the pack
	// validator proves the two correspond one to one in both directions.
	ID string `json:"id"`

	// Text is the checklist line the learner reads.
	Text string `json:"text"`

	// Status is the outcome: pass, fail, warn, timeout or error. Section 5's
	// example shows only pass and fail because that level produced only
	// those two; all five are legal values here.
	Status Status `json:"status"`

	// Optional marks a bonus objective, which never blocks passing. Omitted
	// when false, matching section 5, where the field appears only on the
	// bonus objective.
	Optional bool `json:"optional,omitempty"`

	// Message is what the learner reads about this objective. Empty on a
	// pass. On a fail or a warn it is the authored on_fail, plus any
	// diagnostic the check produced. On a timeout or an error it is the
	// engine's own sentence and never the authored on_fail: we did not find
	// out whether the learner was right, so telling them what they got wrong
	// would be a guess.
	Message string `json:"message,omitempty"`
}

// blocking reports whether this objective decides Passed.
//
// An optional objective is a bonus and a warn objective is advice; neither can
// fail a level. The two are separate fields rather than one because they mean
// different things to a learner: a bonus is something they could still go and
// earn, a warning is a comment on how they solved it.
func (o ObjectiveResult) blocking() bool {
	return !o.Optional && o.Status != StatusWarn
}
