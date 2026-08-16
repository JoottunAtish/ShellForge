package game

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
	"github.com/JoottunAtish/ShellForge/internal/verify"
)

// TestVerifySpecCarriesEveryFieldAcross is the assertion behind the claim that
// content.CheckSpec and verify.Spec mirror each other, so the converter can stay
// mechanical.
//
// It sets every field to a distinguishable value and checks each one arrives. A
// field added to both structs and forgotten here is the failure mode: the check
// would silently lose it, and the level would verify something other than what
// its author wrote, which is the quietest possible way for a level to start
// accepting wrong answers.
func TestVerifySpecCarriesEveryFieldAcross(t *testing.T) {
	in := content.CheckSpec{
		ID:             "obj1",
		Type:           "file_content",
		OnFail:         "the authored on_fail",
		Severity:       content.SeverityWarn,
		Optional:       true,
		TimeoutSeconds: 7,
		Params:         map[string]any{"path": "/home/learner/quest/report.txt", "match": "trimmed_equals"},
		Line:           42,
	}

	got, err := verifySpec(in, 0)
	if err != nil {
		t.Fatalf("verifySpec: %v", err)
	}

	if got.ID != in.ID {
		t.Errorf("ID = %q, want %q", got.ID, in.ID)
	}
	if got.Type != in.Type {
		t.Errorf("Type = %q, want %q", got.Type, in.Type)
	}
	if got.OnFail != in.OnFail {
		t.Errorf("OnFail = %q, want %q", got.OnFail, in.OnFail)
	}
	if got.Severity != in.Severity {
		t.Errorf("Severity = %q, want %q", got.Severity, in.Severity)
	}
	if !got.Optional {
		t.Error("Optional was not carried across")
	}
	if got.TimeoutSeconds != in.TimeoutSeconds {
		t.Errorf("TimeoutSeconds = %d, want %d", got.TimeoutSeconds, in.TimeoutSeconds)
	}
	if !reflect.DeepEqual(got.Params, in.Params) {
		t.Errorf("Params = %#v, want %#v", got.Params, in.Params)
	}
}

// TestVerifySpecFieldCountsMatch is the tripwire for the test above.
//
// The field-by-field test can only check fields somebody thought to check. This
// one fails when either struct grows a field at all, which is the moment to
// decide whether the converter should carry it. It is a deliberate maintenance
// burden: the alternative is a silently incomplete conversion.
//
// The two counts are equal today, which is a coincidence rather than a rule:
// content.CheckSpec carries Line, a position in a file the engine has no use
// for, and verify.Spec carries Text, which is joined from the objectives list
// rather than read off the check. Nine fields are common to both. The numbers
// are spelled out so that a change has to be acknowledged rather than absorbed.
func TestVerifySpecFieldCountsMatch(t *testing.T) {
	const (
		// ID Type OnFail Severity Optional TimeoutSeconds AnyOf AllOf Not Params Line
		wantContentFields = 11
		// ID Text Type OnFail Severity Optional TimeoutSeconds Params AnyOf AllOf Not
		wantVerifyFields = 11
	)

	gotContent := reflect.TypeOf(content.CheckSpec{}).NumField()
	gotVerify := reflect.TypeOf(verify.Spec{}).NumField()

	if gotContent != wantContentFields {
		t.Errorf("content.CheckSpec has %d fields, expected %d.\n"+
			"A field was added or removed. Decide whether verifySpec should carry it, then update this count and TestVerifySpecCarriesEveryFieldAcross.",
			gotContent, wantContentFields)
	}
	if gotVerify != wantVerifyFields {
		t.Errorf("verify.Spec has %d fields, expected %d.\n"+
			"A field was added or removed. Decide whether verifySpec should populate it, then update this count and TestVerifySpecCarriesEveryFieldAcross.",
			gotVerify, wantVerifyFields)
	}
}

// TestVerifySpecRecursesThroughEveryCompositionKind walks a three-deep tree of
// mixed kinds and asserts the structure and the messages survive.
func TestVerifySpecRecursesThroughEveryCompositionKind(t *testing.T) {
	in := content.CheckSpec{
		ID:     "obj1",
		OnFail: "OUTER",
		Not: &content.CheckSpec{
			OnFail: "MIDDLE",
			AnyOf: []content.CheckSpec{
				{
					OnFail: "INNER-A",
					AllOf: []content.CheckSpec{
						{Type: "file_exists", OnFail: "LEAF-1", Params: map[string]any{"path": "/a"}},
						{Type: "file_exists", OnFail: "LEAF-2", Params: map[string]any{"path": "/b"}},
					},
				},
				{Type: "file_exists", OnFail: "INNER-B", Params: map[string]any{"path": "/c"}},
			},
		},
	}

	got, err := verifySpec(in, 0)
	if err != nil {
		t.Fatalf("verifySpec: %v", err)
	}

	if got.Not == nil {
		t.Fatal("Not was not converted")
	}
	if got.Not.OnFail != "MIDDLE" {
		t.Errorf("Not.OnFail = %q, want %q", got.Not.OnFail, "MIDDLE")
	}
	if len(got.Not.AnyOf) != 2 {
		t.Fatalf("Not.AnyOf has %d branches, want 2", len(got.Not.AnyOf))
	}
	if len(got.Not.AnyOf[0].AllOf) != 2 {
		t.Fatalf("Not.AnyOf[0].AllOf has %d branches, want 2", len(got.Not.AnyOf[0].AllOf))
	}

	// Declaration order is part of the contract: any_of reports the first
	// passing branch and all_of the first failing one, so reordering branches
	// changes which message a learner reads.
	if got.Not.AnyOf[0].AllOf[0].OnFail != "LEAF-1" || got.Not.AnyOf[0].AllOf[1].OnFail != "LEAF-2" {
		t.Error("branch order was not preserved")
	}
	if got.Not.AnyOf[1].OnFail != "INNER-B" {
		t.Errorf("AnyOf[1].OnFail = %q, want %q", got.Not.AnyOf[1].OnFail, "INNER-B")
	}
}

// TestVerifySpecDoesNotPushMetadataOntoBranches pins the authoring invariant
// that id, optional and severity describe a whole objective and never a branch.
//
// A converter that helpfully copied them down would make a branch look
// independently reportable, and the engine would then have two sources of truth
// about whether an objective is a bonus.
func TestVerifySpecDoesNotPushMetadataOntoBranches(t *testing.T) {
	in := content.CheckSpec{
		ID: "obj1", OnFail: "OUTER", Optional: true, Severity: content.SeverityWarn,
		AllOf: []content.CheckSpec{
			{Type: "file_exists", OnFail: "LEAF", Params: map[string]any{"path": "/a"}},
		},
	}

	got, err := verifySpec(in, 0)
	if err != nil {
		t.Fatalf("verifySpec: %v", err)
	}

	branch := got.AllOf[0]
	if branch.ID != "" {
		t.Errorf("branch inherited the objective's id %q", branch.ID)
	}
	if branch.Optional {
		t.Error("branch inherited Optional from the objective")
	}
	if branch.Severity != "" {
		t.Errorf("branch inherited Severity %q from the objective", branch.Severity)
	}
}

// TestVerifySpecsJoinsObjectiveText is the join that lets the engine assemble a
// checklist. Without it every objective would render with a blank line.
func TestVerifySpecsJoinsObjectiveText(t *testing.T) {
	checks := []content.CheckSpec{
		{ID: "obj1", Type: "file_exists", OnFail: "a", Params: map[string]any{"path": "/a"}},
		{ID: "obj2", Type: "file_exists", OnFail: "b", Params: map[string]any{"path": "/b"}},
	}
	objectives := []content.Objective{
		{ID: "obj1", Text: "report.txt holds the count"},
		{ID: "obj2", Text: "codes.txt is sorted", Optional: true},
	}

	specs, err := verifySpecs(checks, objectives)
	if err != nil {
		t.Fatalf("verifySpecs: %v", err)
	}

	if specs[0].Text != "report.txt holds the count" {
		t.Errorf("specs[0].Text = %q", specs[0].Text)
	}
	if specs[1].Text != "codes.txt is sorted" {
		t.Errorf("specs[1].Text = %q", specs[1].Text)
	}
}

// TestOptionalOnEitherObjectiveOrCheckMakesABonus resolves the schema's
// two-sources-of-truth problem in the safe direction.
//
// optional lives on both content.Objective and content.CheckSpec and the two can
// disagree. Treating a disagreement as "required" would let a level fail a
// learner on something its own checklist showed as a bonus, which is worse than
// the reverse, so either saying optional wins.
func TestOptionalOnEitherObjectiveOrCheckMakesABonus(t *testing.T) {
	tests := []struct {
		name         string
		checkOpt     bool
		objectiveOpt bool
		want         bool
	}{
		{name: "neither", checkOpt: false, objectiveOpt: false, want: false},
		{name: "only the check", checkOpt: true, objectiveOpt: false, want: true},
		{name: "only the objective", checkOpt: false, objectiveOpt: true, want: true},
		{name: "both", checkOpt: true, objectiveOpt: true, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			specs, err := verifySpecs(
				[]content.CheckSpec{{ID: "obj1", Type: "file_exists", OnFail: "a",
					Optional: tt.checkOpt, Params: map[string]any{"path": "/a"}}},
				[]content.Objective{{ID: "obj1", Text: "t", Optional: tt.objectiveOpt}},
			)
			if err != nil {
				t.Fatalf("verifySpecs: %v", err)
			}
			if specs[0].Optional != tt.want {
				t.Errorf("Optional = %v, want %v", specs[0].Optional, tt.want)
			}
		})
	}
}

// TestVerifySpecsToleratesACheckWithNoObjective keeps a severity: warn check
// working. The authoring invariants say a warn check needs no objective, so
// joining must not fail when it finds none.
func TestVerifySpecsToleratesACheckWithNoObjective(t *testing.T) {
	specs, err := verifySpecs(
		[]content.CheckSpec{
			{ID: "nocheat", Type: "command_not_matched", OnFail: "a note",
				Severity: content.SeverityWarn, Params: map[string]any{"pattern": "^echo"}},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("verifySpecs: %v", err)
	}
	if specs[0].Text != "" {
		t.Errorf("Text = %q, want empty for a check with no objective", specs[0].Text)
	}
}

// TestVerifySpecRejectsAmbiguousShapes covers the shapes the engine also
// refuses. Refusing here as well is what lets the message name the line in the
// level file, which the engine cannot see.
func TestVerifySpecRejectsAmbiguousShapes(t *testing.T) {
	tests := []struct {
		name    string
		spec    content.CheckSpec
		wantMsg string
	}{
		{
			name: "both a type and a composition node",
			spec: content.CheckSpec{ID: "obj1", Type: "file_mode", OnFail: "a", Line: 12,
				AnyOf: []content.CheckSpec{{Type: "file_exists", OnFail: "b", Params: map[string]any{"path": "/a"}}}},
			wantMsg: "declares both type",
		},
		{
			name:    "neither a type nor a composition node",
			spec:    content.CheckSpec{ID: "obj1", OnFail: "a", Line: 12},
			wantMsg: "declares neither",
		},
		{
			name: "two composition kinds at once",
			spec: content.CheckSpec{ID: "obj1", OnFail: "a", Line: 12,
				AnyOf: []content.CheckSpec{{Type: "file_exists", OnFail: "b", Params: map[string]any{"path": "/a"}}},
				AllOf: []content.CheckSpec{{Type: "file_exists", OnFail: "c", Params: map[string]any{"path": "/b"}}}},
			wantMsg: "more than one of any_of",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := verifySpec(tt.spec, 0)
			if err == nil {
				t.Fatal("verifySpec accepted an ambiguous shape")
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("error does not explain the problem: %v", err)
			}

			var uxErr *ux.Error
			if !errors.As(err, &uxErr) {
				t.Fatalf("error is not a *ux.Error, so a learner would see a bare Go error: %T", err)
			}
			if uxErr.DocAnchor != docAnchorLevelPackInvalid {
				t.Errorf("DocAnchor = %q, want %q", uxErr.DocAnchor, docAnchorLevelPackInvalid)
			}
			if uxErr.Remediation == "" {
				t.Error("no remediation, which CLAUDE.md non-negotiable 6 forbids")
			}
			if !strings.Contains(uxErr.Op, "12") {
				t.Errorf("Op %q does not name the line, which is how an author finds an unnamed branch", uxErr.Op)
			}
		})
	}
}

// TestVerifySpecNamesTheLineForABranchWithNoID is why content.CheckSpec.Line
// exists. A branch has no id, so the line number is the only thing that locates
// it in a file.
func TestVerifySpecNamesTheLineForABranchWithNoID(t *testing.T) {
	in := content.CheckSpec{
		ID: "obj1", OnFail: "OUTER", Line: 10,
		AllOf: []content.CheckSpec{
			{OnFail: "a broken branch", Line: 14}, // neither type nor composition
		},
	}

	_, err := verifySpec(in, 0)
	if err == nil {
		t.Fatal("verifySpec accepted a branch that is neither a type nor a composition node")
	}
	if !strings.Contains(err.Error(), "14") {
		t.Errorf("error names no line, or the wrong one: %v", err)
	}
	if strings.Contains(err.Error(), `check "obj1"`) {
		t.Errorf("error attributes the branch's problem to the outer objective, which sends the author to the wrong line: %v", err)
	}
}

// TestVerifySpecRefusesTooDeepATree bounds recursion over pack data. Without a
// bound this walk is a stack overflow driven by a level file.
func TestVerifySpecRefusesTooDeepATree(t *testing.T) {
	spec := content.CheckSpec{Type: "file_exists", OnFail: "leaf", Params: map[string]any{"path": "/a"}}
	for i := 0; i <= maxSpecDepth; i++ {
		spec = content.CheckSpec{OnFail: "wrapper", AllOf: []content.CheckSpec{spec}}
	}
	spec.ID = "obj1"

	if _, err := verifySpec(spec, 0); err == nil {
		t.Fatalf("verifySpec accepted a tree deeper than %d", maxSpecDepth)
	}
}

// TestVerifyBranchesReturnsNilForNoBranches keeps a converted leaf identical to
// one written by hand. verify.Spec.IsComposite reads a non-empty AnyOf as "this
// is a composition node", so an allocated empty slice would be a latent trap for
// any future code that checked for nil instead of length.
func TestVerifyBranchesReturnsNilForNoBranches(t *testing.T) {
	got, err := verifySpec(content.CheckSpec{
		ID: "obj1", Type: "file_exists", OnFail: "a", Params: map[string]any{"path": "/a"},
	}, 0)
	if err != nil {
		t.Fatalf("verifySpec: %v", err)
	}
	if got.AnyOf != nil || got.AllOf != nil || got.Not != nil {
		t.Errorf("a leaf converted with non-nil composition fields: AnyOf=%v AllOf=%v Not=%v", got.AnyOf, got.AllOf, got.Not)
	}
	if got.IsComposite() {
		t.Error("a converted leaf reports itself composite")
	}
}
