package verify

import (
	"context"
	"strings"
	"testing"
	"time"
)

// stubCheck is a Check that answers from a fixed status without touching a
// sandbox, and records that it ran.
//
// It lives in this file rather than in engine_test.go because composition is
// the first thing that needs it, and Go does not care which file in a package a
// type is declared in. Both test files use it.
type stubCheck struct {
	id     string
	status Status
	msg    string

	// ran is set when Run is called, which is how the short-circuit tests
	// assert a branch was never reached.
	ran bool

	// delay makes Run take time, for the engine's budget tests.
	delay time.Duration

	// before runs at the top of Run, so a test can cancel a context or
	// otherwise change the world from inside a check.
	before func()
}

func (s *stubCheck) ID() string { return s.id }

func (s *stubCheck) Run(ctx context.Context, _ Env) Result {
	s.ran = true
	if s.before != nil {
		s.before()
	}
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return Result{ID: s.id, Status: StatusError, Message: ctx.Err().Error()}
		}
	}
	return Result{ID: s.id, Status: s.status, Message: s.msg}
}

// stub is shorthand for a stubCheck with an id derived from its status, for the
// many table rows that do not care about the id.
func stub(status Status) *stubCheck {
	return &stubCheck{id: "stub-" + string(status), status: status, msg: "stub message for " + string(status)}
}

// runComposite evaluates c with an empty Env, which every composite in these
// tests can tolerate because their children are stubs.
func runComposite(c Check) Result {
	return c.Run(context.Background(), Env{})
}

// TestCompositesFoldChildStatuses is the truth table for the three nodes.
//
// The interesting rows are not the pass and fail ones, they are timeout and
// error: an inconclusive child must stay inconclusive rather than collapsing
// into one of the two decided answers.
func TestCompositesFoldChildStatuses(t *testing.T) {
	tests := []struct {
		name     string
		kind     compositeKind
		children []Status
		want     Status
	}{
		// any_of
		{name: "any_of: one pass is enough", kind: compositeAnyOf, children: []Status{StatusFail, StatusPass}, want: StatusPass},
		{name: "any_of: all fail is a fail", kind: compositeAnyOf, children: []Status{StatusFail, StatusFail}, want: StatusFail},
		{name: "any_of: a timeout outranks a fail", kind: compositeAnyOf, children: []Status{StatusFail, StatusTimeout}, want: StatusTimeout},
		{name: "any_of: an error outranks a fail", kind: compositeAnyOf, children: []Status{StatusFail, StatusError}, want: StatusError},
		{name: "any_of: a timeout outranks an error", kind: compositeAnyOf, children: []Status{StatusError, StatusTimeout}, want: StatusTimeout},
		{name: "any_of: a pass outranks a timeout", kind: compositeAnyOf, children: []Status{StatusTimeout, StatusPass}, want: StatusPass},

		// all_of
		{name: "all_of: every pass is a pass", kind: compositeAllOf, children: []Status{StatusPass, StatusPass}, want: StatusPass},
		{name: "all_of: the first non-pass wins", kind: compositeAllOf, children: []Status{StatusPass, StatusFail}, want: StatusFail},
		{name: "all_of: a timeout is reported as a timeout", kind: compositeAllOf, children: []Status{StatusPass, StatusTimeout}, want: StatusTimeout},
		{name: "all_of: an error is reported as an error", kind: compositeAllOf, children: []Status{StatusPass, StatusError}, want: StatusError},
		{name: "all_of: the first non-pass wins even before a worse one", kind: compositeAllOf, children: []Status{StatusFail, StatusError}, want: StatusFail},

		// not
		{name: "not: a passing child fails", kind: compositeNot, children: []Status{StatusPass}, want: StatusFail},
		{name: "not: a failing child passes", kind: compositeNot, children: []Status{StatusFail}, want: StatusPass},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			children := make([]Check, 0, len(tt.children))
			for _, status := range tt.children {
				children = append(children, stub(status))
			}
			c := newComposite("obj", tt.kind, "the composite's own message", children)

			if got := runComposite(c).Status; got != tt.want {
				t.Errorf("status = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestNotDoesNotLaunderTimeoutOrError is the subtlest rule in this file and the
// reason it gets a test of its own rather than two more rows above.
//
// `not` inverts pass and fail. It must NOT invert timeout or error, because the
// negation of "we could not tell" is still "we could not tell". If it did,
// a sandbox that stopped answering would satisfy every `not` in the pack, and a
// level would start accepting wrong answers precisely when something was
// already broken.
func TestNotDoesNotLaunderTimeoutOrError(t *testing.T) {
	for _, inconclusive := range []Status{StatusTimeout, StatusError} {
		t.Run(string(inconclusive), func(t *testing.T) {
			child := stub(inconclusive)
			c := newComposite("obj", compositeNot, "the composite's own message", []Check{child})

			got := runComposite(c)
			if got.Status == StatusPass {
				t.Fatalf("not over a %s child reported pass; an inconclusive child must never become a pass", inconclusive)
			}
			if got.Status != inconclusive {
				t.Errorf("status = %q, want %q propagated unchanged", got.Status, inconclusive)
			}
		})
	}
}

// TestCompositesNestAndReportTheirOwnOnFail is acceptance criterion 1.
//
// It builds not{any_of{all_of{...}}} with a distinct on_fail at every depth and
// asserts the outermost message is what comes back, and that no inner message
// appears anywhere in it. A composite that fell back to whichever child
// happened to fail would explain the wrong thing to the learner: the inner
// message describes a branch, and the objective is the whole composite.
func TestCompositesNestAndReportTheirOwnOnFail(t *testing.T) {
	const (
		innerMsg  = "INNER: the all_of branch message"
		middleMsg = "MIDDLE: the any_of branch message"
		outerMsg  = "OUTER: the objective message the learner should read"
	)

	// all_of over two passing leaves passes, so any_of passes, so not fails.
	// A failing `not` is what surfaces the outermost on_fail.
	inner := newComposite("", compositeAllOf, innerMsg, []Check{stub(StatusPass), stub(StatusPass)})
	middle := newComposite("", compositeAnyOf, middleMsg, []Check{inner})
	outer := newComposite("obj1", compositeNot, outerMsg, []Check{middle})

	got := runComposite(outer)

	if got.Status != StatusFail {
		t.Fatalf("status = %q, want %q: not over a passing any_of must fail", got.Status, StatusFail)
	}
	if got.Message != outerMsg {
		t.Errorf("message = %q, want the outermost on_fail %q", got.Message, outerMsg)
	}
	for _, inner := range []string{innerMsg, middleMsg} {
		if strings.Contains(got.Message, inner) {
			t.Errorf("message leaked a branch's on_fail %q; a composite reports its own message and discards its children's", inner)
		}
	}
	if got.ID != "obj1" {
		t.Errorf("ID = %q, want the outermost id %q", got.ID, "obj1")
	}
}

// TestCompositeReportsItsOwnOnFailAtEveryDepth is the same rule checked one
// level down, so that a fix which only special-cased the root would fail.
func TestCompositeReportsItsOwnOnFailAtEveryDepth(t *testing.T) {
	const innerMsg = "INNER: this all_of is what failed"
	const outerMsg = "OUTER: the objective message"

	inner := newComposite("", compositeAllOf, innerMsg, []Check{stub(StatusFail)})
	outer := newComposite("obj1", compositeAllOf, outerMsg, []Check{inner})

	if got := runComposite(inner); got.Message != innerMsg {
		t.Errorf("the inner composite reported %q, want its own %q", got.Message, innerMsg)
	}
	if got := runComposite(outer); got.Message != outerMsg {
		t.Errorf("the outer composite reported %q, want its own %q", got.Message, outerMsg)
	}
}

// TestCompositePassReportsNoMessage keeps a composite consistent with a leaf: a
// pass carries no message, because on_fail describes a failure.
func TestCompositePassReportsNoMessage(t *testing.T) {
	c := newComposite("obj1", compositeAllOf, "should not be shown", []Check{stub(StatusPass)})
	if got := runComposite(c); got.Message != "" {
		t.Errorf("a passing composite reported message %q, want none", got.Message)
	}
}

// TestAnyOfStopsAtFirstPassingBranch pins a deliberate internal short-circuit.
//
// doc.go states that checks never short-circuit, and that invariant is about
// the objective checklist: every objective runs so the learner sees a complete
// list. A composition branch is not an objective. It has no id, it never
// appears in LevelResult.Objectives, and nothing is reported about it either
// way, so evaluating the remaining branches would cost sandbox round trips and
// buy the learner nothing. A four-branch any_of under a 10 second budget is the
// case that makes this matter.
//
// This test exists so the optimisation is not mistaken for the bug it
// resembles, and reverted by somebody enforcing the invariant it does not
// violate.
func TestAnyOfStopsAtFirstPassingBranch(t *testing.T) {
	first := stub(StatusPass)
	second := stub(StatusFail)
	third := stub(StatusFail)

	c := newComposite("obj1", compositeAnyOf, "message", []Check{first, second, third})
	if got := runComposite(c).Status; got != StatusPass {
		t.Fatalf("status = %q, want %q", got, StatusPass)
	}

	if !first.ran {
		t.Error("the first branch did not run")
	}
	if second.ran || third.ran {
		t.Error("a branch after the first passing one ran; any_of is decided and the rest are wasted sandbox calls")
	}
}

// TestAllOfStopsAtFirstFailingBranch is the same optimisation on the other node.
func TestAllOfStopsAtFirstFailingBranch(t *testing.T) {
	first := stub(StatusFail)
	second := stub(StatusPass)

	c := newComposite("obj1", compositeAllOf, "message", []Check{first, second})
	if got := runComposite(c).Status; got != StatusFail {
		t.Fatalf("status = %q, want %q", got, StatusFail)
	}
	if !first.ran {
		t.Error("the first branch did not run")
	}
	if second.ran {
		t.Error("a branch after the first failing one ran; all_of is decided and the rest are wasted sandbox calls")
	}
}

// TestCompositeStopsOnContextCancellation asserts a composite does not keep
// making sandbox calls after the learner has pressed Ctrl-C. Without this, a
// cancelled ten-branch all_of would still run all ten.
func TestCompositeStopsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	first := &stubCheck{id: "first", status: StatusPass, before: cancel}
	second := stub(StatusPass)

	c := newComposite("obj1", compositeAllOf, "message", []Check{first, second})
	got := c.Run(ctx, Env{})

	if second.ran {
		t.Error("a branch ran after the context was cancelled")
	}
	if got.Status != StatusError {
		t.Errorf("status = %q, want %q: a cancelled composite is inconclusive, not failed", got.Status, StatusError)
	}
	if got.Message == "" {
		t.Error("a cancelled composite reported no message")
	}
}

// TestCompositeDurationCoversItsBranches checks the composite stamps a duration
// covering the work it did, rather than leaving the field at zero for the
// objective the learner actually waited on.
func TestCompositeDurationCoversItsBranches(t *testing.T) {
	const delay = 20 * time.Millisecond
	child := &stubCheck{id: "slow", status: StatusPass, delay: delay}

	c := newComposite("obj1", compositeAllOf, "message", []Check{child})
	if got := runComposite(c).Duration; got < delay {
		t.Errorf("Duration = %s, want at least the %s its branch took", got, delay)
	}
}

// TestCompositeWithNoBranchesIsAnError guards the shape Build is supposed to
// reject. If a caller builds one anyway, by constructing a composite directly,
// the honest answer is that nothing was asserted rather than a vacuous pass.
// A vacuous pass on an empty all_of is how a level stops testing anything.
func TestCompositeWithNoBranchesIsAnError(t *testing.T) {
	for _, kind := range []compositeKind{compositeAnyOf, compositeAllOf, compositeNot} {
		t.Run(string(kind), func(t *testing.T) {
			c := newComposite("obj1", kind, "message", nil)
			if got := runComposite(c).Status; got != StatusError {
				t.Errorf("status = %q for a %s with no branches, want %q: an empty composite asserts nothing and must not pass",
					got, kind, StatusError)
			}
		})
	}
}
