package content

import (
	"fmt"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
)

// CheckSpec is one entry in a level's checks list, or one branch of a
// composition node.
//
// It is deliberately loose about the per-type fields. internal/verify owns
// what path, match, value, pattern and the rest mean for each of the
// registered types; this package knows only the fields common to every check,
// plus the three composition nodes. Everything else lands in Params verbatim
// and is handed to the check registry to give meaning to.
//
// That split is what lets a check type gain a parameter without this package
// changing at all, and it is why Validate takes a TypeChecker rather than
// reaching into internal/verify: content asks "is this type known, and are
// these parameters well formed", and verify answers.
type CheckSpec struct {
	// ID is the check's own id, unique within its level. A composition
	// node's branches carry no id: only the outermost check corresponds to
	// an objective.
	ID string

	// Type names a registered check type, such as "file_exists". It is
	// empty on a composition node, where AnyOf, AllOf or Not carries the
	// meaning instead.
	Type string

	// OnFail is the message the learner reads when this check fails.
	// Required at every composition depth, because a composite that falls
	// back to a child's message explains the wrong thing.
	OnFail string

	// Severity is "fail" or "warn". A warn failure shows a note and does not
	// block passing.
	Severity string

	// Optional marks a bonus check. It never blocks passing.
	Optional bool

	// TimeoutSeconds bounds this check. Zero means the engine default.
	TimeoutSeconds int

	// AnyOf passes when at least one branch passes.
	AnyOf []CheckSpec

	// AllOf passes when every branch passes.
	AllOf []CheckSpec

	// Not passes when its single branch fails.
	Not *CheckSpec

	// Params carries every key not named above, exactly as it appeared in
	// the YAML. internal/verify's factory for Type is what validates it.
	Params map[string]any

	// Line is the line in SourceFile this check starts on, so a validation
	// problem can name a place rather than an index. It is populated by the
	// decoder and is not a YAML field.
	Line int
}

// Severity values a check may declare.
const (
	SeverityFail = "fail"
	SeverityWarn = "warn"
)

// checkReservedKeys are the keys CheckSpec decodes into its own fields. Every
// other key in a check's mapping becomes a Params entry.
//
// It is the declaration decodeField's switch is tested against, so that a key
// added to one and not the other is a test failure rather than a field that
// silently lands in Params and is then rejected by the check registry as an
// unknown parameter.
var checkReservedKeys = map[string]bool{
	"id":              true,
	"type":            true,
	"on_fail":         true,
	"severity":        true,
	"optional":        true,
	"timeout_seconds": true,
	"any_of":          true,
	"all_of":          true,
	"not":             true,
}

// UnmarshalYAML decodes one check, splitting its mapping into the common
// fields above and the type-specific Params below.
//
// It takes an ast.Node rather than raw bytes for two reasons. The node
// carries its own position, which is the only way Line can be populated
// without re-parsing the file; and taking the node means the strict-mode
// unknown-field rule that guards the rest of the level schema stops at a
// check's boundary, which is exactly right. A typo in a level's top-level
// fields must be a rejection, but "path" on a file_exists check is not a
// field this package knows or should know.
func (c *CheckSpec) UnmarshalYAML(node ast.Node) error {
	*c = CheckSpec{Line: nodeLine(node)}

	m, ok := node.(ast.MapNode)
	if !ok {
		return fmt.Errorf("a check must be a mapping of fields, found %s", node.Type())
	}

	params := map[string]any{}
	seen := map[string]bool{}

	for it := m.MapRange(); it.Next(); {
		key := it.Key().GetToken().Value
		if seen[key] {
			return fmt.Errorf("check field %q appears twice", key)
		}
		seen[key] = true

		if err := c.decodeField(key, it.Value(), params); err != nil {
			return err
		}
	}

	if len(params) > 0 {
		c.Params = params
	}
	return nil
}

// decodeField routes one key of a check's mapping to its field, or to params.
func (c *CheckSpec) decodeField(key string, value ast.Node, params map[string]any) error {
	var err error
	switch key {
	case "id":
		err = decodeNode(value, key, &c.ID)
	case "type":
		err = decodeNode(value, key, &c.Type)
	case "on_fail":
		err = decodeNode(value, key, &c.OnFail)
	case "severity":
		err = decodeNode(value, key, &c.Severity)
	case "optional":
		err = decodeNode(value, key, &c.Optional)
	case "timeout_seconds":
		err = decodeNode(value, key, &c.TimeoutSeconds)
	case "any_of":
		err = decodeNode(value, key, &c.AnyOf)
	case "all_of":
		err = decodeNode(value, key, &c.AllOf)
	case "not":
		var branch CheckSpec
		if err = decodeNode(value, key, &branch); err == nil {
			c.Not = &branch
		}
	default:
		var v any
		if err = decodeNode(value, key, &v); err == nil {
			params[key] = v
		}
	}
	return err
}

// decodeNode decodes one value node into dst, naming the field in any error
// so a wrong type reads as "check field \"optional\": ..." rather than as a
// bare reflection complaint.
func decodeNode(value ast.Node, key string, dst any) error {
	if err := yaml.NodeToValue(value, dst); err != nil {
		return fmt.Errorf("check field %q: %w", key, err)
	}
	return nil
}

// nodeLine reports the one-based line a node starts on, or zero when the
// position is unavailable. Zero is the "unknown" value Problem.Line uses, so
// an unavailable position degrades to a problem without a line rather than to
// a wrong one.
func nodeLine(node ast.Node) int {
	if node == nil {
		return 0
	}
	tok := node.GetToken()
	if tok == nil || tok.Position == nil {
		return 0
	}
	return tok.Position.Line
}

// IsComposite reports whether the check is a composition node rather than a
// direct assertion. Exactly one of Type, AnyOf, AllOf and Not may be set, and
// Validate is what enforces that.
func (c *CheckSpec) IsComposite() bool {
	return len(c.AnyOf) > 0 || len(c.AllOf) > 0 || c.Not != nil
}

// Branches returns the child checks of a composition node, in declaration
// order, or nil for a direct assertion. It lets a caller walk a check tree
// without repeating the three-way composition test.
func (c *CheckSpec) Branches() []*CheckSpec {
	var out []*CheckSpec
	for i := range c.AnyOf {
		out = append(out, &c.AnyOf[i])
	}
	for i := range c.AllOf {
		out = append(out, &c.AllOf[i])
	}
	if c.Not != nil {
		out = append(out, c.Not)
	}
	return out
}
