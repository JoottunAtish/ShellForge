package verify

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The verification engine: build a level's checks once, then run them as often
// as the learner types `check`.
//
// docs/LEVEL-FORMAT.md section 4 is the execution contract this implements.
// The parts that are easy to get wrong, and are each pinned by a test:
//
// Checks run in declaration order and nothing short-circuits, so the objective
// checklist is always complete. A learner reading a one-line list after their
// first attempt cannot tell what else is still wrong.
//
// Only the first failing blocking check's on_fail is shown. Telling somebody
// five things are wrong when the second follows from the first is how a
// beginner concludes the game is broken rather than their answer.
//
// A timeout and an error are not failures. "You have not solved it" and "we
// could not tell" need different words in front of a beginner, and neither of
// those two ever carries the authored on_fail, because nothing was established
// about whether the learner was right.

const (
	// DefaultCheckTimeout bounds one check, per docs/LEVEL-FORMAT.md 4.6.
	DefaultCheckTimeout = 10 * time.Second

	// DefaultLevelTimeout bounds a whole run, per docs/LEVEL-FORMAT.md 4.6.
	DefaultLevelTimeout = 60 * time.Second
)

// Engine builds and runs a level's checks.
//
// An Engine is safe for concurrent use and holds no per-run state: everything
// a run needs is in its arguments. That is what makes one Engine per process
// correct, and what makes Run repeatable against the same []Check.
type Engine struct {
	checkTimeout time.Duration
	levelTimeout time.Duration

	// lookup resolves a type name to its Factory. It is a field rather than a
	// direct call to Lookup so that tests can drive Build without registering
	// anything: Register panics on a duplicate type name and registry_test.go
	// pins the catalogue at exactly the authored fourteen, so a test that
	// registered a stub would either panic on rerun or break that pin.
	lookup func(typeName string) (Factory, bool)
}

// Option configures an Engine.
type Option func(*Engine)

// WithCheckTimeout overrides the per-check budget. A non-positive duration is
// ignored, because a zero or negative budget produces a context that has
// already expired, which would time out every check in the level with no
// explanation an author could act on.
func WithCheckTimeout(d time.Duration) Option {
	return func(e *Engine) {
		if d > 0 {
			e.checkTimeout = d
		}
	}
}

// WithLevelTimeout overrides the whole-run budget. A non-positive duration is
// ignored, for the reason given on WithCheckTimeout.
func WithLevelTimeout(d time.Duration) Option {
	return func(e *Engine) {
		if d > 0 {
			e.levelTimeout = d
		}
	}
}

// withLookup replaces the registry lookup. Unexported: it exists for this
// package's own tests, and a caller outside the package that wanted its own
// check types would be working around the registry rather than using it.
func withLookup(f func(string) (Factory, bool)) Option {
	return func(e *Engine) {
		if f != nil {
			e.lookup = f
		}
	}
}

// NewEngine returns an Engine with the documented budgets, adjusted by opts.
func NewEngine(opts ...Option) *Engine {
	e := &Engine{
		checkTimeout: DefaultCheckTimeout,
		levelTimeout: DefaultLevelTimeout,
		lookup:       Lookup,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// objectiveMeta is what Run needs about a check and cannot get from the Check
// interface: the checklist line, the authored on_fail, and the three fields
// that describe how a whole objective is reported.
//
// Build discards the Spec once it has produced a Check, so this rides alongside
// each top-level check instead. The alternative, widening the Check interface,
// would change all fourteen existing check types to carry data none of them
// reads.
type objectiveMeta struct {
	id       string
	text     string
	onFail   string
	severity string
	optional bool
	timeout  time.Duration
}

// blocking reports whether this objective decides whether the level passed.
func (m objectiveMeta) blocking() bool {
	return !m.optional && m.severity != severityWarn
}

// severityWarn is the severity value that turns a failure into a note. The
// constant is spelled here rather than imported from internal/content, which
// is a peer package this one may not import; the two agree by contract, and
// docs/LEVEL-FORMAT.md section 3 is where that contract is written down.
const severityWarn = "warn"

// objective wraps a built check with the metadata Run needs.
type objective struct {
	meta  objectiveMeta
	inner Check
}

func (o *objective) ID() string { return o.meta.id }

func (o *objective) Run(ctx context.Context, env Env) Result {
	return o.inner.Run(ctx, env)
}

// metadata exposes the wrapper's fields to Run through a type assertion.
func (o *objective) metadata() objectiveMeta { return o.meta }

// metadataOf reads the objective metadata off a check, falling back to a
// required objective with no text for a Check that Build did not produce.
//
// The fallback exists so that a hand-rolled Check, which is what a test writes,
// still runs. It deliberately reports the check blocking rather than optional:
// a check whose metadata is unknown must be able to fail a level, because the
// alternative is a level that silently cannot be failed.
func metadataOf(c Check) objectiveMeta {
	if o, ok := c.(interface{ metadata() objectiveMeta }); ok {
		return o.metadata()
	}
	return objectiveMeta{id: c.ID()}
}

// Build turns validated specs into runnable checks, once, at level load.
//
// A learner types `check` repeatedly. Re-parsing parameters on every run would
// be wasted work, and worse, it would move a parameter error from level load to
// whichever `check` first hit it, which is the worst possible moment to discover
// that a level is malformed.
//
// Every error names the check id, because an author with a pack of twenty
// levels needs to know which one to open.
func (e *Engine) Build(specs []Spec) ([]Check, error) {
	checks := make([]Check, 0, len(specs))

	for _, spec := range specs {
		inner, err := e.buildOne(spec, spec.ID, 0)
		if err != nil {
			return nil, err
		}
		checks = append(checks, &objective{
			meta: objectiveMeta{
				id:       spec.ID,
				text:     spec.Text,
				onFail:   spec.OnFail,
				severity: spec.Severity,
				optional: spec.Optional,
				timeout:  time.Duration(spec.TimeoutSeconds) * time.Second,
			},
			inner: inner,
		})
	}

	return checks, nil
}

// buildOne builds one node of a spec tree, recursing through composition
// branches.
//
// objectiveID is the id of the top-level check this node belongs to, threaded
// down unchanged. A composition branch carries no id of its own, so without it
// an error four levels deep could only say "a composition branch", which tells
// an author with twenty levels nothing about where to look. depth is the
// current nesting level, zero at the top.
func (e *Engine) buildOne(spec Spec, objectiveID string, depth int) (Check, error) {
	if depth >= maxComposeDepth {
		return nil, e.specError(objectiveID, fmt.Errorf(
			"composition nests more than %d deep; use a type: script check instead, which reads better at this depth anyway",
			maxComposeDepth))
	}

	composite := spec.IsComposite()

	// Refuse the ambiguous shapes rather than guessing which half the author
	// meant. internal/content's validator already refuses both, but Build is a
	// library entry point and pack data is not this process's own.
	if spec.Type != "" && composite {
		return nil, e.specError(objectiveID, fmt.Errorf(
			"declares both type %q and a composition node (%s); a check is either a type or exactly one of any_of, all_of, not",
			spec.Type, spec.compositionKindsSet()))
	}
	if spec.Type == "" && !composite {
		return nil, e.specError(objectiveID, fmt.Errorf(
			"declares neither a type nor a composition node; every check needs one or the other"))
	}
	if composite {
		return e.buildComposite(spec, objectiveID, depth)
	}
	return e.buildLeaf(spec, objectiveID)
}

// buildLeaf resolves a type name through the registry and calls its Factory.
func (e *Engine) buildLeaf(spec Spec, objectiveID string) (Check, error) {
	factory, ok := e.lookup(spec.Type)
	if !ok {
		return nil, e.specError(objectiveID, fmt.Errorf("unknown check type %q", spec.Type))
	}

	check, err := factory(spec)
	if err != nil {
		// A well-behaved Factory routes through factoryError, which already
		// names the type and the id, so wrapping again would print the id
		// twice. A Factory that returns a bare error would otherwise produce a
		// Build failure an author cannot attribute to a check at all, which is
		// the worse outcome, so the id is added when it is missing.
		if !strings.Contains(err.Error(), strconv.Quote(objectiveID)) {
			return nil, e.specError(objectiveID, err)
		}
		return nil, err
	}
	return check, nil
}

// buildComposite builds a composition node and its branches.
func (e *Engine) buildComposite(spec Spec, objectiveID string, depth int) (Check, error) {
	kinds := 0
	for _, set := range []bool{len(spec.AnyOf) > 0, len(spec.AllOf) > 0, spec.Not != nil} {
		if set {
			kinds++
		}
	}
	if kinds > 1 {
		return nil, e.specError(objectiveID, fmt.Errorf(
			"sets more than one of any_of, all_of and not (%s); set exactly one", spec.compositionKindsSet()))
	}

	var (
		kind        compositeKind
		branchSpecs []Spec
	)
	switch {
	case len(spec.AnyOf) > 0:
		kind, branchSpecs = compositeAnyOf, spec.AnyOf
	case len(spec.AllOf) > 0:
		kind, branchSpecs = compositeAllOf, spec.AllOf
	default:
		kind, branchSpecs = compositeNot, []Spec{*spec.Not}
	}

	if kind == compositeNot && len(branchSpecs) != 1 {
		return nil, e.specError(objectiveID, fmt.Errorf("not takes exactly one branch, got %d", len(branchSpecs)))
	}

	branches := make([]Check, 0, len(branchSpecs))
	for _, branchSpec := range branchSpecs {
		// A branch inherits the objective's id when it has none of its own.
		// This is safe because a composite discards its children's Results
		// entirely and reports its own, so a branch's Check.ID is never
		// observed by anything; what it buys is a Factory error that names the
		// objective an author can search for rather than saying check "".
		if branchSpec.ID == "" {
			branchSpec.ID = objectiveID
		}
		branch, err := e.buildOne(branchSpec, objectiveID, depth+1)
		if err != nil {
			return nil, err
		}
		branches = append(branches, branch)
	}

	return newComposite(spec.ID, kind, spec.OnFail, branches), nil
}

// compositionKindsSet names the composition keys a spec actually set, for an
// error message that tells the author which keys collided.
func (s Spec) compositionKindsSet() string {
	var set []string
	if len(s.AnyOf) > 0 {
		set = append(set, "any_of")
	}
	if len(s.AllOf) > 0 {
		set = append(set, "all_of")
	}
	if s.Not != nil {
		set = append(set, "not")
	}
	if len(set) == 0 {
		return "none"
	}
	out := set[0]
	for _, k := range set[1:] {
		out += ", " + k
	}
	return out
}

// specError wraps err with the id of the objective the failure belongs to.
func (e *Engine) specError(objectiveID string, err error) error {
	if objectiveID == "" {
		// A top-level check with no id at all. The pack validator refuses one,
		// so this is only reachable from a caller building specs by hand.
		return fmt.Errorf("verify: a check with no id: %w", err)
	}
	return fmt.Errorf("verify: check %q: %w", objectiveID, err)
}

// Run evaluates every check and assembles the level result.
//
// It returns a LevelResult and never an error. A caller always has something to
// render: a run that was cancelled, or one whose sandbox stopped answering,
// still produces a complete checklist saying so. A caller that wants to know
// whether the learner interrupted it inspects ctx.Err(), which is the idiomatic
// answer and avoids a field docs/LEVEL-FORMAT.md section 5 does not have.
//
// The level budget marks rather than skips. Section 4 rule 4's stated purpose
// for no-short-circuit is that the checklist is fully populated, so an expired
// budget leaves every remaining objective in the list, marked as not evaluated,
// rather than dropping it.
func (e *Engine) Run(ctx context.Context, checks []Check, env Env) LevelResult {
	start := time.Now()

	levelCtx, cancelLevel := context.WithTimeout(ctx, e.levelTimeout)
	defer cancelLevel()

	res := LevelResult{
		LevelID:    env.LevelID,
		Objectives: make([]ObjectiveResult, 0, len(checks)),
		Notes:      make([]string, 0),
	}

	var cancelled, budgetSpent bool

	for _, check := range checks {
		meta := metadataOf(check)

		// The parent context is tested before the level context, because
		// levelCtx is done in both cases and only the parent's own error tells
		// a learner's Ctrl-C apart from a slow level.
		switch {
		case ctx.Err() != nil:
			cancelled = true
			res.append(meta, Result{
				ID:      meta.id,
				Status:  StatusError,
				Message: "not evaluated: the check run was cancelled",
			})
			continue
		case levelCtx.Err() != nil:
			budgetSpent = true
			res.append(meta, Result{
				ID:     meta.id,
				Status: StatusTimeout,
				Message: fmt.Sprintf("not evaluated: the %s budget for this level's checks ran out",
					e.levelTimeout),
			})
			continue
		}

		res.append(meta, e.runOne(levelCtx, check, meta, env))
	}

	// The engine's own notes go last, after every authored one, so a learner
	// reads what the level author wanted to say before reading what the engine
	// had to say about the run.
	if budgetSpent {
		res.Notes = append(res.Notes, fmt.Sprintf(
			"The %s budget for this level's checks ran out, so some objectives were not evaluated. "+
				"This is not a wrong answer. Run `check` again, and if it keeps happening the level is too slow to verify.",
			e.levelTimeout))
	}
	if cancelled {
		res.Notes = append(res.Notes, "The check run was cancelled, so some objectives were not evaluated.")
	}

	res.finish(cancelled)
	res.DurationMS = time.Since(start).Milliseconds()
	return res
}

// runOne runs a single check under its own budget and normalises the outcome.
func (e *Engine) runOne(levelCtx context.Context, check Check, meta objectiveMeta, env Env) Result {
	budget := meta.timeout
	if budget <= 0 {
		budget = e.checkTimeout
	}

	checkCtx, cancel := context.WithTimeout(levelCtx, budget)
	defer cancel()

	result := runGuarded(checkCtx, check, env)

	// A check that honoured its context reports something of its own choosing,
	// and a check that ignored it reports nothing at all because runGuarded
	// gave up waiting. Either way, an expired per-check budget is a timeout,
	// and the message says so in the learner's terms rather than repeating
	// "context deadline exceeded".
	if checkCtx.Err() != nil && result.Status != StatusPass {
		if levelCtx.Err() != nil {
			// The level ran out, not this check. Reported by the caller's
			// budget branch on the next iteration; here it means this check
			// was the one that consumed the last of it.
			return Result{
				ID:     meta.id,
				Status: StatusTimeout,
				Message: fmt.Sprintf("the %s budget for this level's checks ran out while this objective was being checked",
					e.levelTimeout),
				Duration: result.Duration,
			}
		}
		return Result{
			ID:     meta.id,
			Status: StatusTimeout,
			Message: fmt.Sprintf("this check did not finish within %s, so it was stopped. "+
				"That is not a wrong answer: something it read did not come back. "+
				"If a file it looks at is a named pipe or a link to a device, remove it and run `check` again.", budget),
			Duration: result.Duration,
		}
	}

	return result
}

// runGuarded runs check on its own goroutine and gives up when ctx is done.
//
// The goroutine is what makes the per-check budget true for a check that does
// not honour its context. The `script` escape hatch runs author-supplied bash
// through one Exec, and a backend whose cancellation is imperfect is exactly
// what the budget exists to survive.
//
// A panic is recovered into StatusError rather than allowed to escape. recover
// only catches a panic raised in its own goroutine, so an unrecovered panic
// here would kill the process: the learner would lose the whole checklist, and
// the host terminal is in raw mode at that point because internal/pty put it
// there.
//
// A check that never returns leaks one goroutine for the life of the process.
// That is accepted deliberately: the alternative is blocking the learner's
// prompt on a check that will not come back, and one parked goroutine in a
// session-scoped process is the smaller problem. TODO(v0.2): if a level ever
// makes this common, the fix is on the runtime side, killing the sandbox
// process the check started, not here.
func runGuarded(ctx context.Context, check Check, env Env) Result {
	done := make(chan Result, 1)

	go func() {
		defer func() {
			if v := recover(); v != nil {
				done <- Result{
					ID:      check.ID(),
					Status:  StatusError,
					Message: fmt.Sprintf("this check could not run: %v", v),
				}
			}
		}()
		done <- check.Run(ctx, env)
	}()

	select {
	case result := <-done:
		return result
	case <-ctx.Done():
		// The caller decides what an expired context means, because only it
		// knows whether the check's own budget or the level's ran out.
		return Result{ID: check.ID(), Status: StatusTimeout}
	}
}

// append records one check's outcome on the result, applying the severity and
// optional rules from docs/LEVEL-FORMAT.md section 5.
//
// A severity: warn check produces a note and no checklist entry. The authoring
// invariants say such a check needs no objective, so emitting a line for one
// would show the learner something they were never asked to do.
func (r *LevelResult) append(meta objectiveMeta, result Result) {
	warn := meta.severity == severityWarn
	passed := result.Status == StatusPass

	if warn {
		if !passed && result.Message != "" {
			r.Notes = append(r.Notes, result.Message)
		}
		return
	}

	status := result.Status
	message := result.Message
	if passed {
		// on_fail describes a failure. A passing objective says nothing, so a
		// check that returned a message anyway does not get to print it.
		message = ""
	}

	r.Objectives = append(r.Objectives, ObjectiveResult{
		ID:       meta.id,
		Text:     objectiveText(meta),
		Status:   status,
		Optional: meta.optional,
		Message:  message,
	})

	// A bonus objective the learner missed is worth a note as well as a
	// checklist line: the line says they did not get it, the note says what
	// they could have done instead.
	if meta.optional && !passed && message != "" {
		r.Notes = append(r.Notes, message)
	}
}

// objectiveText falls back to the objective's id when a level supplied no text.
//
// An empty checklist line renders as a blank bullet, which reads as a rendering
// bug rather than as a level missing a field. The id is not friendly, but it is
// something the learner can quote in a bug report.
func objectiveText(meta objectiveMeta) string {
	if meta.text != "" {
		return meta.text
	}
	return meta.id
}

// finish computes Passed and PrimaryFailure once every objective is recorded.
func (r *LevelResult) finish(cancelled bool) {
	r.Passed = true

	for i := range r.Objectives {
		obj := r.Objectives[i]
		if !obj.blocking() {
			continue
		}
		if obj.Status == StatusPass {
			continue
		}

		r.Passed = false
		if r.PrimaryFailure == nil {
			// A copy, not a pointer into the slice, so a caller that sorts or
			// filters Objectives cannot make this field describe a different
			// objective than the one it was computed from.
			failure := obj
			r.PrimaryFailure = &failure
		}
	}

	// A cancelled run establishes nothing, so it has no headline failure to
	// show. Leaving one set would tell the learner their answer was wrong when
	// the truth is that they pressed Ctrl-C.
	if cancelled {
		r.Passed = false
		r.PrimaryFailure = nil
	}
}
