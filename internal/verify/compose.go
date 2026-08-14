package verify

import (
	"context"
	"time"
)

// The three composition nodes from docs/LEVEL-FORMAT.md section 3.
//
// A composite is a Check like any other, so it nests without any special case:
// its branches are Checks, and a branch that happens to be a composite is
// indistinguishable from a leaf to its parent. Engine.Build is what turns a
// Spec tree into a Check tree.
//
// Two rules in this file are easy to get wrong and are each pinned by a test.
//
// A composite reports its OWN authored on_fail, at every depth, and discards
// whatever its children said. A composite is one objective; a branch is part of
// how that objective is decided. A composite that fell back to a failing
// child's message would explain a branch to a learner who was told about an
// objective.
//
// A composite short-circuits internally, and that does not violate the
// no-short-circuit invariant in doc.go. That invariant is about the objective
// checklist: every objective is evaluated so the learner sees a complete list.
// A branch is not an objective. It has no id, it never appears in
// LevelResult.Objectives, and nothing is reported about it either way, so
// evaluating branches after the answer is decided costs sandbox round trips and
// buys nothing. TestAnyOfStopsAtFirstPassingBranch exists so this is not
// mistaken for the bug it resembles.

// compositeKind selects how a composite folds its branches.
type compositeKind string

const (
	compositeAnyOf compositeKind = "any_of"
	compositeAllOf compositeKind = "all_of"
	compositeNot   compositeKind = "not"
)

// maxComposeDepth bounds how deeply a level may nest composition nodes.
//
// The bound exists because a Spec tree comes from pack YAML, which is data this
// process did not write. Build recurses over it, so an absurdly deep tree is a
// stack overflow driven by content rather than by code. Eight is far past any
// legible level: the deepest thing docs/LEVEL-FORMAT.md illustrates is one
// level, and a level author who needs nine has a `type: script` escape hatch
// that reads better anyway. Refused rather than truncated, per the house rule
// that pack data is validated and not adjusted.
const maxComposeDepth = 8

// composite evaluates its branches and folds their statuses into one Result.
type composite struct {
	id       string
	kind     compositeKind
	onFail   string
	branches []Check
}

// newComposite builds a composition node. Callers come from Build, and from
// tests that drive a node directly.
func newComposite(id string, kind compositeKind, onFail string, branches []Check) Check {
	return &composite{id: id, kind: kind, onFail: onFail, branches: branches}
}

// ID returns the composite's own id, which is the objective's id. A branch
// carries no id, so a nested composite's is empty.
func (c *composite) ID() string { return c.id }

// Run evaluates the branches and folds the answer.
func (c *composite) Run(ctx context.Context, env Env) Result {
	start := time.Now()

	// An empty composite asserts nothing. Build refuses to construct one, so
	// reaching here means a caller built it directly; the honest answer is
	// that nothing was determined. Answering pass, which is what a naive fold
	// over zero branches produces for all_of, would silently turn an authoring
	// mistake into a level that cannot be failed.
	if len(c.branches) == 0 {
		return Result{
			ID:       c.id,
			Status:   StatusError,
			Message:  "this check declares no branches, so there was nothing to verify",
			Duration: time.Since(start),
		}
	}

	status := c.fold(ctx, env)

	return Result{
		ID:       c.id,
		Status:   status,
		Message:  c.messageFor(status, ctx.Err()),
		Duration: time.Since(start),
	}
}

// fold runs the branches and reduces their statuses to one.
func (c *composite) fold(ctx context.Context, env Env) Status {
	switch c.kind {
	case compositeNot:
		return c.foldNot(ctx, env)
	case compositeAnyOf:
		return c.foldAnyOf(ctx, env)
	default:
		return c.foldAllOf(ctx, env)
	}
}

// foldAnyOf passes as soon as one branch passes.
//
// When none passes, the answer is the most inconclusive thing seen rather than
// a flat fail: if one branch timed out, this composite genuinely does not know
// whether the learner solved it, and saying "fail" would put an authored on_fail
// in front of somebody whose sandbox stopped answering.
func (c *composite) foldAnyOf(ctx context.Context, env Env) Status {
	sawTimeout, sawError := false, false

	for _, branch := range c.branches {
		if err := ctx.Err(); err != nil {
			return StatusError
		}
		switch branch.Run(ctx, env).Status {
		case StatusPass:
			return StatusPass
		case StatusTimeout:
			sawTimeout = true
		case StatusError:
			sawError = true
		}
	}

	switch {
	case sawTimeout:
		return StatusTimeout
	case sawError:
		return StatusError
	default:
		return StatusFail
	}
}

// foldAllOf reports the first branch that did not pass, preserving that
// branch's status so a timeout stays a timeout.
func (c *composite) foldAllOf(ctx context.Context, env Env) Status {
	for _, branch := range c.branches {
		if err := ctx.Err(); err != nil {
			return StatusError
		}
		if status := branch.Run(ctx, env).Status; status != StatusPass {
			return status
		}
	}
	return StatusPass
}

// foldNot inverts its single branch.
//
// Only pass and fail invert. A timeout or an error propagates unchanged,
// because the negation of "we could not tell" is still "we could not tell".
// Inverting an inconclusive result would mean a sandbox that stopped answering
// satisfied every `not` in the pack, which is a level accepting a wrong answer
// at exactly the moment something is already broken.
func (c *composite) foldNot(ctx context.Context, env Env) Status {
	if err := ctx.Err(); err != nil {
		return StatusError
	}

	// Build guarantees exactly one branch. Extra branches would be silently
	// ignored here, which is why Build refuses them rather than trusting this.
	switch status := c.branches[0].Run(ctx, env).Status; status {
	case StatusPass:
		return StatusFail
	case StatusFail:
		return StatusPass
	default:
		return status
	}
}

// messageFor returns what the learner reads for this outcome.
//
// A pass says nothing: on_fail describes a failure. A fail says the authored
// text. An inconclusive outcome says what happened instead, and deliberately
// does NOT include the authored on_fail: we never established whether the
// learner was right, so telling them what they got wrong would be a guess.
func (c *composite) messageFor(status Status, ctxErr error) string {
	switch status {
	case StatusPass:
		return ""
	case StatusFail:
		return c.onFail
	case StatusTimeout:
		return "one of the checks behind this objective ran out of time, so this could not be decided"
	default:
		if ctxErr != nil {
			return "this objective was not finished: " + ctxErr.Error()
		}
		return "one of the checks behind this objective could not determine an answer"
	}
}
