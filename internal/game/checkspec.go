package game

import (
	"fmt"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
	"github.com/JoottunAtish/ShellForge/internal/verify"
)

// The content to verify translation, and the only place in the codebase that
// knows both shapes.
//
// internal/content owns the level schema and internal/verify owns what a check
// means. They are peers at layer 3 and neither imports the other, which is what
// keeps the check registry independent of the YAML shape. Something has to sit
// above both and join them, and this is it: layer 4, the lowest layer that may
// import either.
//
// The two structs mirror each other field for field on purpose, so this file
// stays a mechanical recursion rather than a translation with judgement in it.
// TestVerifySpecCarriesEveryFieldAcross asserts that mirroring rather than
// trusting it.

// maxSpecDepth bounds how deeply a level may nest composition nodes.
//
// It matches internal/verify's own bound, which the engine also enforces. Both
// exist because this recursion walks pack YAML, which is data this process did
// not write, and the failure mode of an unbounded recursion over hostile data is
// a stack overflow rather than an error message. Checking here as well means the
// author gets a message naming the level's own field before the engine is
// reached at all.
const maxSpecDepth = 8

// verifySpecs converts a level's authored checks into engine specs, joining each
// one to its objective text.
//
// objectives is the level's own objectives list. The pack validator proves check
// ids and objective ids correspond one to one in both directions, so every
// non-warn check finds its text; a check with no matching objective still
// converts, because refusing here would turn a validator's job into a runtime
// failure a learner would see.
func verifySpecs(checks []content.CheckSpec, objectives []content.Objective) ([]verify.Spec, error) {
	text := make(map[string]string, len(objectives))
	optional := make(map[string]bool, len(objectives))
	for _, obj := range objectives {
		text[obj.ID] = obj.Text
		optional[obj.ID] = obj.Optional
	}

	specs := make([]verify.Spec, 0, len(checks))
	for i := range checks {
		spec, err := verifySpec(checks[i], 0)
		if err != nil {
			return nil, err
		}

		spec.Text = text[spec.ID]

		// optional lives on both content.Objective and content.CheckSpec, and
		// the two can disagree. Either saying so makes the objective a bonus:
		// treating a disagreement as "required" would let a level fail a
		// learner on something its own checklist presented as optional, which
		// is the worse direction to get wrong. verify.Spec.Optional is the only
		// authority from here on.
		//
		// TODO(v0.2): the schema should not carry the flag twice. Tracked as a
		// content follow-up rather than fixed here, because removing one of
		// them changes the level format.
		if optional[spec.ID] {
			spec.Optional = true
		}

		specs = append(specs, spec)
	}
	return specs, nil
}

// verifySpec converts one node, recursing through composition branches.
//
// Metadata does not descend. ID, OnFail, Severity and Optional are read from the
// node that carries them and children inherit nothing, which matches the
// authoring invariant that those fields appear only on the outermost check of an
// objective. A branch's own on_fail is carried across so the engine has it, even
// though a composite reports its own message and never a child's: dropping it
// here would mean this function silently discarded authored content, and the
// engine is the right place for that decision.
func verifySpec(c content.CheckSpec, depth int) (verify.Spec, error) {
	if depth > maxSpecDepth {
		return verify.Spec{}, specProblem(c, fmt.Errorf(
			"composition nests more than %d deep; a check that needs this much structure reads better as a single type: script check",
			maxSpecDepth))
	}

	spec := verify.Spec{
		ID:             c.ID,
		Type:           c.Type,
		OnFail:         c.OnFail,
		Severity:       c.Severity,
		Optional:       c.Optional,
		TimeoutSeconds: c.TimeoutSeconds,
		Params:         c.Params,
	}

	// Refuse the ambiguous shapes here as well as in the engine. The engine
	// cannot name the level or the line, and an author fixing YAML wants both.
	composite := c.IsComposite()
	if c.Type != "" && composite {
		return verify.Spec{}, specProblem(c, fmt.Errorf(
			"declares both type %q and a composition node; a check is either a type or exactly one of any_of, all_of, not", c.Type))
	}
	if c.Type == "" && !composite {
		return verify.Spec{}, specProblem(c, fmt.Errorf(
			"declares neither a type nor a composition node"))
	}

	kinds := 0
	for _, set := range []bool{len(c.AnyOf) > 0, len(c.AllOf) > 0, c.Not != nil} {
		if set {
			kinds++
		}
	}
	if kinds > 1 {
		return verify.Spec{}, specProblem(c, fmt.Errorf(
			"sets more than one of any_of, all_of and not; set exactly one"))
	}

	var err error
	if spec.AnyOf, err = verifyBranches(c.AnyOf, depth); err != nil {
		return verify.Spec{}, err
	}
	if spec.AllOf, err = verifyBranches(c.AllOf, depth); err != nil {
		return verify.Spec{}, err
	}
	if c.Not != nil {
		branch, err := verifySpec(*c.Not, depth+1)
		if err != nil {
			return verify.Spec{}, err
		}
		spec.Not = &branch
	}

	return spec, nil
}

// verifyBranches converts a slice of branches, preserving order.
//
// It returns nil rather than an empty slice for no branches, because
// verify.Spec.IsComposite reads a non-empty AnyOf or AllOf as "this is a
// composition node". An allocated empty slice would still be len 0 and so would
// read correctly today, but returning nil makes the intent explicit and keeps a
// converted leaf byte-identical to one built by hand.
func verifyBranches(branches []content.CheckSpec, depth int) ([]verify.Spec, error) {
	if len(branches) == 0 {
		return nil, nil
	}
	out := make([]verify.Spec, 0, len(branches))
	for i := range branches {
		spec, err := verifySpec(branches[i], depth+1)
		if err != nil {
			return nil, err
		}
		out = append(out, spec)
	}
	return out, nil
}

// specProblem wraps err with everything an author needs to find the check: the
// id when it has one, and the line in the level file.
//
// A composition branch carries no id, so the line is what locates it. That is
// why content.CheckSpec.Line exists, and this is the one place outside the
// validator that uses it.
func specProblem(c content.CheckSpec, err error) error {
	where := fmt.Sprintf("line %d", c.Line)
	if c.ID != "" {
		where = fmt.Sprintf("check %q (line %d)", c.ID, c.Line)
	}
	return ux.Fail(
		fmt.Sprintf("read the checks for this level: %s", where),
		err,
		remediationLevelPackInvalid,
		docAnchorLevelPackInvalid,
	)
}
