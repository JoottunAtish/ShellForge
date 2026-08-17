package content

import (
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ProblemLevel separates a problem that must be fixed from one an author
// should know about.
type ProblemLevel string

const (
	// ProblemError is a pack that will not work. It fails validation.
	ProblemError ProblemLevel = "error"

	// ProblemWarning is a pack that works but is incomplete. It does not
	// fail validation, because a half-written pack is the normal state of
	// one being filled in, and a validator that refuses to run until the
	// pack is finished is a validator nobody uses while writing.
	ProblemWarning ProblemLevel = "warning"
)

// Problem is one thing wrong with a pack, located precisely enough that an
// author can go straight to it.
type Problem struct {
	// Level separates an error from a warning.
	Level ProblemLevel `json:"level"`

	// File is the pack-relative path, always set.
	File string `json:"file"`

	// LevelID is the level the problem is in, or empty for a problem with
	// the pack itself.
	LevelID string `json:"level_id,omitempty"`

	// Field is the dotted path to the offending field, such as
	// "checks[2].on_fail".
	Field string `json:"field,omitempty"`

	// Message says what is wrong.
	Message string `json:"message"`

	// Line is the line in File, or zero when it is not known.
	Line int `json:"line,omitempty"`
}

// Error renders a problem as one line, in the shape an editor's quickfix list
// understands: file, then location, then message.
func (p Problem) Error() string {
	var b strings.Builder
	b.WriteString(p.File)
	if p.Line > 0 {
		b.WriteString(fmt.Sprintf(":%d", p.Line))
	}
	b.WriteString(": ")
	if p.LevelID != "" {
		b.WriteString(p.LevelID + ": ")
	}
	if p.Field != "" {
		b.WriteString(p.Field + ": ")
	}
	b.WriteString(p.Message)
	return b.String()
}

// Report is everything Validate found.
type Report struct {
	// Problems are in file order, then in the order they were found within
	// a file.
	Problems []Problem `json:"problems"`
}

// OK reports whether the pack passed validation. Warnings do not fail it.
func (r Report) OK() bool {
	for _, p := range r.Problems {
		if p.Level == ProblemError {
			return false
		}
	}
	return true
}

// Errors returns only the problems that fail validation.
func (r Report) Errors() []Problem {
	var out []Problem
	for _, p := range r.Problems {
		if p.Level == ProblemError {
			out = append(out, p)
		}
	}
	return out
}

// TypeChecker answers the two questions about a check that only the check
// registry can answer. Validate takes this interface rather than importing
// internal/verify's concrete registry, which keeps this package testable with
// a fake and keeps the dependency between two layer 3 packages one way.
type TypeChecker interface {
	// Known reports whether typeName is a registered check type.
	Known(typeName string) bool

	// ValidateParams reports whether params is well formed for typeName.
	ValidateParams(typeName string, params map[string]any) error
}

// levelIDPattern is the level id rule from docs/LEVEL-FORMAT.md section 2.
//
// It is deliberately narrower than platform.IdentifierPattern: lowercase
// only, and hyphens but no underscores. Every value it accepts,
// platform.IdentifierPattern also accepts, which is what makes a validated
// level id safe to use as an argv element and as a path segment under the
// state directory. TestLevelIDIsNarrowerThanIdentifier pins that relationship
// so neither pattern can drift into accepting something the other refuses.
var levelIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// journalCheckTypes are the check types that read the command journal.
//
// They may never gate passing. The journal records what the learner typed,
// and instrument.bash writes it from inside the sandbox, where the learner
// can forge an entry with a printf of the right escape sequence. Both
// internal/journal/doc.go and internal/verify/doc.go already state that the
// journal is learner-influenced and is never evidence for scoring; before
// this rule, nothing enforced it, so a level could quietly stake passing on a
// signal a learner can fake.
var journalCheckTypes = map[string]bool{
	"command_matched":     true,
	"command_not_matched": true,
}

// ValidateOption configures Validate.
type ValidateOption func(*validator)

// WithPackFS gives Validate the pack's own filesystem, which enables the
// rules that need to know whether a file exists: a source: naming an asset
// that is not in the pack, most of all. Without it those rules are skipped
// rather than guessed at.
func WithPackFS(fsys fs.FS, root string) ValidateOption {
	return func(v *validator) {
		v.fsys = fsys
		v.fsRoot = root
	}
}

// WithHostRoot gives Validate the pack's directory on the host, which enables
// the symlink containment rule.
//
// Lexical containment is checked with or without this: fs.ValidPath already
// refuses a source: with a .. segment or a leading slash. What a host root
// adds is the case that a lexical test cannot see, an asset directory or file
// that is a symlink pointing outside the pack. Resolution happens before the
// prefix comparison, never after, because comparing the unresolved string is
// exactly how a symlinked asset gets through.
func WithHostRoot(dir string) ValidateOption {
	return func(v *validator) { v.hostRoot = dir }
}

// Validate checks a loaded pack against every authoring invariant in
// docs/LEVEL-FORMAT.md and returns everything it found.
//
// It reports every problem, not the first. An author who can fix only one
// error per run is an author who stops running the validator, and the output
// is one problem per line precisely so it can be pasted into an editor's
// quickfix list.
func Validate(p *Pack, tc TypeChecker, opts ...ValidateOption) Report {
	v := &validator{pack: p, tc: tc}
	for _, opt := range opts {
		opt(v)
	}

	v.validatePack()
	for i := range p.Levels {
		v.validateLevel(&p.Levels[i])
	}
	v.validatePrerequisites()
	v.validateActCoverage()

	v.sort()
	return Report{Problems: v.problems}
}

// validator accumulates problems while walking a pack.
type validator struct {
	pack     *Pack
	tc       TypeChecker
	fsys     fs.FS
	fsRoot   string
	hostRoot string
	problems []Problem
}

// errorf records an error-level problem.
func (v *validator) errorf(file, levelID, field string, line int, format string, args ...any) {
	v.problems = append(v.problems, Problem{
		Level:   ProblemError,
		File:    file,
		LevelID: levelID,
		Field:   field,
		Line:    line,
		Message: fmt.Sprintf(format, args...),
	})
}

// warnf records a warning-level problem.
func (v *validator) warnf(file, levelID, field string, format string, args ...any) {
	v.problems = append(v.problems, Problem{
		Level:   ProblemWarning,
		File:    file,
		LevelID: levelID,
		Field:   field,
		Message: fmt.Sprintf(format, args...),
	})
}

// sort orders problems by file, then line, then field, so that output is
// stable across runs and across machines. Two runs of the validator over an
// unchanged pack must produce byte-identical output, or the output cannot be
// diffed.
func (v *validator) sort() {
	sort.SliceStable(v.problems, func(i, j int) bool {
		a, b := v.problems[i], v.problems[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Field < b.Field
	})
}

const packFile = "pack.yaml"

// validatePack checks pack.yaml's own fields and the identity of every level.
func (v *validator) validatePack() {
	p := v.pack

	if p.ID == "" {
		v.errorf(packFile, "", "id", 0, "must not be empty")
	}
	if p.Name == "" {
		v.errorf(packFile, "", "name", 0, "must not be empty")
	}
	if p.ContentAPI != ContentAPIVersion {
		v.errorf(packFile, "", "content_api", 0, "must be %d, this build understands no other", ContentAPIVersion)
	}
	if len(p.Acts) == 0 {
		v.errorf(packFile, "", "acts", 0, "a pack must declare at least one act")
	}

	seenAct := map[string]bool{}
	for i, act := range p.Acts {
		field := fmt.Sprintf("acts[%d]", i)
		if act.ID == "" {
			v.errorf(packFile, "", field+".id", 0, "must not be empty")
			continue
		}
		if seenAct[act.ID] {
			v.errorf(packFile, "", field+".id", 0, "act id %q is declared twice", act.ID)
		}
		seenAct[act.ID] = true
		if act.Title == "" {
			v.errorf(packFile, "", field+".title", 0, "must not be empty")
		}
	}

	seenLevel := map[string]string{}
	for i := range p.Levels {
		lvl := &p.Levels[i]
		if lvl.ID == "" {
			v.errorf(lvl.SourceFile, "", "id", 0, "must not be empty")
			continue
		}
		if !levelIDPattern.MatchString(lvl.ID) {
			v.errorf(lvl.SourceFile, lvl.ID, "id", 0,
				"must match %s: lowercase letters, digits and hyphens, starting with a letter or a digit", levelIDPattern)
		}
		if first, dup := seenLevel[lvl.ID]; dup {
			v.errorf(lvl.SourceFile, lvl.ID, "id", 0, "level id %q is already declared in %s", lvl.ID, first)
		}
		seenLevel[lvl.ID] = lvl.SourceFile
	}
}

// validateLevel checks one level's own fields.
func (v *validator) validateLevel(lvl *Level) {
	file, id := lvl.SourceFile, lvl.ID

	if lvl.Title == "" {
		v.errorf(file, id, "title", 0, "must not be empty")
	}
	if lvl.Version < 1 {
		v.errorf(file, id, "version", 0, "must be 1 or greater, so that changing a level's checks can invalidate a recorded best score")
	}
	if lvl.Difficulty < 1 || lvl.Difficulty > 5 {
		v.errorf(file, id, "difficulty", 0, "must be between 1 and 5, got %d", lvl.Difficulty)
	}
	if lvl.XP <= 0 {
		v.errorf(file, id, "xp", 0, "must be greater than zero")
	}
	if lvl.ParCommands < 0 {
		v.errorf(file, id, "par_commands", 0, "must not be negative; omit it to disable the efficiency bonus")
	}
	if len(lvl.Concepts) == 0 {
		v.errorf(file, id, "concepts", 0, "a level must name at least one concept it teaches")
	}
	if strings.TrimSpace(lvl.Briefing) == "" {
		v.errorf(file, id, "briefing", 0, "must not be empty")
	}
	if strings.TrimSpace(lvl.Solution) == "" {
		v.errorf(file, id, "solution", 0, "must not be empty: the golden test runs it, and `hint --reveal` prints it")
	}

	v.validateAct(lvl)
	v.validateHints(lvl)
	v.validateSetup(lvl)
	v.validateChecks(lvl)
}

// validateAct checks that a level names an act pack.yaml declares.
func (v *validator) validateAct(lvl *Level) {
	if lvl.Act == "" {
		v.errorf(lvl.SourceFile, lvl.ID, "act", 0, "must not be empty")
		return
	}
	for _, act := range v.pack.Acts {
		if act.ID == lvl.Act {
			return
		}
	}
	v.errorf(lvl.SourceFile, lvl.ID, "act", 0, "act %q is not declared in pack.yaml", lvl.Act)
}

// validateHints enforces the two-hint minimum and that a hint says something.
func (v *validator) validateHints(lvl *Level) {
	if len(lvl.Hints) < 2 {
		v.errorf(lvl.SourceFile, lvl.ID, "hints", 0,
			"a level needs at least two hints, got %d: one hint is a cliff", len(lvl.Hints))
	}
	for i, h := range lvl.Hints {
		field := fmt.Sprintf("hints[%d]", i)
		if h.Cost < 0 {
			v.errorf(lvl.SourceFile, lvl.ID, field+".cost", 0, "must not be negative")
		}
		if strings.TrimSpace(h.Text) == "" && !h.RevealSolution {
			v.errorf(lvl.SourceFile, lvl.ID, field, 0,
				"a hint must have text, or set reveal_solution: true to print the level's solution")
		}
	}
}

// validateSetup enforces the containment rules on a level's world: the root
// it lives in, and every asset it materializes.
//
// This is the rule with teeth. setup.root is the path teardown later hands to
// a recursive delete inside the sandbox, so a hole here is a hole there. The
// runner in internal/content/setup checks the same thing again at execution
// time and resolves symlinks inside the sandbox as well; this is the gate
// that catches it before a level is ever played.
func (v *validator) validateSetup(lvl *Level) {
	file, id := lvl.SourceFile, lvl.ID

	if err := validateSetupRoot(lvl.Setup.Root); err != nil {
		v.errorf(file, id, "setup.root", 0, "%s", err)
	}

	for i, f := range lvl.Setup.Files {
		field := fmt.Sprintf("setup.files[%d]", i)

		if f.Path == "" {
			v.errorf(file, id, field+".path", 0, "must not be empty")
		} else if path.IsAbs(f.Path) || !fs.ValidPath(path.Clean(f.Path)) || strings.HasPrefix(path.Clean(f.Path), "..") {
			v.errorf(file, id, field+".path", 0,
				"%q must be a relative path inside the level root, with no leading slash and no .. segment", f.Path)
		}

		sources := 0
		for _, set := range []bool{f.Source != "", f.ContentSet, f.Generate != nil} {
			if set {
				sources++
			}
		}
		if sources != 1 {
			v.errorf(file, id, field, 0,
				"set exactly one of source, content and generate, found %d", sources)
		}

		if f.Source != "" {
			v.validateAssetSource(lvl, field+".source", f.Source)
		}
		if f.Generate != nil && f.Generate.Kind == "" {
			v.errorf(file, id, field+".generate.kind", 0, "must name a generator")
		}
	}
}

// validateSetupRoot refuses any level root that is not strictly inside the
// sandbox learner's home directory.
//
// The rules are ordered so that the check cannot be defeated by cleaning: a
// .. segment is refused before path.Clean runs, because cleaning is precisely
// what would hide it. The case that matters most, and the one most likely to
// be missed, is /home/learner exactly: it satisfies a naive "starts with
// /home/learner" test and it is the learner's entire home directory.
func validateSetupRoot(root string) error {
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("must not be empty: it is the path reset and teardown delete")
	}
	for _, segment := range strings.Split(root, "/") {
		if segment == ".." {
			return fmt.Errorf("%q must not contain a .. segment", root)
		}
	}
	clean := path.Clean(root)
	if !path.IsAbs(clean) {
		return fmt.Errorf("%q must be an absolute path", root)
	}
	if !strings.HasPrefix(clean, learnerHomePrefix) {
		return fmt.Errorf("%q must be a directory strictly inside %s, so that reset and teardown can never reach past it", root, learnerHomePrefix)
	}
	return nil
}

// learnerHomePrefix is the only place a level's world may live. The trailing
// slash is what makes /home/learner itself fail the prefix test rather than
// pass it.
const learnerHomePrefix = "/home/learner/"

// validateAssetSource checks that a source: names a real file inside the
// pack, and only inside the pack.
func (v *validator) validateAssetSource(lvl *Level, field, source string) {
	file, id := lvl.SourceFile, lvl.ID

	clean := path.Clean(source)
	if path.IsAbs(source) || !fs.ValidPath(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		v.errorf(file, id, field, 0,
			"%q must name a file inside the pack, as a relative path with no leading slash and no .. segment", source)
		return
	}

	if v.fsys != nil {
		if _, err := fs.Stat(v.fsys, joinPack(v.fsRoot, clean)); err != nil {
			v.errorf(file, id, field, 0, "%q is not in the pack", source)
			return
		}
	}

	if v.hostRoot != "" {
		if err := assetInsideHostRoot(v.hostRoot, clean); err != nil {
			v.errorf(file, id, field, 0, "%s", err)
		}
	}
}

// assetInsideHostRoot resolves both the pack root and the asset through every
// symlink and then compares, which is the only order that works. Comparing
// the strings first and resolving afterwards is the classic way to let a
// symlinked asset directory out of the pack.
func assetInsideHostRoot(hostRoot, source string) error {
	root, err := filepath.EvalSymlinks(hostRoot)
	if err != nil {
		return fmt.Errorf("cannot resolve the pack directory %q: %v", hostRoot, err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("cannot resolve the pack directory %q: %v", hostRoot, err)
	}

	target, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(source)))
	if err != nil {
		return fmt.Errorf("%q is not in the pack", source)
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("%q is not in the pack", source)
	}

	if target != root && !strings.HasPrefix(target, root+string(filepath.Separator)) {
		return fmt.Errorf("%q resolves to %s, which is outside the pack", source, target)
	}
	return nil
}

// validateChecks enforces the check rules, then the one-to-one correspondence
// between objective ids and check ids.
func (v *validator) validateChecks(lvl *Level) {
	file, id := lvl.SourceFile, lvl.ID

	if len(lvl.Checks) == 0 {
		v.errorf(file, id, "checks", 0, "a level must declare at least one check")
	}
	if len(lvl.Objectives) == 0 {
		v.errorf(file, id, "objectives", 0, "a level must declare at least one objective")
	}

	// bonus is keyed by objective id and is the sole answer to "is this
	// objective optional": issue #97 gave optional a single home on
	// Objective, so this is the only place gating may read the flag from. A
	// check id with no entry reads as false, which is the safe default: an
	// orphan check (one whose id matches no objective) still gates, and the
	// missing correspondence is reported on its own by
	// validateObjectiveCorrespondence below. An objective with no id is
	// skipped rather than folded into an entry for "", so that an unnamed
	// optional objective can never make an unnamed check look non-gating.
	//
	// A duplicate objective id is reduced with OR. That is not the stricter
	// direction for every rule that reads this map, and it is worth being
	// honest about which way it cuts: OR means more objectives read as a
	// bonus, so the journal rule below refuses fewer checks, while the
	// "level you cannot fail" count refuses more. The reason to pick OR is
	// not strictness, it is agreement: verifySpecs in internal/game reduces
	// the same list the same way, and nothing calls Validate on the path a
	// learner runs, so a disagreement here would let the validator see a
	// bonus objective while the engine gated on it. The duplicate itself is
	// separately an error from validateObjectiveCorrespondence.
	bonus := make(map[string]bool, len(lvl.Objectives))
	for _, o := range lvl.Objectives {
		if o.ID == "" {
			continue
		}
		bonus[o.ID] = bonus[o.ID] || o.Optional
	}

	required := 0
	checkIDs := map[string]bool{}
	for i := range lvl.Checks {
		c := &lvl.Checks[i]
		field := fmt.Sprintf("checks[%d]", i)

		if c.ID == "" {
			v.errorf(file, id, field+".id", c.Line, "a top-level check must have an id, so it can match an objective")
		} else if checkIDs[c.ID] {
			v.errorf(file, id, field+".id", c.Line, "check id %q is declared twice", c.ID)
		}
		checkIDs[c.ID] = true

		// Whether this check tree decides pass or fail. Only the outermost
		// node carries that, and it is read from this check's own
		// objective, never from the check itself: severity has no defined
		// meaning on a composition branch either, since the engine
		// evaluates a composite structurally.
		gating := !bonus[c.ID] && c.Severity != SeverityWarn

		// A tree that asserts nothing about sandbox state never counts
		// toward the required total, even when it is written as required.
		// It is refused separately, and counting it here would mask the
		// larger problem: a level gated only on the journal has nothing
		// verifying state at all, so an author fixing the first complaint
		// would only then discover the second.
		if gating && hasStateLeaf(c) {
			required++
		}

		v.validateCheckTree(lvl, field, c, gating)
	}

	if required == 0 && len(lvl.Checks) > 0 {
		v.errorf(file, id, "checks", 0,
			"a level needs at least one check that neither sits on an optional objective nor sets severity: warn: a level you cannot fail is not a level")
	}

	v.validateObjectiveCorrespondence(lvl, checkIDs)
}

// validateCheckTree walks one check and every branch beneath it. The rules
// that apply at depth zero apply at every depth: an on_fail is required on a
// composite too, because a composite that falls back to whichever child
// happened to fail explains the wrong thing to the learner.
//
// gating says whether this tree decides pass or fail, and it is decided once
// at the root and passed down unchanged. A branch cannot make itself
// non-gating by declaring optional or severity, because those fields describe
// how the engine reports a whole objective and a branch is not an objective.
func (v *validator) validateCheckTree(lvl *Level, field string, c *CheckSpec, gating bool) {
	file, id := lvl.SourceFile, lvl.ID
	branch := strings.ContainsAny(field, ".")

	if strings.TrimSpace(c.OnFail) == "" {
		v.errorf(file, id, field+".on_fail", c.Line,
			"must not be empty: name what is wrong and what to look at next, never \"incorrect\"")
	}
	if c.Severity != "" && c.Severity != SeverityFail && c.Severity != SeverityWarn {
		v.errorf(file, id, field+".severity", c.Line,
			"must be %q or %q, got %q", SeverityFail, SeverityWarn, c.Severity)
	}
	if c.TimeoutSeconds < 0 {
		v.errorf(file, id, field+".timeout_seconds", c.Line, "must not be negative")
	}

	// optional is never a field a check may declare, at any composition
	// depth: issue #97 gave it a single home on the objective, so a check
	// cannot become a bonus, or stop being one, by writing the word itself.
	// Refusing it here, rather than silently ignoring it, is what stops an
	// author from believing a check they marked optional is exempt from
	// gating when its objective still says otherwise, and it is what stops
	// the shape pipe-05's old obj3 shipped with, optional declared on both
	// the objective and the check, from ever shipping again.
	//
	// The message names deletion first and the objective second, because
	// only the presence of the key is recorded and never its value, so
	// optional: false lands here too. An author who wrote optional: false
	// asked for the default, and telling them to set optional: true on the
	// objective would talk them into turning a required objective into a
	// bonus, changing what the level gates on. Deleting the line is the
	// right next action for both values.
	if c.optionalDeclared {
		v.errorf(file, id, field+".optional", c.Line,
			"belongs on the objective, not on the check: delete it here, and set optional: true on the objective with the same id if this objective is meant to be a bonus")
	}

	// Refusing these on a branch is the other half of the gating fix. An
	// author who writes severity: warn on a branch believes they have made
	// that branch not count, and they have not: the enclosing objective
	// still passes or fails as a whole. Silently ignoring the field is how
	// a level ends up gating on something its author thought was exempt.
	if branch {
		if c.Severity != "" {
			v.errorf(file, id, field+".severity", c.Line,
				"means nothing on a composition branch: it describes a whole objective, and only the outermost check of an objective may declare it")
		}
		if c.ID != "" {
			v.errorf(file, id, field+".id", c.Line,
				"means nothing on a composition branch: only the outermost check of an objective corresponds to an objective id")
		}
	}

	composite := c.IsComposite()
	switch {
	case composite && c.Type != "":
		v.errorf(file, id, field, c.Line,
			"a check declares either a type or a composition node, not both; this one has type %q and a composition node", c.Type)
	case !composite && c.Type == "":
		v.errorf(file, id, field+".type", c.Line,
			"must name a check type, or the check must be an any_of, all_of or not composition")
	}

	if composite {
		v.validateCompositeShape(lvl, field, c)
		for i := range c.AnyOf {
			v.validateCheckTree(lvl, fmt.Sprintf("%s.any_of[%d]", field, i), &c.AnyOf[i], gating)
		}
		for i := range c.AllOf {
			v.validateCheckTree(lvl, fmt.Sprintf("%s.all_of[%d]", field, i), &c.AllOf[i], gating)
		}
		if c.Not != nil {
			v.validateCheckTree(lvl, field+".not", c.Not, gating)
		}
		return
	}

	v.validateCheckType(lvl, field, c, gating)
}

// hasStateLeaf reports whether a check tree asserts anything about sandbox
// state, as opposed to reading only the command journal.
//
// It is what stops a composite made entirely of journal checks from counting
// as the check that can fail a level.
func hasStateLeaf(c *CheckSpec) bool {
	if !c.IsComposite() {
		return !journalCheckTypes[c.Type]
	}
	for _, b := range c.Branches() {
		if hasStateLeaf(b) {
			return true
		}
	}
	return false
}

// validateCompositeShape refuses a composition node that sets more than one
// of any_of, all_of and not, or that has no branches at all.
func (v *validator) validateCompositeShape(lvl *Level, field string, c *CheckSpec) {
	set := 0
	for _, has := range []bool{len(c.AnyOf) > 0, len(c.AllOf) > 0, c.Not != nil} {
		if has {
			set++
		}
	}
	if set > 1 {
		v.errorf(lvl.SourceFile, lvl.ID, field, c.Line,
			"set exactly one of any_of, all_of and not, found %d", set)
	}
	if len(c.Params) > 0 {
		v.errorf(lvl.SourceFile, lvl.ID, field, c.Line,
			"a composition node takes no check parameters, found %s", quotedKeys(c.Params))
	}
}

// validateCheckType asks the registry whether the type exists and whether its
// parameters are well formed, then applies the one rule this package owns
// about which types may gate passing.
func (v *validator) validateCheckType(lvl *Level, field string, c *CheckSpec, gating bool) {
	file, id := lvl.SourceFile, lvl.ID

	if v.tc != nil {
		if !v.tc.Known(c.Type) {
			v.errorf(file, id, field+".type", c.Line, "unknown check type %q", c.Type)
			return
		}
		if err := v.tc.ValidateParams(c.Type, c.Params); err != nil {
			v.errorf(file, id, field, c.Line, "%v", err)
		}
	}

	// gating, resolved by the caller from this check's own objective, not
	// from anything the node declares about itself. Nesting a journal check
	// inside a required composite would otherwise satisfy the rule node by
	// node while the objective as a whole still turned on a signal the
	// learner can forge.
	switch {
	case journalCheckTypes[c.Type] && gating:
		v.errorf(file, id, field, c.Line,
			"a %s check must not decide whether a level is passed. The journal records what the learner typed, and the learner can forge it from inside the sandbox. Set optional: true on this check's objective, or severity: warn on the check itself, or move this check into an objective of its own.",
			c.Type)
	case journalCheckTypes[c.Type]:
		// A legitimately optional or severity: warn journal check is exactly
		// the shape the rule above steers an author toward, and today it is
		// silently useless: no runtime session in this build wires a real
		// verify.JournalReader (issue #88), so every command list this check
		// sees is empty. That degrades into a wrong answer rather than an
		// error, which is worse than either check type refusing to load, so
		// this warns instead of staying quiet.
		v.warnf(file, id, field,
			"%s reads the command journal, and no runtime session in this build wires a real one yet (issue #88): every command list it sees is empty. %s until then. This is harmless, since a journal check may never gate passing, but the check verifies nothing yet.",
			c.Type, journalNeverOutcome(c.Type))
	}
}

// journalNeverOutcome names what a journal check does today, with nothing
// populating the journal it reads from. The two check types fail in opposite
// directions: command_matched can never pass, and command_not_matched can
// never fire.
func journalNeverOutcome(checkType string) string {
	if checkType == "command_not_matched" {
		return "It can never fire, so the anti-pattern it warns about goes uncaught"
	}
	return "It can never pass"
}

// validateObjectiveCorrespondence enforces the one-to-one relationship in
// both directions. An objective with no check is a promise nothing verifies;
// a check with no objective is work the learner was never told to do.
func (v *validator) validateObjectiveCorrespondence(lvl *Level, checkIDs map[string]bool) {
	file, id := lvl.SourceFile, lvl.ID

	objectiveIDs := map[string]bool{}
	for i, o := range lvl.Objectives {
		field := fmt.Sprintf("objectives[%d]", i)
		if o.ID == "" {
			v.errorf(file, id, field+".id", 0, "must not be empty")
			continue
		}
		if objectiveIDs[o.ID] {
			v.errorf(file, id, field+".id", 0, "objective id %q is declared twice", o.ID)
		}
		objectiveIDs[o.ID] = true

		if strings.TrimSpace(o.Text) == "" {
			v.errorf(file, id, field+".text", 0, "must not be empty: it is the checklist line the learner reads")
		}
		if !checkIDs[o.ID] {
			v.errorf(file, id, field+".id", 0, "objective %q has no check with the same id", o.ID)
		}
	}

	for i := range lvl.Checks {
		c := &lvl.Checks[i]
		if c.ID == "" || objectiveIDs[c.ID] {
			continue
		}
		// A severity: warn check is exempt, and only in this direction. It
		// never appears on the objective checklist at all: it produces a
		// note in the result, per docs/LEVEL-FORMAT.md section 5, so there
		// is no checklist line for it to correspond to. Requiring one would
		// reject the anti-pattern warning that the same document's own
		// worked example uses and that the level-authoring skill asks for.
		// An optional check is not exempt: a bonus objective is shown to
		// the learner, so it still needs its line.
		if c.Severity == SeverityWarn {
			continue
		}
		v.errorf(file, id, fmt.Sprintf("checks[%d].id", i), c.Line,
			"check %q has no objective with the same id, so the learner is never told to do it", c.ID)
	}
}

// validatePrerequisites reports a prerequisite that names no loaded level,
// and a cycle in the unlock DAG.
//
// A dangling prerequisite is a warning when the id appears in an act, because
// that is the pack's normal state while it is being filled in, and an error
// when it appears nowhere at all, because that is a typo.
func (v *validator) validatePrerequisites() {
	loaded := map[string]bool{}
	for i := range v.pack.Levels {
		loaded[v.pack.Levels[i].ID] = true
	}
	planned := map[string]bool{}
	for _, act := range v.pack.Acts {
		for _, id := range act.Levels {
			planned[id] = true
		}
	}

	for i := range v.pack.Levels {
		lvl := &v.pack.Levels[i]
		for j, prereq := range lvl.Prerequisites {
			field := fmt.Sprintf("prerequisites[%d]", j)
			// The self case is tested before the loaded case, not after: a
			// level is always in loaded, so testing loaded first would make
			// "a level cannot be its own prerequisite" unreachable and leave
			// the author with only the downstream cycle message from Order.
			switch {
			case prereq == lvl.ID:
				v.errorf(lvl.SourceFile, lvl.ID, field, 0, "a level cannot be its own prerequisite")
			case loaded[prereq]:
			case planned[prereq]:
				v.warnf(lvl.SourceFile, lvl.ID, field,
					"prerequisite %q is listed in pack.yaml but has no level file yet", prereq)
			default:
				v.errorf(lvl.SourceFile, lvl.ID, field, 0,
					"prerequisite %q is not a level in this pack", prereq)
			}
		}
	}

	if _, err := v.pack.Order(); err != nil {
		v.errorf(packFile, "", "", 0, "%v", err)
	}
}

// validateActCoverage reports a level id an act names with no file, and a
// level file no act names.
//
// The first is a warning: pack.yaml is the plan of record and lists all
// twenty five levels while only some are written, so treating it as an error
// would mean the pack could not validate until the last level was finished.
// The second is an error, because a level with a file and no act is a level
// no learner can ever reach.
func (v *validator) validateActCoverage() {
	loaded := map[string]bool{}
	for i := range v.pack.Levels {
		loaded[v.pack.Levels[i].ID] = true
	}

	claimed := map[string]string{}
	for i, act := range v.pack.Acts {
		for j, id := range act.Levels {
			field := fmt.Sprintf("acts[%d].levels[%d]", i, j)
			if other, dup := claimed[id]; dup {
				v.errorf(packFile, "", field, 0, "level %q is already listed in act %s", id, other)
				continue
			}
			claimed[id] = act.ID
			if !loaded[id] {
				v.warnf(packFile, "", field, "level %q has no file in levels/ yet", id)
			}
		}
	}

	for i := range v.pack.Levels {
		lvl := &v.pack.Levels[i]
		if lvl.ID != "" && claimed[lvl.ID] == "" {
			v.errorf(lvl.SourceFile, lvl.ID, "id", 0,
				"no act in pack.yaml lists %q, so no learner can reach it", lvl.ID)
		}
	}
}

// quotedKeys renders a param map's keys, sorted and quoted, for an error
// message that has to name what it found.
func quotedKeys(params map[string]any) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, strconv.Quote(k))
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
