package game

import "fmt"

// State is one point in the Orchestrator's lifecycle for a single level
// attempt.
//
// Ten states are declared here because they are the full vocabulary the
// ticket's design settles on, not because every one of them is reached by
// production code this ticket. StateProvisioning and StateBriefing are never
// assigned to an Orchestrator's state this ticket: provisioning a sandbox and
// rendering a briefing both belong to a caller above this package. StateHinting
// is reachable only through legalTransition, exercised directly by this
// package's own tests, because no public method enters or exits it yet. See
// orchestrator.go's package doc comment for the full transition table.
type State string

// The ten declared states. Ordering here is the ordering of a typical run,
// not a ranking.
const (
	StateIdle         State = "idle"
	StateProvisioning State = "provisioning"
	StateSetup        State = "setup"
	StateBriefing     State = "briefing"
	StateActive       State = "active"
	StateChecking     State = "checking"
	StateHinting      State = "hinting"
	StatePassed       State = "passed"
	StateFailed       State = "failed"
	StateTeardown     State = "teardown"
)

// String returns the state's wire form, which is also its constant's value.
func (s State) String() string { return string(s) }

// legalTransition reports whether moving from to directly is ever legal,
// for any caller, at any layer. It is table-driven and unexported: the table
// is the single place the shape of the state machine is written down, and
// orchestrator.go consults it for every transition it performs rather than
// re-deriving the rule inline.
//
// Close is universal: it may run from any state (it is the only defined way
// out of a level in play), so every state may move to StateTeardown, and
// StateTeardown always resolves back to StateIdle. Those two rules are
// checked once, ahead of the per-state table below, rather than repeated
// into every case.
//
// The table is otherwise deliberately wider than what any exported
// Orchestrator method exercises this ticket. Active <-> Hinting is legal
// here even though no public method enters or exits StateHinting yet: this
// ticket's tests reach those two edges directly, in the same package, to pin
// the contract before any caller depends on it.
func legalTransition(from, to State) bool {
	if to == StateTeardown {
		return from != StateTeardown
	}
	if from == StateTeardown {
		return to == StateIdle
	}

	switch from {
	case StateIdle:
		return to == StateSetup
	case StateSetup:
		return to == StateActive || to == StateFailed
	case StateProvisioning:
		return to == StateSetup || to == StateFailed
	case StateBriefing:
		return to == StateActive
	case StateActive:
		return to == StateChecking || to == StateHinting
	case StateChecking:
		return to == StateActive || to == StatePassed
	case StateHinting:
		return to == StateActive
	case StatePassed:
		return to == StateActive
	case StateFailed:
		return false
	default:
		return false
	}
}

// TransitionError reports that Op was refused because the Orchestrator was
// in State, which does not permit it. It is returned, never panicked, and is
// meant to be matched with errors.As so a caller can distinguish "this call
// is out of order" from every other failure.
type TransitionError struct {
	Op    string
	State State
}

// Error implements the error interface.
func (e *TransitionError) Error() string {
	return fmt.Sprintf("game: cannot %s while %s", e.Op, e.State)
}
