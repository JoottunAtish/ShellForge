package verify

import (
	"context"
	"fmt"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/scope"
)

// Status is the outcome of running one Check.
type Status string

const (
	// StatusPass means the check confirmed the asserted state.
	StatusPass Status = "pass"

	// StatusFail means the check ran to completion and the asserted state
	// does not hold. Message carries the authored on_fail text.
	StatusFail Status = "fail"

	// StatusWarn is a non-blocking failure. Assigning it from a Check's
	// severity is the verification engine's job, not this package's: see
	// doc.go's third invariant. No Check in this package returns it
	// directly today.
	StatusWarn Status = "warn"

	// StatusTimeout means the check did not finish inside its budget.
	StatusTimeout Status = "timeout"

	// StatusError means the check could not determine an answer at all:
	// Session.Exec itself failed, or the sandbox state the check needs has
	// not been produced yet. This is distinct from StatusFail, which means
	// the check ran and the learner has not solved the objective. Reporting
	// StatusError as StatusFail reads to a learner as "you got it wrong"
	// when the truth is "we could not tell yet", which is a worse failure
	// than the one it is trying to report.
	StatusError Status = "error"
)

// Result is what one Check.Run produced.
type Result struct {
	// ID is the check's own id, copied from Spec.ID.
	ID string

	// Status is the outcome.
	Status Status

	// Message is the authored on_fail text for a failure, or a check's own
	// explanation for StatusError and StatusTimeout. For StatusFail on the
	// script check, a failing script's stdout is appended to the authored
	// on_fail.
	Message string

	// Duration is how long Run took.
	Duration time.Duration
}

// Env is everything a Check may read. A check that needs something not on
// Env is a check reaching somewhere it should not.
type Env struct {
	// Session is the sandbox connection a check probes real state through.
	Session runtime.Session

	// Root is the level's setup.root.
	Root string

	// LevelID is the id of the level being checked, which the engine copies
	// into LevelResult.LevelID. It is on Env rather than a parameter of
	// Engine.Run so that Run keeps the signature the ticket fixed, and it is
	// a plain string so that this package still never learns where a level
	// comes from. No Check reads it, and a Check that wanted to would be
	// deciding pass or fail from the level's name rather than from state.
	LevelID string

	// Snapshots reads the shell-local state files instrument.bash writes.
	Snapshots Snapshots

	// Journal answers questions about command history. It is declared in
	// this package rather than imported from internal/journal; see doc.go.
	Journal JournalReader
}

// Check is one verifiable assertion about sandbox state.
type Check interface {
	// ID returns the check's own id.
	ID() string

	// Run evaluates the check against env and returns a Result. Run must
	// never mutate sandbox state.
	Run(ctx context.Context, env Env) Result
}

// Spec is the declarative, unvalidated form of a check, as loaded from level
// YAML. A Factory turns a Spec into a Check, validating Params along the
// way.
//
// A Spec is either a leaf, naming a registered Type, or a composition node,
// setting exactly one of AnyOf, AllOf and Not. Engine.Build refuses a Spec
// that is both or neither.
//
// The three composition fields deliberately mirror internal/content's
// CheckSpec, field for field and method for method, even though neither
// package may import the other: they are peers at layer 3. Whatever converts
// one into the other lives above both, and mirroring the shape is what keeps
// that conversion a mechanical recursion rather than a translation with
// judgement in it.
type Spec struct {
	// ID is the check's own id, unique within its level.
	ID string

	// Text is the objective checklist line the learner reads, copied from the
	// level's objectives list by whatever built this Spec. It is carried here
	// rather than looked up later because the engine assembles the whole
	// result, and an assembler that had ids but not text would push the join
	// out to the renderer, where an id that matches no objective becomes a
	// blank line instead of a build error.
	//
	// Empty is legal: a severity: warn check produces a note rather than a
	// checklist line and needs no text.
	Text string

	// Type names a registered check type, such as "file_exists". It is empty
	// on a composition node.
	Type string

	// OnFail is the message shown when the check fails. Required by the
	// level format; this package does not enforce that a caller supplied
	// one, since that belongs to level validation.
	OnFail string

	// Severity is "fail" or "warn". Interpreting it into a Result's
	// severity for the objective checklist is the verification engine's
	// job; a Check itself only ever reports pass, fail, error, or timeout.
	Severity string

	// Optional marks the check as non-blocking for level completion.
	// Interpreting it is the verification engine's job.
	Optional bool

	// TimeoutSeconds bounds this check's own execution. Zero means the
	// package default. Enforcing it is the verification engine's job: a
	// Check's own Run honours ctx cancellation but does not start its own
	// timer.
	TimeoutSeconds int

	// Params carries the type-specific fields, as decoded from YAML: a
	// map of plain Go values (string, bool, float64, []any, and nested
	// maps), never a typed struct. A Factory is what gives them meaning.
	Params map[string]any

	// AnyOf passes when at least one branch passes.
	AnyOf []Spec

	// AllOf passes when every branch passes.
	AllOf []Spec

	// Not passes when its single branch fails.
	Not *Spec
}

// IsComposite reports whether the Spec is a composition node rather than a
// leaf naming a registered type.
func (s Spec) IsComposite() bool {
	return len(s.AnyOf) > 0 || len(s.AllOf) > 0 || s.Not != nil
}

// Branches returns the child Specs of a composition node, in declaration
// order, or nil for a leaf. It lets a caller walk a Spec tree without
// repeating the three-way composition test.
func (s Spec) Branches() []*Spec {
	var out []*Spec
	for i := range s.AnyOf {
		out = append(out, &s.AnyOf[i])
	}
	for i := range s.AllOf {
		out = append(out, &s.AllOf[i])
	}
	if s.Not != nil {
		out = append(out, s.Not)
	}
	return out
}

// Factory builds a Check from a Spec. It returns an error on a malformed or
// unknown parameter, so that pack loading catches the mistake before any
// level is played, rather than a learner discovering it at Run time.
type Factory func(s Spec) (Check, error)

// JournalReader is everything a Check may ask the command journal. It is
// declared here, not imported from internal/journal, so that this package
// keeps zero dependency on internal/journal at the source level: see doc.go
// for why that is a deliberate contract, not an oversight. internal/journal
// satisfies this interface from outside the package.
type JournalReader interface {
	// Commands returns the commands in scope, in the order the learner ran
	// them.
	Commands(s Scope) []string
}

// Scope and ScopeKind are ALIASES for the types in internal/scope, not new
// named types. That distinction is the whole point: an alias makes
// verify.Scope and scope.Scope one type, so *journal.Journal satisfies
// JournalReader above with no adapter translating between two lookalike
// structs. A named type (type Scope scope.Scope) would compile here and
// silently put the adapter back. See internal/scope/doc.go.
type (
	// Scope selects which commands a journal check reads. N is meaningful
	// only when Kind is ScopeLastN.
	Scope = scope.Scope

	// ScopeKind selects which slice of journal history a journal check
	// reads.
	ScopeKind = scope.ScopeKind
)

const (
	// ScopeLevel is every command run since the level started.
	ScopeLevel = scope.Level

	// ScopeLastN is the most recent N commands.
	ScopeLastN = scope.LastN

	// ScopeLast is only the most recent command.
	ScopeLast = scope.Last
)

// factoryError wraps err with the check type and id, so a pack loading
// failure names which check and which type could not be built.
func factoryError(typeName, id string, err error) error {
	return fmt.Errorf("verify: %s check %q: %w", typeName, id, err)
}

// paramString reads a string parameter from params. If required is true, a
// missing or empty value is an error. A present value of the wrong Go type
// is always an error.
func paramString(params map[string]any, key string, required bool) (string, error) {
	raw, ok := params[key]
	if !ok {
		if required {
			return "", fmt.Errorf("missing required param %q", key)
		}
		return "", nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("param %q must be a string, got %T", key, raw)
	}
	if required && s == "" {
		return "", fmt.Errorf("param %q must not be empty", key)
	}
	return s, nil
}

// paramStringPresence is like paramString but also reports whether the key
// was present at all, distinguishing "absent" from "present and empty". A
// check that treats those two cases differently, such as env_var's optional
// value, needs this rather than paramString.
func paramStringPresence(params map[string]any, key string) (value string, present bool, err error) {
	raw, ok := params[key]
	if !ok {
		return "", false, nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", true, fmt.Errorf("param %q must be a string, got %T", key, raw)
	}
	return s, true, nil
}

// paramBool reads an optional boolean parameter, defaulting to false.
func paramBool(params map[string]any, key string) (bool, error) {
	raw, ok := params[key]
	if !ok {
		return false, nil
	}
	b, ok := raw.(bool)
	if !ok {
		return false, fmt.Errorf("param %q must be a boolean, got %T", key, raw)
	}
	return b, nil
}

// paramStringSlice reads an optional list-of-strings parameter. It accepts
// []any (the shape goccy/go-yaml produces for a YAML sequence of scalars)
// and []string (the shape a test fixture writes directly), so a Factory
// need not care which one it was handed.
func paramStringSlice(params map[string]any, key string) ([]string, error) {
	raw, ok := params[key]
	if !ok {
		return nil, nil
	}
	if s, ok := raw.([]string); ok {
		return s, nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("param %q must be a list of strings, got %T", key, raw)
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("param %q must be a list of strings, found element of type %T", key, v)
		}
		out = append(out, s)
	}
	return out, nil
}
