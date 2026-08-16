package content

// Level is the typed surface one level's YAML file decodes into. Every field
// docs/LEVEL-FORMAT.md section 2 describes appears here, and nothing else: a
// field that is not in that document does not go in this struct, because the
// loader decodes in strict mode and an unrecognized key is a rejection rather
// than a silent default.
//
// This type began life in setupspec.go declaring only the four fields issue
// #50's setup and teardown runner reads. Issue #53 extended it in place and
// moved it here, which is exactly what that declaration's own contract
// comment called for.
type Level struct {
	// ID is the level's unique identifier, matching [a-z0-9][a-z0-9-]* per
	// the authoring invariants in docs/LEVEL-FORMAT.md section 2. It is
	// validated with platform.ValidIdentifier before it becomes a path
	// element or an argv element, because it reaches both: the sentinel
	// path and the sandbox user allowlist.
	ID string `yaml:"id"`

	// Version is bumped by the author when a level's checks change, which
	// invalidates previously recorded best scores.
	Version int `yaml:"version"`

	// Title is the level's display name.
	Title string `yaml:"title"`

	// Act is the id of the act in pack.yaml this level belongs to.
	Act string `yaml:"act"`

	// Difficulty is 1 to 5.
	Difficulty int `yaml:"difficulty"`

	// XP is the base experience award for completing the level.
	XP int `yaml:"xp"`

	// ParCommands is the command count a competent user needs, which drives
	// the efficiency bonus. Zero disables the bonus rather than demanding
	// perfection, so omitting the field is a real choice and not an
	// unwinnable default.
	ParCommands int `yaml:"par_commands"`

	// EstimatedMinutes is how long a beginner should expect to take.
	EstimatedMinutes int `yaml:"estimated_minutes"`

	// Concepts names what the level teaches and drives the concept coverage
	// report. At least one is required.
	Concepts []string `yaml:"concepts"`

	// Prerequisites are level ids that must be complete first. They form the
	// unlock DAG, which the validator proves acyclic.
	Prerequisites []string `yaml:"prerequisites"`

	// Requires names runtime capabilities the level needs, such as
	// networking, systemd, or multiuser.
	Requires []string `yaml:"requires"`

	// Boss changes presentation: a boss level shows no numbered steps.
	Boss bool `yaml:"boss"`

	// Briefing is the markdown shown before the shell attaches.
	Briefing string `yaml:"briefing"`

	// Objectives are the checklist items shown to the learner. Their ids
	// correspond one to one with check ids, in both directions.
	Objectives []Objective `yaml:"objectives"`

	// Setup describes how the level's world is materialized into the
	// sandbox before the learner plays.
	Setup Setup `yaml:"setup"`

	// Checks are the assertions about sandbox state that decide whether the
	// level is solved. The top level is an implicit all_of over the
	// non-optional entries.
	Checks []CheckSpec `yaml:"checks"`

	// Hints escalate from a nudge to a reveal. At least two are required,
	// because one hint is a cliff.
	Hints []Hint `yaml:"hints"`

	// Solution is the commands that solve the level. Golden tests run it,
	// and `hint --reveal` prints it.
	Solution string `yaml:"solution"`

	// Teardown describes how the level's world is removed afterward.
	Teardown Teardown `yaml:"teardown"`

	// Tags are free-form labels used for filtering and reporting.
	Tags []string `yaml:"tags"`

	// SourceFile is the pack-relative path the level was loaded from, for
	// use in every validation message. It carries yaml:"-" because it is set
	// by the loader, never by the pack's own YAML.
	SourceFile string `yaml:"-"`
}

// Objective is one checklist item shown to the learner. Its ID must match the
// id of a check, which is what keeps the checklist honest: an objective with
// no check is a promise nothing verifies, and a check with no objective is
// work the learner was never told to do.
type Objective struct {
	// ID matches a CheckSpec.ID in the same level.
	ID string `yaml:"id"`

	// Text is the checklist line the learner reads.
	Text string `yaml:"text"`

	// Optional marks a bonus objective. A bonus objective awards XP and
	// never blocks passing.
	Optional bool `yaml:"optional"`

	// Preserves marks an objective the learner satisfies by NOT breaking
	// something, rather than by doing something.
	//
	// Almost every objective describes work: it fails on a fresh setup and
	// passes once the learner has acted, and the golden contract in
	// docs/LEVEL-FORMAT.md section 7 requires exactly that, because a
	// required check passing before the learner touches anything is usually
	// a check that tests nothing. files-04 is the exception that shows the
	// rule. It is the level that teaches deletion, and "important/ is
	// untouched, byte for byte" is the whole point of it: the objective is
	// already true at setup, and the learner's job is to keep it true while
	// deleting everything around it.
	//
	// Setting this exempts the objective from the golden contract's
	// pre-solution assertion. It does NOT make the objective optional: a
	// learner who deletes important/ still fails the level, which is the
	// entire lesson.
	//
	// It is deliberately narrow. A level cannot mark every objective this
	// way to quiet the gate, because the harness still requires at least one
	// required objective to fail before the solution runs.
	Preserves bool `yaml:"preserves"`
}

// Hint is one tier of help, with the cost shown before the learner commits to
// spending it.
type Hint struct {
	// Cost is the XP the learner pays to read this hint.
	Cost int `yaml:"cost"`

	// Text is the hint itself. It is empty when RevealSolution is set, since
	// the level's solution is the text in that case.
	Text string `yaml:"text"`

	// RevealSolution makes this hint print the level's solution block.
	RevealSolution bool `yaml:"reveal_solution"`
}
