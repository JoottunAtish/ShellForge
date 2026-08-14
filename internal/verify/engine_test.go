package verify

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// blockingCheck blocks until it is released, and deliberately ignores ctx.
//
// Ignoring the context is the whole point. A well-behaved check honours
// cancellation, so a timeout test built only from well-behaved checks proves
// nothing about the engine: the check would have returned on its own. The
// `script` escape hatch runs arbitrary author-supplied bash through one Exec,
// and a runtime whose cancellation is imperfect is exactly the case the
// engine's own budget has to survive.
type blockingCheck struct {
	id      string
	release chan struct{}
	ran     bool
}

func newBlockingCheck(t *testing.T, id string) *blockingCheck {
	c := &blockingCheck{id: id, release: make(chan struct{})}
	// Released on cleanup so the goroutine the engine started does not outlive
	// the test. The engine documents that it leaks one goroutine per check that
	// never returns; a test that relied on that leak would be a slow leak in
	// the suite.
	t.Cleanup(func() { close(c.release) })
	return c
}

func (b *blockingCheck) ID() string { return b.id }

func (b *blockingCheck) Run(_ context.Context, _ Env) Result {
	b.ran = true
	<-b.release
	return Result{ID: b.id, Status: StatusPass}
}

// panickingCheck panics, which the engine must contain.
type panickingCheck struct{ id string }

func (p *panickingCheck) ID() string { return p.id }

func (p *panickingCheck) Run(_ context.Context, _ Env) Result {
	panic("a check panicked: " + p.id)
}

// testEngine builds an Engine whose registry lookups are satisfied from specs,
// so no test in this file ever calls Register. Register panics on a duplicate
// and registry_test.go pins the catalogue at exactly fourteen names, so a test
// that registered a stub type would either panic on a rerun or break that pin.
func testEngine(t *testing.T, factories map[string]Factory, opts ...Option) *Engine {
	t.Helper()
	lookup := func(name string) (Factory, bool) {
		f, ok := factories[name]
		return f, ok
	}
	return NewEngine(append([]Option{withLookup(lookup)}, opts...)...)
}

// stubFactory returns a Factory producing a stubCheck with a fixed status. The
// returned map is keyed by a type name the tests invent, which is safe because
// nothing here touches the real registry.
func stubFactories(statuses map[string]Status) map[string]Factory {
	out := map[string]Factory{}
	for name, status := range statuses {
		status := status
		out[name] = func(s Spec) (Check, error) {
			return &stubCheck{id: s.ID, status: status, msg: s.OnFail}, nil
		}
	}
	return out
}

// leafSpec is shorthand for a leaf Spec of an invented type.
func leafSpec(id, typeName, onFail string) Spec {
	return Spec{ID: id, Text: "objective " + id, Type: typeName, OnFail: onFail}
}

// runEngine builds and runs specs, failing the test on a build error.
func runEngine(t *testing.T, e *Engine, specs []Spec) LevelResult {
	t.Helper()
	checks, err := e.Build(specs)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return e.Run(context.Background(), checks, Env{LevelID: "test-01"})
}

// --- Build ---

// TestBuildFailsAtLoadNotAtRun is the build-once property #52 asks for. A
// learner types `check` repeatedly, so a parameter error must surface when the
// level loads, not on whichever `check` happens to hit it.
func TestBuildFailsAtLoadNotAtRun(t *testing.T) {
	wantErr := errors.New("param \"path\" must not be empty")
	factories := map[string]Factory{
		"broken": func(Spec) (Check, error) { return nil, wantErr },
	}
	e := testEngine(t, factories)

	_, err := e.Build([]Spec{leafSpec("obj1", "broken", "message")})
	if err == nil {
		t.Fatal("Build returned no error for a Factory that rejected its params")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("Build error does not wrap the Factory's own error: %v", err)
	}
	if !strings.Contains(err.Error(), "obj1") {
		t.Errorf("Build error does not name the check id, so an author cannot find it: %v", err)
	}
}

func TestBuildRejectsAnUnknownType(t *testing.T) {
	e := testEngine(t, nil)
	_, err := e.Build([]Spec{leafSpec("obj1", "no_such_type", "message")})
	if err == nil {
		t.Fatal("Build accepted an unregistered check type")
	}
	if !strings.Contains(err.Error(), "no_such_type") {
		t.Errorf("Build error does not name the unknown type: %v", err)
	}
}

// TestBuildRejectsTypeAndCompositionTogether is the shape that makes the
// documented file_mode `any_of` parameter unreachable from YAML. internal/content
// already refuses it, but Build cannot assume its caller validated: it is a
// library entry point and pack data is not this process's own.
func TestBuildRejectsTypeAndCompositionTogether(t *testing.T) {
	factories := stubFactories(map[string]Status{"leaf": StatusPass})
	e := testEngine(t, factories)

	spec := Spec{
		ID: "obj1", Type: "leaf", OnFail: "message",
		AnyOf: []Spec{leafSpec("", "leaf", "branch message")},
	}
	_, err := e.Build([]Spec{spec})
	if err == nil {
		t.Fatal("Build accepted a spec that is both a type and a composition node")
	}
	for _, want := range []string{"obj1", "leaf", "any_of"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Build error does not mention %q, so an author cannot see the collision: %v", want, err)
		}
	}
}

func TestBuildRejectsMoreThanOneCompositionKind(t *testing.T) {
	factories := stubFactories(map[string]Status{"leaf": StatusPass})
	e := testEngine(t, factories)

	spec := Spec{
		ID: "obj1", OnFail: "message",
		AnyOf: []Spec{leafSpec("", "leaf", "a")},
		AllOf: []Spec{leafSpec("", "leaf", "b")},
	}
	if _, err := e.Build([]Spec{spec}); err == nil {
		t.Fatal("Build accepted a spec setting both any_of and all_of")
	}
}

func TestBuildRejectsASpecThatIsNeitherTypeNorComposite(t *testing.T) {
	e := testEngine(t, nil)
	if _, err := e.Build([]Spec{{ID: "obj1", OnFail: "message"}}); err == nil {
		t.Fatal("Build accepted a spec with no type and no composition node")
	}
}

// TestBuildRejectsACompositionNodeWithNoBranches guards the vacuous-pass shape.
// An all_of over zero branches passes under a naive fold, which would turn an
// authoring slip into a level that cannot be failed.
//
// A nil Not is not tested here: it is indistinguishable from a plain check with
// no type, which TestBuildRejectsASpecThatIsNeitherTypeNorComposite covers. An
// empty any_of is the interesting case, because IsComposite reads it as a
// composition node.
func TestBuildRejectsACompositionNodeWithNoBranches(t *testing.T) {
	e := testEngine(t, nil)
	if _, err := e.Build([]Spec{{ID: "obj1", OnFail: "message", AnyOf: []Spec{}}}); err == nil {
		t.Fatal("Build accepted a composition node with no branches; an empty all_of cannot be failed")
	}
}

// TestBuildRefusesTooDeepATree bounds recursion over pack data.
func TestBuildRefusesTooDeepATree(t *testing.T) {
	factories := stubFactories(map[string]Status{"leaf": StatusPass})
	e := testEngine(t, factories)

	// Nest one past the limit.
	spec := leafSpec("", "leaf", "innermost")
	for i := 0; i <= maxComposeDepth; i++ {
		spec = Spec{OnFail: "wrapper", AllOf: []Spec{spec}}
	}
	spec.ID = "obj1"

	_, err := e.Build([]Spec{spec})
	if err == nil {
		t.Fatalf("Build accepted a tree nested deeper than maxComposeDepth (%d)", maxComposeDepth)
	}
	if !strings.Contains(err.Error(), "obj1") {
		t.Errorf("Build error does not name the check id: %v", err)
	}
}

func TestBuildAcceptsATreeAtTheDepthLimit(t *testing.T) {
	factories := stubFactories(map[string]Status{"leaf": StatusPass})
	e := testEngine(t, factories)

	spec := leafSpec("", "leaf", "innermost")
	for i := 0; i < maxComposeDepth-1; i++ {
		spec = Spec{OnFail: "wrapper", AllOf: []Spec{spec}}
	}
	spec.ID = "obj1"

	if _, err := e.Build([]Spec{spec}); err != nil {
		t.Errorf("Build rejected a tree at the depth limit: %v", err)
	}
}

// --- Run: ordering and no short-circuit ---

// TestRunDoesNotShortCircuitOnFailure is acceptance criterion 2. The checklist
// must be complete even when the first objective fails, because a learner
// reading a one-line list cannot tell what else is still wrong.
func TestRunDoesNotShortCircuitOnFailure(t *testing.T) {
	factories := stubFactories(map[string]Status{
		"failing": StatusFail,
		"passing": StatusPass,
	})
	e := testEngine(t, factories)

	res := runEngine(t, e, []Spec{
		leafSpec("obj1", "failing", "first failed"),
		leafSpec("obj2", "passing", "second"),
		leafSpec("obj3", "passing", "third"),
	})

	if len(res.Objectives) != 3 {
		t.Fatalf("got %d objectives, want 3: a failure must not stop the run", len(res.Objectives))
	}
	for i, want := range []string{"obj1", "obj2", "obj3"} {
		if res.Objectives[i].ID != want {
			t.Errorf("objectives[%d].ID = %q, want %q: results are in declaration order", i, res.Objectives[i].ID, want)
		}
	}
	if res.Objectives[1].Status != StatusPass || res.Objectives[2].Status != StatusPass {
		t.Error("a check after the failing one did not run")
	}
}

func TestRunCopiesTheLevelIDFromEnv(t *testing.T) {
	e := testEngine(t, stubFactories(map[string]Status{"passing": StatusPass}))
	checks, err := e.Build([]Spec{leafSpec("obj1", "passing", "m")})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	res := e.Run(context.Background(), checks, Env{LevelID: "pipe-05"})
	if res.LevelID != "pipe-05" {
		t.Errorf("LevelID = %q, want %q", res.LevelID, "pipe-05")
	}
}

func TestRunCarriesObjectiveTextOntoTheResult(t *testing.T) {
	e := testEngine(t, stubFactories(map[string]Status{"passing": StatusPass}))
	res := runEngine(t, e, []Spec{
		{ID: "obj1", Text: "report.txt holds the count", Type: "passing", OnFail: "m"},
	})
	if len(res.Objectives) != 1 {
		t.Fatalf("got %d objectives, want 1", len(res.Objectives))
	}
	if res.Objectives[0].Text != "report.txt holds the count" {
		t.Errorf("Text = %q, want the authored objective line", res.Objectives[0].Text)
	}
}

// --- Run: primary failure, notes, passed ---

// TestPrimaryFailureIsFirstRequiredFailure is acceptance criterion 3, and the
// Day 2 exit criterion "only the first failing required objective is shown".
func TestPrimaryFailureIsFirstRequiredFailure(t *testing.T) {
	factories := stubFactories(map[string]Status{
		"failing": StatusFail,
		"passing": StatusPass,
	})
	e := testEngine(t, factories)

	optional := leafSpec("obj1", "failing", "the optional bonus message")
	optional.Optional = true
	warn := leafSpec("obj3", "failing", "the warn message")
	warn.Severity = "warn"

	res := runEngine(t, e, []Spec{
		optional,
		leafSpec("obj2", "failing", "the first required failure"),
		warn,
		leafSpec("obj4", "failing", "the second required failure"),
		leafSpec("obj5", "passing", "unused"),
	})

	if res.PrimaryFailure == nil {
		t.Fatal("PrimaryFailure is nil with two required failures present")
	}
	if res.PrimaryFailure.ID != "obj2" {
		t.Errorf("PrimaryFailure.ID = %q, want %q: neither the optional nor the warn objective may be the headline", res.PrimaryFailure.ID, "obj2")
	}
	if res.PrimaryFailure.Message != "the first required failure" {
		t.Errorf("PrimaryFailure.Message = %q, want the authored on_fail", res.PrimaryFailure.Message)
	}
	if res.Passed {
		t.Error("Passed is true with a required failure")
	}

	// The optional and warn messages are notes, and the required failures are
	// not: a note is for something that did not block.
	joined := strings.Join(res.Notes, "\n")
	for _, want := range []string{"the optional bonus message", "the warn message"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Notes is missing %q:\n%s", want, joined)
		}
	}
	for _, unwanted := range []string{"the first required failure", "the second required failure"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("Notes contains the blocking message %q; a blocking failure is the primary failure, not a note", unwanted)
		}
	}
}

// TestOnlyTheOptionalFailingLeavesPassedTrue is the other half of criterion 3.
func TestOnlyTheOptionalFailingLeavesPassedTrue(t *testing.T) {
	factories := stubFactories(map[string]Status{
		"failing": StatusFail,
		"passing": StatusPass,
	})
	e := testEngine(t, factories)

	optional := leafSpec("obj2", "failing", "bonus missed")
	optional.Optional = true

	res := runEngine(t, e, []Spec{
		leafSpec("obj1", "passing", "unused"),
		optional,
	})

	if !res.Passed {
		t.Error("Passed is false when only an optional objective failed; a bonus never blocks")
	}
	if res.PrimaryFailure != nil {
		t.Errorf("PrimaryFailure = %+v, want nil", res.PrimaryFailure)
	}
	if len(res.Objectives) != 2 {
		t.Fatalf("got %d objectives, want 2: an optional objective is still shown on the checklist", len(res.Objectives))
	}
	if !res.Objectives[1].Optional {
		t.Error("the optional objective did not carry Optional on its result")
	}
}

// TestWarnFailureDoesNotClearPassed is criterion 3's warn case. A warn check is
// advice about how the learner solved it, never a gate.
func TestWarnFailureDoesNotClearPassed(t *testing.T) {
	factories := stubFactories(map[string]Status{
		"failing": StatusFail,
		"passing": StatusPass,
	})
	e := testEngine(t, factories)

	warn := leafSpec("nocheat", "failing", "hardcoding works today, but not at 3 AM")
	warn.Severity = "warn"

	res := runEngine(t, e, []Spec{leafSpec("obj1", "passing", "unused"), warn})

	if !res.Passed {
		t.Error("Passed is false with only a severity: warn failure")
	}
	if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "3 AM") {
		t.Errorf("the warn message did not become a note: %v", res.Notes)
	}
}

// TestWarnCheckProducesNoObjectiveEntry pins the authoring invariant that a
// severity: warn check needs no objective. If the engine emitted a checklist
// line for one, every level with an anti-pattern check would show the learner a
// line they were never told to satisfy.
func TestWarnCheckProducesNoObjectiveEntry(t *testing.T) {
	factories := stubFactories(map[string]Status{"failing": StatusFail, "passing": StatusPass})
	e := testEngine(t, factories)

	warn := leafSpec("nocheat", "failing", "a note")
	warn.Severity = "warn"

	res := runEngine(t, e, []Spec{leafSpec("obj1", "passing", "unused"), warn})

	for _, obj := range res.Objectives {
		if obj.ID == "nocheat" {
			t.Errorf("a severity: warn check produced a checklist entry %+v; it produces a note and nothing else", obj)
		}
	}
	if len(res.Objectives) != 1 {
		t.Errorf("got %d objectives, want 1", len(res.Objectives))
	}
}

// TestWarnCheckThatPassesSaysNothing keeps a satisfied anti-pattern check
// silent. The learner did not hardcode the answer; there is nothing to report.
func TestWarnCheckThatPassesSaysNothing(t *testing.T) {
	factories := stubFactories(map[string]Status{"passing": StatusPass})
	e := testEngine(t, factories)

	warn := leafSpec("nocheat", "passing", "would have been a note")
	warn.Severity = "warn"

	res := runEngine(t, e, []Spec{leafSpec("obj1", "passing", "unused"), warn})

	if len(res.Notes) != 0 {
		t.Errorf("a passing warn check produced notes %v, want none", res.Notes)
	}
	if len(res.Objectives) != 1 {
		t.Errorf("got %d objectives, want 1", len(res.Objectives))
	}
}

// TestPassingRunHasNoPrimaryFailureAndNoNotes is the happy path, asserted
// because it is what a learner sees most often.
func TestPassingRunHasNoPrimaryFailureAndNoNotes(t *testing.T) {
	e := testEngine(t, stubFactories(map[string]Status{"passing": StatusPass}))
	res := runEngine(t, e, []Spec{
		leafSpec("obj1", "passing", "unused"),
		leafSpec("obj2", "passing", "unused"),
	})

	if !res.Passed {
		t.Error("Passed is false for a run where every check passed")
	}
	if res.PrimaryFailure != nil {
		t.Errorf("PrimaryFailure = %+v, want nil", res.PrimaryFailure)
	}
	if len(res.Notes) != 0 {
		t.Errorf("Notes = %v, want empty", res.Notes)
	}
	for _, obj := range res.Objectives {
		if obj.Message != "" {
			t.Errorf("objective %s carries message %q on a pass; on_fail describes a failure", obj.ID, obj.Message)
		}
	}
}

// TestRunAllocatesSlicesEvenWhenEmpty is what makes the null-versus-empty
// promise in result.go true rather than aspirational.
func TestRunAllocatesSlicesEvenWhenEmpty(t *testing.T) {
	e := testEngine(t, nil)
	res := e.Run(context.Background(), nil, Env{LevelID: "empty"})

	if res.Objectives == nil {
		t.Error("Objectives is nil; it must be an allocated empty slice so the JSON is [] not null")
	}
	if res.Notes == nil {
		t.Error("Notes is nil; it must be an allocated empty slice so the JSON is [] not null")
	}
}

// --- Run: timeouts ---

// TestCheckTimeoutIsDistinctFromFailure is acceptance criterion 4. A timeout
// must not read as a wrong answer: the remediation is different, and telling a
// learner their answer is wrong when the truth is that a read never finished is
// the worse failure of the two.
func TestCheckTimeoutIsDistinctFromFailure(t *testing.T) {
	const authored = "AUTHORED: report.txt is missing or the number is off"

	blocked := newBlockingCheck(t, "obj1")
	factories := map[string]Factory{
		"blocking": func(Spec) (Check, error) { return blocked, nil },
		"passing":  func(s Spec) (Check, error) { return &stubCheck{id: s.ID, status: StatusPass}, nil },
	}
	e := testEngine(t, factories, WithCheckTimeout(20*time.Millisecond))

	done := make(chan LevelResult, 1)
	go func() {
		done <- runEngine(t, e, []Spec{
			leafSpec("obj1", "blocking", authored),
			leafSpec("obj2", "passing", "unused"),
		})
	}()

	var res LevelResult
	select {
	case res = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s; the per-check budget is not enforced for a check that ignores its context")
	}

	if len(res.Objectives) != 2 {
		t.Fatalf("got %d objectives, want 2: a timeout must not stop the run", len(res.Objectives))
	}
	if res.Objectives[0].Status != StatusTimeout {
		t.Errorf("objectives[0].Status = %q, want %q", res.Objectives[0].Status, StatusTimeout)
	}
	if strings.Contains(res.Objectives[0].Message, "AUTHORED") {
		t.Errorf("a timeout carried the authored on_fail %q; we never established the learner was wrong", res.Objectives[0].Message)
	}
	if res.Objectives[0].Message == "" {
		t.Error("a timeout carried no message at all")
	}
	if res.Objectives[1].Status != StatusPass {
		t.Errorf("objectives[1].Status = %q, want %q: the check after the timeout must still run", res.Objectives[1].Status, StatusPass)
	}
	if res.Passed {
		t.Error("Passed is true with a timed-out required objective")
	}
}

// TestPerCheckTimeoutHonoursTheSpecOverride proves a level's own
// timeout_seconds reaches Run. Build throws the Spec away once it has produced a
// Check, so the per-check budget only survives because the engine copies it onto
// the objective wrapper; a bug that dropped it would leave every check on the
// engine default and no other test would notice.
//
// The assertion is arranged so the override has to WIN for the test to pass: the
// engine default is set punishingly low and the spec asks for much longer than
// the check needs. A dropped override times the check out; an honoured one lets
// it finish. Going the other way, a generous default and a tight override, would
// need a second of wall clock, because timeout_seconds is authored in whole
// seconds.
func TestPerCheckTimeoutHonoursTheSpecOverride(t *testing.T) {
	factories := map[string]Factory{
		"slow": func(s Spec) (Check, error) {
			return &stubCheck{id: s.ID, status: StatusPass, delay: 150 * time.Millisecond}, nil
		},
	}
	e := testEngine(t, factories,
		WithCheckTimeout(10*time.Millisecond),
		WithLevelTimeout(10*time.Second),
	)

	spec := leafSpec("obj1", "slow", "authored")
	spec.TimeoutSeconds = 5

	res := runEngine(t, e, []Spec{spec})

	if got := res.Objectives[0].Status; got != StatusPass {
		t.Errorf("status = %q, want %q: the check's own timeout_seconds of %ds must override the engine's %s default",
			got, StatusPass, spec.TimeoutSeconds, 10*time.Millisecond)
	}
}

// TestZeroTimeoutSecondsFallsBackToTheEngineDefault is the other half of the
// same contract, stated on Spec.TimeoutSeconds: zero means the package default,
// not "no budget at all". A bug that treated zero as unlimited would let one
// slow check hang a learner's `check` indefinitely.
func TestZeroTimeoutSecondsFallsBackToTheEngineDefault(t *testing.T) {
	blocked := newBlockingCheck(t, "obj1")
	factories := map[string]Factory{
		"blocking": func(Spec) (Check, error) { return blocked, nil },
	}
	e := testEngine(t, factories, WithCheckTimeout(20*time.Millisecond))

	spec := leafSpec("obj1", "blocking", "authored")
	spec.TimeoutSeconds = 0

	done := make(chan LevelResult, 1)
	go func() { done <- runEngine(t, e, []Spec{spec}) }()

	select {
	case res := <-done:
		if got := res.Objectives[0].Status; got != StatusTimeout {
			t.Errorf("status = %q, want %q", got, StatusTimeout)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s; timeout_seconds of zero was treated as no budget rather than the engine default")
	}
}

// TestLevelBudgetMarksTheRemainder is criterion 4's level half. Exceeding the
// level budget must not violate the no-short-circuit invariant by silently
// dropping objectives: the checklist stays complete, with the unvisited
// entries marked.
func TestLevelBudgetMarksTheRemainder(t *testing.T) {
	factories := map[string]Factory{
		"slow": func(s Spec) (Check, error) {
			return &stubCheck{id: s.ID, status: StatusPass, delay: 30 * time.Millisecond}, nil
		},
	}
	e := testEngine(t, factories,
		WithCheckTimeout(time.Second),
		WithLevelTimeout(45*time.Millisecond),
	)

	res := runEngine(t, e, []Spec{
		leafSpec("obj1", "slow", "a"),
		leafSpec("obj2", "slow", "b"),
		leafSpec("obj3", "slow", "c"),
		leafSpec("obj4", "slow", "d"),
	})

	if len(res.Objectives) != 4 {
		t.Fatalf("got %d objectives, want 4: the checklist stays complete when the level budget runs out", len(res.Objectives))
	}

	marked := 0
	for _, obj := range res.Objectives {
		if obj.Status == StatusTimeout {
			marked++
			if !strings.Contains(strings.ToLower(obj.Message), "budget") {
				t.Errorf("objective %s timed out without saying the level budget ran out: %q", obj.ID, obj.Message)
			}
		}
	}
	if marked == 0 {
		t.Fatal("no objective was marked as timed out; the level budget did nothing")
	}
	if res.Passed {
		t.Error("Passed is true after the level budget ran out")
	}

	joined := strings.Join(res.Notes, "\n")
	if !strings.Contains(strings.ToLower(joined), "budget") {
		t.Errorf("Notes does not mention the level budget:\n%s", joined)
	}
}

// --- Run: cancellation ---

// TestCancelledContextStopsPromptlyAndReportsCleanly is acceptance criterion 5.
// The learner can Ctrl-C a `check`, and what comes back has to be coherent.
func TestCancelledContextStopsPromptlyAndReportsCleanly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	first := &stubCheck{id: "obj1", status: StatusPass, before: cancel}
	second := &stubCheck{id: "obj2", status: StatusPass}
	third := &stubCheck{id: "obj3", status: StatusPass}

	byID := map[string]*stubCheck{"obj1": first, "obj2": second, "obj3": third}
	factories := map[string]Factory{
		"stub": func(s Spec) (Check, error) { return byID[s.ID], nil },
	}
	e := testEngine(t, factories)

	checks, err := e.Build([]Spec{
		leafSpec("obj1", "stub", "a"),
		leafSpec("obj2", "stub", "b"),
		leafSpec("obj3", "stub", "c"),
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	done := make(chan LevelResult, 1)
	go func() { done <- e.Run(ctx, checks, Env{LevelID: "test-01"}) }()

	var res LevelResult
	select {
	case res = <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return within 1s of cancellation")
	}

	if second.ran || third.ran {
		t.Error("a check ran after the context was cancelled")
	}
	if len(res.Objectives) != 3 {
		t.Fatalf("got %d objectives, want 3: a cancelled run still reports every objective", len(res.Objectives))
	}
	for _, obj := range res.Objectives[1:] {
		if obj.Status != StatusError {
			t.Errorf("objective %s has status %q after cancellation, want %q", obj.ID, obj.Status, StatusError)
		}
		if !strings.Contains(strings.ToLower(obj.Message), "cancel") {
			t.Errorf("objective %s does not say it was cancelled: %q", obj.ID, obj.Message)
		}
	}
	if res.PrimaryFailure != nil {
		t.Errorf("PrimaryFailure = %+v after cancellation, want nil: nothing was established", res.PrimaryFailure)
	}
	if res.Passed {
		t.Error("Passed is true for a cancelled run")
	}
	if !strings.Contains(strings.ToLower(strings.Join(res.Notes, "\n")), "cancel") {
		t.Errorf("Notes does not mention cancellation: %v", res.Notes)
	}
}

// TestAPreCancelledContextRunsNothing is the degenerate case: a learner who hit
// Ctrl-C before the first check started must not pay for one anyway.
func TestAPreCancelledContextRunsNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	only := &stubCheck{id: "obj1", status: StatusPass}
	factories := map[string]Factory{
		"stub": func(Spec) (Check, error) { return only, nil },
	}
	e := testEngine(t, factories)
	checks, err := e.Build([]Spec{leafSpec("obj1", "stub", "a")})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	res := e.Run(ctx, checks, Env{LevelID: "test-01"})
	if only.ran {
		t.Error("a check ran under an already cancelled context")
	}
	if len(res.Objectives) != 1 || res.Objectives[0].Status != StatusError {
		t.Errorf("objectives = %+v, want one entry with status %q", res.Objectives, StatusError)
	}
}

// --- Run: panic containment ---

// TestRunContainsAPanickingCheck matters because Run puts each check on its own
// goroutine. An unrecovered panic there kills the process and the learner loses
// the whole checklist, plus a terminal that Mux left in raw mode.
func TestRunContainsAPanickingCheck(t *testing.T) {
	factories := map[string]Factory{
		"panicking": func(s Spec) (Check, error) { return &panickingCheck{id: s.ID}, nil },
		"passing":   func(s Spec) (Check, error) { return &stubCheck{id: s.ID, status: StatusPass}, nil },
	}
	e := testEngine(t, factories)

	res := runEngine(t, e, []Spec{
		leafSpec("obj1", "panicking", "authored"),
		leafSpec("obj2", "passing", "unused"),
	})

	if len(res.Objectives) != 2 {
		t.Fatalf("got %d objectives, want 2: a panicking check must not stop the run", len(res.Objectives))
	}
	if res.Objectives[0].Status != StatusError {
		t.Errorf("objectives[0].Status = %q, want %q for a panicking check", res.Objectives[0].Status, StatusError)
	}
	if res.Objectives[1].Status != StatusPass {
		t.Error("the check after the panicking one did not run")
	}
}

// --- Run: duration ---

func TestRunReportsADurationInMilliseconds(t *testing.T) {
	factories := map[string]Factory{
		"slow": func(s Spec) (Check, error) {
			return &stubCheck{id: s.ID, status: StatusPass, delay: 15 * time.Millisecond}, nil
		},
	}
	e := testEngine(t, factories)

	res := runEngine(t, e, []Spec{leafSpec("obj1", "slow", "a")})
	if res.DurationMS < 10 {
		t.Errorf("DurationMS = %d, want at least 10 for a check that slept 15ms", res.DurationMS)
	}
}

// --- Composites through the engine ---

// TestBuildWiresCompositesFromSpecs closes the loop between Spec and compose.go:
// the composition tests drive nodes directly, and this is what proves Build
// produces them.
func TestBuildWiresCompositesFromSpecs(t *testing.T) {
	factories := stubFactories(map[string]Status{
		"passing": StatusPass,
		"failing": StatusFail,
	})
	e := testEngine(t, factories)

	res := runEngine(t, e, []Spec{
		{
			ID: "obj1", Text: "either form is accepted", OnFail: "OUTER: neither form matched",
			AnyOf: []Spec{
				leafSpec("", "failing", "INNER: not the numeral"),
				leafSpec("", "passing", "INNER: not the word"),
			},
		},
	})

	if len(res.Objectives) != 1 {
		t.Fatalf("got %d objectives, want 1: a composite is one objective", len(res.Objectives))
	}
	if res.Objectives[0].Status != StatusPass {
		t.Errorf("status = %q, want %q: any_of with one passing branch passes", res.Objectives[0].Status, StatusPass)
	}
	if res.Objectives[0].Text != "either form is accepted" {
		t.Errorf("Text = %q, want the composite's authored objective line", res.Objectives[0].Text)
	}
}

func TestAFailingCompositeReportsItsOwnMessageThroughTheEngine(t *testing.T) {
	factories := stubFactories(map[string]Status{"failing": StatusFail})
	e := testEngine(t, factories)

	res := runEngine(t, e, []Spec{
		{
			ID: "obj1", Text: "either form", OnFail: "OUTER: the count does not look right",
			AnyOf: []Spec{
				leafSpec("", "failing", "INNER: not the numeral"),
				leafSpec("", "failing", "INNER: not the word"),
			},
		},
	})

	got := res.Objectives[0].Message
	if got != "OUTER: the count does not look right" {
		t.Errorf("message = %q, want the composite's own on_fail", got)
	}
	if strings.Contains(got, "INNER") {
		t.Errorf("message leaked a branch's on_fail: %q", got)
	}
	if res.PrimaryFailure == nil || res.PrimaryFailure.ID != "obj1" {
		t.Errorf("PrimaryFailure = %+v, want the composite objective", res.PrimaryFailure)
	}
}

// --- Defaults ---

func TestNewEngineDefaultsMatchTheDocumentedBudgets(t *testing.T) {
	e := NewEngine()
	if e.checkTimeout != DefaultCheckTimeout {
		t.Errorf("checkTimeout = %s, want %s (docs/LEVEL-FORMAT.md section 4.6)", e.checkTimeout, DefaultCheckTimeout)
	}
	if e.levelTimeout != DefaultLevelTimeout {
		t.Errorf("levelTimeout = %s, want %s (docs/LEVEL-FORMAT.md section 4.6)", e.levelTimeout, DefaultLevelTimeout)
	}
	if DefaultCheckTimeout != 10*time.Second {
		t.Errorf("DefaultCheckTimeout = %s, want 10s per docs/LEVEL-FORMAT.md section 4.6", DefaultCheckTimeout)
	}
	if DefaultLevelTimeout != 60*time.Second {
		t.Errorf("DefaultLevelTimeout = %s, want 60s per docs/LEVEL-FORMAT.md section 4.6", DefaultLevelTimeout)
	}
}

// TestOptionsRejectNonPositiveBudgets keeps a zero or negative option from
// producing a context that is already expired, which would make every check
// time out with no explanation an author could act on.
func TestOptionsRejectNonPositiveBudgets(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Second} {
		e := NewEngine(WithCheckTimeout(d), WithLevelTimeout(d))
		if e.checkTimeout != DefaultCheckTimeout {
			t.Errorf("WithCheckTimeout(%s) took effect; a non-positive budget must be ignored", d)
		}
		if e.levelTimeout != DefaultLevelTimeout {
			t.Errorf("WithLevelTimeout(%s) took effect; a non-positive budget must be ignored", d)
		}
	}
}

// TestBuildIsSafeToRunManyTimes is the other half of build-once, run-many: a
// learner types `check` repeatedly against the same built checks, so a second
// Run must not see state the first one left behind.
func TestBuildIsSafeToRunManyTimes(t *testing.T) {
	factories := stubFactories(map[string]Status{"passing": StatusPass, "failing": StatusFail})
	e := testEngine(t, factories)

	checks, err := e.Build([]Spec{
		leafSpec("obj1", "passing", "a"),
		leafSpec("obj2", "failing", "b"),
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	first := e.Run(context.Background(), checks, Env{LevelID: "x"})
	second := e.Run(context.Background(), checks, Env{LevelID: "x"})

	if first.Passed != second.Passed {
		t.Errorf("Passed changed between runs: %v then %v", first.Passed, second.Passed)
	}
	if len(first.Objectives) != len(second.Objectives) {
		t.Fatalf("objective count changed between runs: %d then %d", len(first.Objectives), len(second.Objectives))
	}
	for i := range first.Objectives {
		if first.Objectives[i].Status != second.Objectives[i].Status {
			t.Errorf("objectives[%d] status changed between runs: %q then %q",
				i, first.Objectives[i].Status, second.Objectives[i].Status)
		}
	}
	if len(first.Notes) != len(second.Notes) {
		t.Errorf("note count changed between runs: %d then %d; notes are accumulating across calls", len(first.Notes), len(second.Notes))
	}
}
