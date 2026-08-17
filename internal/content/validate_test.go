package content

import (
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/JoottunAtish/ShellForge/internal/platform"
)

// validateFixture loads and validates a fixture pack with the fake registry.
func validateFixture(t *testing.T, fsys fstest.MapFS) Report {
	t.Helper()
	pack := loadFixture(t, fsys)
	return Validate(pack, newFakeTypeChecker(), WithPackFS(fsys, "."))
}

// TestValidPackHasNoProblems is the test that catches an over-eager rule. A
// validator that reports something about a correct pack is worse than one
// that misses a case, because an author learns to ignore its output.
func TestValidPackHasNoProblems(t *testing.T) {
	report := validateFixture(t, fixturePack("", defaultLevel()))

	if !report.OK() {
		t.Errorf("a valid pack did not pass validation:\n%s", formatReport(report))
	}
	if len(report.Problems) != 0 {
		t.Errorf("a valid pack produced problems:\n%s", formatReport(report))
	}
}

// TestValidateReportsEveryProblemNotJustTheFirst is an explicit acceptance
// criterion. An author who can fix one error per run stops using the
// validator.
func TestValidateReportsEveryProblemNotJustTheFirst(t *testing.T) {
	lvl := defaultLevel()
	lvl.OmitFields = []string{"title", "concepts", "briefing"}
	lvl.Solution = "solution: \"\"\n"

	report := validateFixture(t, fixturePack("", lvl))

	for _, field := range []string{"title", "concepts", "briefing", "solution"} {
		requireProblem(t, report, "nav-01", field, ProblemError, "")
	}
}

// TestValidateLevelIdentity covers the id rules: shape, and duplication
// across two files.
func TestValidateLevelIdentity(t *testing.T) {
	t.Run("malformed id", func(t *testing.T) {
		lvl := defaultLevel()
		lvl.ID = "Nav_01"

		acts := "  - id: act1\n    title: \"Act One\"\n    levels: [Nav_01]\n"
		report := validateFixture(t, fixturePack(acts, lvl))

		requireProblem(t, report, "Nav_01", "id", ProblemError, "lowercase letters, digits and hyphens")
	})

	t.Run("leading hyphen is refused", func(t *testing.T) {
		lvl := defaultLevel()
		lvl.ID = "-nav-01"

		acts := "  - id: act1\n    title: \"Act One\"\n    levels: [-nav-01]\n"
		report := validateFixture(t, fixturePack(acts, lvl))

		requireProblem(t, report, "-nav-01", "id", ProblemError, "starting with a letter or a digit")
	})

	t.Run("duplicate id across files", func(t *testing.T) {
		first, second := defaultLevel(), defaultLevel()
		fsys := fixturePack("", first)
		fsys["levels/02-nav-01-copy.yaml"] = &fstest.MapFile{Data: []byte(second.YAML())}

		report := validateFixture(t, fsys)

		requireProblem(t, report, "nav-01", "id", ProblemError, "already declared in")
	})
}

// TestLevelIDIsNarrowerThanIdentifier pins the relationship the security
// rules depend on: every level id the validator accepts is also safe as an
// argv element and as a path segment. If either pattern is loosened without
// the other, this fails.
func TestLevelIDIsNarrowerThanIdentifier(t *testing.T) {
	accepted := []string{"nav-01", "files-04", "a", "0", "boss-final", "pipe-05"}
	for _, id := range accepted {
		if !levelIDPattern.MatchString(id) {
			t.Errorf("levelIDPattern should accept %q", id)
			continue
		}
		if !platform.ValidIdentifier(id) {
			t.Errorf("level id %q passes levelIDPattern but not platform.ValidIdentifier, so it is not safe as an argv element", id)
		}
	}

	// The narrowing itself: these are safe as argv elements but are not
	// legal level ids, and the level rule is what refuses them.
	for _, id := range []string{"Nav01", "nav_01"} {
		if !platform.ValidIdentifier(id) {
			t.Errorf("test premise is wrong: %q should pass platform.ValidIdentifier", id)
		}
		if levelIDPattern.MatchString(id) {
			t.Errorf("levelIDPattern should refuse %q", id)
		}
	}

	if _, err := regexp.Compile(platform.IdentifierPattern); err != nil {
		t.Fatalf("platform.IdentifierPattern does not compile: %v", err)
	}
}

// TestValidateOnFailIsRequiredAtEveryDepth covers the composition depth rule.
// A composite that falls back to whichever child failed explains the wrong
// thing to the learner.
func TestValidateOnFailIsRequiredAtEveryDepth(t *testing.T) {
	lvl := defaultLevel()
	lvl.Checks = `checks:
  - id: obj1
    on_fail: "The count doesn't look right."
    any_of:
      - type: file_exists
        path: /home/learner/quest/a.txt
        on_fail: "Nothing at a.txt."
      - not:
          type: file_exists
          path: /home/learner/quest/b.txt
        on_fail: ""
`
	report := validateFixture(t, fixturePack("", lvl))

	requireProblem(t, report, "nav-01", "checks[0].any_of[1].on_fail", ProblemError, "must not be empty")
	requireNoProblem(t, report, "nav-01", "checks[0].on_fail")
	requireNoProblem(t, report, "nav-01", "checks[0].any_of[0].on_fail")
}

// TestValidateNestedCompositionCarriesLineNumbers proves a problem deep in a
// check tree can still name a place in the file.
func TestValidateNestedCompositionCarriesLineNumbers(t *testing.T) {
	lvl := defaultLevel()
	lvl.Checks = `checks:
  - id: obj1
    on_fail: "Something in the tree is wrong."
    all_of:
      - type: no_such_check
        on_fail: "Never registered."
`
	report := validateFixture(t, fixturePack("", lvl))

	p, ok := findProblem(report, "nav-01", "checks[0].all_of[0].type")
	if !ok {
		t.Fatalf("no problem for the unknown nested type:\n%s", formatReport(report))
	}
	if p.Line == 0 {
		t.Error("a nested check problem carries no line number, so an author cannot find it")
	}
	if !strings.Contains(p.Message, "no_such_check") {
		t.Errorf("message should name the unknown type, got %q", p.Message)
	}
}

// TestValidateCompositionShape refuses a check that is both a type and a
// composition, and one that sets two composition nodes at once.
func TestValidateCompositionShape(t *testing.T) {
	t.Run("type and composition together", func(t *testing.T) {
		lvl := defaultLevel()
		lvl.Checks = `checks:
  - id: obj1
    type: file_exists
    path: /home/learner/quest/a.txt
    on_fail: "Confused check."
    any_of:
      - type: file_exists
        path: /home/learner/quest/b.txt
        on_fail: "Nothing at b.txt."
`
		report := validateFixture(t, fixturePack("", lvl))
		requireProblem(t, report, "nav-01", "checks[0]", ProblemError, "not both")
	})

	t.Run("neither type nor composition", func(t *testing.T) {
		lvl := defaultLevel()
		lvl.Checks = `checks:
  - id: obj1
    on_fail: "Nothing to run."
`
		report := validateFixture(t, fixturePack("", lvl))
		requireProblem(t, report, "nav-01", "checks[0].type", ProblemError, "must name a check type")
	})

	t.Run("two composition nodes at once", func(t *testing.T) {
		lvl := defaultLevel()
		lvl.Checks = `checks:
  - id: obj1
    on_fail: "Confused composite."
    any_of:
      - type: file_exists
        path: /home/learner/quest/a.txt
        on_fail: "Nothing at a.txt."
    all_of:
      - type: file_exists
        path: /home/learner/quest/b.txt
        on_fail: "Nothing at b.txt."
`
		report := validateFixture(t, fixturePack("", lvl))
		requireProblem(t, report, "nav-01", "checks[0]", ProblemError, "exactly one of any_of, all_of and not")
	})
}

// TestValidateOptionalOnACheckNamesTheObjective is the core rule issue #97
// adds: optional belongs on the objective, and nowhere else. A check that
// also declares it is refused, whether or not the objective agrees, because
// the check was never the field's home to begin with.
func TestValidateOptionalOnACheckNamesTheObjective(t *testing.T) {
	t.Run("neither", func(t *testing.T) {
		lvl := defaultLevel()
		lvl.Objectives += "  - id: obj2\n    text: \"A second thing to check\"\n"
		lvl.Checks += "  - id: obj2\n    type: file_exists\n    path: /home/learner/quest/bonus.txt\n    on_fail: \"No bonus.txt yet.\"\n"

		report := validateFixture(t, fixturePack("", lvl))
		requireNoProblem(t, report, "nav-01", "checks[1].optional")
	})

	t.Run("the objective only", func(t *testing.T) {
		lvl := defaultLevel()
		lvl.Objectives += "  - id: obj2\n    text: \"A bonus thing to check\"\n    optional: true\n"
		lvl.Checks += "  - id: obj2\n    type: file_exists\n    path: /home/learner/quest/bonus.txt\n    on_fail: \"No bonus.txt yet.\"\n"

		report := validateFixture(t, fixturePack("", lvl))
		requireNoProblem(t, report, "nav-01", "checks[1].optional")
		if !report.OK() {
			t.Errorf("optional declared only on the objective should be legal:\n%s", formatReport(report))
		}
	})

	t.Run("the check only", func(t *testing.T) {
		lvl := defaultLevel()
		lvl.Objectives += "  - id: obj2\n    text: \"A bonus thing to check\"\n"
		lvl.Checks += "  - id: obj2\n    optional: true\n    type: file_exists\n    path: /home/learner/quest/bonus.txt\n    on_fail: \"No bonus.txt yet.\"\n"

		report := validateFixture(t, fixturePack("", lvl))
		requireProblem(t, report, "nav-01", "checks[1].optional", ProblemError, "belongs on the objective, not on the check")
	})

	t.Run("both", func(t *testing.T) {
		lvl := defaultLevel()
		lvl.Objectives += "  - id: obj2\n    text: \"A bonus thing to check\"\n    optional: true\n"
		lvl.Checks += "  - id: obj2\n    optional: true\n    type: file_exists\n    path: /home/learner/quest/bonus.txt\n    on_fail: \"No bonus.txt yet.\"\n"

		report := validateFixture(t, fixturePack("", lvl))
		requireProblem(t, report, "nav-01", "checks[1].optional", ProblemError, "belongs on the objective, not on the check")
	})
}

// TestValidateOptionalOnABranchNamesTheObjectiveToo is the depth-independent
// half of the same rule. A problem is already reported at this field today,
// but with the old wording that told an author the outermost check may
// declare optional. That is no longer true: the objective is optional's only
// home at every depth, so this is a message-content change, not a new field.
func TestValidateOptionalOnABranchNamesTheObjectiveToo(t *testing.T) {
	lvl := defaultLevel()
	lvl.Checks = `checks:
  - id: obj1
    on_fail: "Outer composite."
    all_of:
      - type: file_exists
        optional: true
        path: /home/learner/quest/answer.txt
        on_fail: "No answer.txt yet."
`
	report := validateFixture(t, fixturePack("", lvl))

	requireProblem(t, report, "nav-01", "checks[0].all_of[0].optional", ProblemError, "belongs on the objective, not on the check")
}

// paramsSeenChecker wraps the fake registry but actually inspects Params,
// unlike fakeTypeChecker.ValidateParams, which ignores them. It exists only
// to prove optional never reaches Params: the real registry would reject an
// unrecognized key, and this is what a content-only test can do without
// depending on internal/verify.
type paramsSeenChecker struct{ *fakeTypeChecker }

func (p paramsSeenChecker) ValidateParams(typeName string, params map[string]any) error {
	if _, leaked := params["optional"]; leaked {
		return errOptionalLeaked{}
	}
	return p.fakeTypeChecker.ValidateParams(typeName, params)
}

// errOptionalLeaked is what paramsSeenChecker returns if optional ever
// reaches Params, standing in for the "unknown parameter" message the real
// registry would produce.
type errOptionalLeaked struct{}

func (errOptionalLeaked) Error() string { return `unknown parameter "optional"` }

// TestValidateOptionalOnACheckIsNotAnUnknownParameter pins the Params trap:
// optional stays a reserved key on CheckSpec, so a check that declares it
// gets the objective-ownership error below, never a registry complaint about
// an unrecognized parameter. paramsSeenChecker is what proves it, since
// fakeTypeChecker's own ValidateParams ignores its params argument entirely.
func TestValidateOptionalOnACheckIsNotAnUnknownParameter(t *testing.T) {
	lvl := defaultLevel()
	lvl.Objectives += "  - id: obj2\n    text: \"A bonus thing to check\"\n"
	lvl.Checks += "  - id: obj2\n    optional: true\n    type: file_exists\n    path: /home/learner/quest/bonus.txt\n    on_fail: \"No bonus.txt yet.\"\n"

	pack := loadFixture(t, fixturePack("", lvl))
	report := Validate(pack, paramsSeenChecker{newFakeTypeChecker()})

	requireProblem(t, report, "nav-01", "checks[1].optional", ProblemError, "belongs on the objective")
	requireNoProblem(t, report, "nav-01", "checks[1]")
}

// TestValidateJournalChecksCannotGatePassing is the rule this ticket adds.
// The journal records what the learner typed and the learner can forge it
// from inside the sandbox, so it may never decide whether a level is passed.
func TestValidateJournalChecksCannotGatePassing(t *testing.T) {
	gating := `checks:
  - id: obj1
    type: %s
    pattern: '^\s*pwd\s*$'
    on_fail: "Print the working directory first."
`
	for _, checkType := range []string{"command_matched", "command_not_matched"} {
		t.Run(checkType+" required is refused", func(t *testing.T) {
			lvl := defaultLevel()
			lvl.Checks = strings.Replace(gating, "%s", checkType, 1)

			report := validateFixture(t, fixturePack("", lvl))

			requireProblem(t, report, "nav-01", "checks[0]", ProblemError, "optional: true on this check's objective")
			// It is also the only check, so the level has nothing that can
			// fail it. Both rules must fire, not one masking the other.
			requireProblem(t, report, "nav-01", "checks", ProblemError, "a level you cannot fail is not a level")
		})

		t.Run(checkType+" optional is accepted", func(t *testing.T) {
			lvl := defaultLevel()
			lvl.Objectives += "  - id: bonus\n    text: \"Did it in one command\"\n    optional: true\n"
			lvl.Checks += "  - id: bonus\n    type: " + checkType +
				"\n    pattern: '^\\s*pwd\\s*$'\n    on_fail: \"There is a shorter way.\"\n"

			report := validateFixture(t, fixturePack("", lvl))

			// Legal, but this build wires no runtime journal yet (issue #88),
			// so the check is a warning rather than a clean pass: it verifies
			// nothing until then, and that gap must stay visible to the
			// author rather than passing silently.
			requireProblem(t, report, "nav-01", "checks[1]", ProblemWarning, "reads the command journal")
			if !report.OK() {
				t.Errorf("an optional journal check should be legal:\n%s", formatReport(report))
			}
		})

		t.Run(checkType+" severity warn is accepted", func(t *testing.T) {
			lvl := defaultLevel()
			lvl.Objectives += "  - id: nocheat\n    text: \"Did not hardcode it\"\n    optional: true\n"
			lvl.Checks += "  - id: nocheat\n    severity: warn\n    type: " + checkType +
				"\n    pattern: '^\\s*echo\\s'\n    on_fail: \"Hardcoding works today, not at 3 AM.\"\n"

			report := validateFixture(t, fixturePack("", lvl))

			// Same gap as above: legal, but not yet functional.
			requireProblem(t, report, "nav-01", "checks[1]", ProblemWarning, "reads the command journal")
			if !report.OK() {
				t.Errorf("a severity: warn journal check should be legal:\n%s", formatReport(report))
			}
		})
	}
}

// TestValidateGatingReadsEachObjectivesOwnOptionalFlag pins that the gating
// lookup is keyed by check id, not collapsed into one shared flag. bonus's
// objective is optional: true and syntax's is not, so an implementation that
// used a single boolean, or ORed every objective's optional together, would
// wrongly exempt syntax too. It must not.
func TestValidateGatingReadsEachObjectivesOwnOptionalFlag(t *testing.T) {
	lvl := defaultLevel()
	lvl.Objectives += "  - id: bonus\n    text: \"Did it in one command\"\n    optional: true\n"
	lvl.Objectives += "  - id: syntax\n    text: \"Used the pipeline\"\n"
	lvl.Checks += "  - id: bonus\n    type: command_matched\n    pattern: '^\\s*pwd\\s*$'\n    on_fail: \"There is a shorter way.\"\n"
	lvl.Checks += "  - id: syntax\n    type: command_matched\n    pattern: '^\\s*pwd\\s*$'\n    on_fail: \"Use the pipeline.\"\n"

	report := validateFixture(t, fixturePack("", lvl))

	requireProblem(t, report, "nav-01", "checks[1]", ProblemWarning, "reads the command journal")
	for _, p := range report.Problems {
		if p.LevelID == "nav-01" && p.Field == "checks[1]" && p.Level == ProblemError {
			t.Errorf("checks[1]'s objective is optional: true, so it must not also get the gating error: %s", p.Message)
		}
	}
	requireProblem(t, report, "nav-01", "checks[2]", ProblemError, "must not decide whether a level is passed")
}

// TestValidateJournalChecksWarnTheirEmptyOutcome is the regression test for
// the wiring gap issue #88 tracks: no runtime session in this build supplies
// a real verify.JournalReader, so a legal journal check degrades into a wrong
// answer rather than an error. command_matched can never pass and
// command_not_matched can never fire; the validator must say so rather than
// stay quiet, so the gap is visible to an author instead of only to a
// learner who plays the level.
func TestValidateJournalChecksWarnTheirEmptyOutcome(t *testing.T) {
	t.Run("command_matched names that it can never pass", func(t *testing.T) {
		lvl := defaultLevel()
		lvl.Objectives += "  - id: bonus\n    text: \"Did it in one command\"\n    optional: true\n"
		lvl.Checks += "  - id: bonus\n    type: command_matched" +
			"\n    pattern: '^\\s*pwd\\s*$'\n    on_fail: \"There is a shorter way.\"\n"

		report := validateFixture(t, fixturePack("", lvl))

		requireProblem(t, report, "nav-01", "checks[1]", ProblemWarning, "can never pass")
	})

	t.Run("command_not_matched names that it can never fire", func(t *testing.T) {
		lvl := defaultLevel()
		lvl.Checks += "  - id: nocheat\n    severity: warn\n    type: command_not_matched" +
			"\n    pattern: '^\\s*echo\\s'\n    on_fail: \"Hardcoding works today, not at 3 AM.\"\n"

		report := validateFixture(t, fixturePack("", lvl))

		requireProblem(t, report, "nav-01", "checks[1]", ProblemWarning, "can never fire")
	})

	t.Run("a gating journal check gets the error, not the warning too", func(t *testing.T) {
		lvl := defaultLevel()
		lvl.Checks = `checks:
  - id: obj1
    type: command_matched
    pattern: '^\s*pwd\s*$'
    on_fail: "Print the working directory first."
`
		report := validateFixture(t, fixturePack("", lvl))

		requireProblem(t, report, "nav-01", "checks[0]", ProblemError, "must not decide whether a level is passed")
		for _, p := range report.Problems {
			if p.LevelID == "nav-01" && p.Field == "checks[0]" && p.Level == ProblemWarning {
				t.Errorf("a gating journal check should get only the error, not the empty-journal warning too: %s", p.Message)
			}
		}
	})
}

// TestValidateJournalChecksCannotHideInsideAComposite is the regression test
// for the bypass the first review of this package found.
//
// Reading optional and severity node by node is not enough. Those fields
// describe a whole objective, so a journal check nested inside a required
// composite satisfies the per-node rule while the objective as a whole still
// turns on a signal the learner can forge from inside the sandbox.
func TestValidateJournalChecksCannotHideInsideAComposite(t *testing.T) {
	t.Run("severity warn on the branch does not exempt the tree", func(t *testing.T) {
		lvl := defaultLevel()
		lvl.Checks = `checks:
  - id: obj1
    on_fail: "Outer composite."
    all_of:
      - type: command_matched
        severity: warn
        pattern: 'pwd'
        on_fail: "Print the working directory first."
`
		report := validateFixture(t, fixturePack("", lvl))

		requireProblem(t, report, "nav-01", "checks[0].all_of[0]", ProblemError, "must not decide whether a level is passed")
		requireProblem(t, report, "nav-01", "checks[0].all_of[0].severity", ProblemError, "means nothing on a composition branch")
		// The tree asserts nothing about state, so it cannot be the check
		// that fails the level either. Both problems must surface together.
		requireProblem(t, report, "nav-01", "checks", ProblemError, "a level you cannot fail is not a level")
	})

	t.Run("optional on the branch does not exempt the tree", func(t *testing.T) {
		lvl := defaultLevel()
		lvl.Checks = `checks:
  - id: obj1
    on_fail: "Outer composite."
    all_of:
      - type: command_matched
        optional: true
        pattern: 'pwd'
        on_fail: "Print the working directory first."
`
		report := validateFixture(t, fixturePack("", lvl))

		requireProblem(t, report, "nav-01", "checks[0].all_of[0]", ProblemError, "must not decide whether a level is passed")
		requireProblem(t, report, "nav-01", "checks[0].all_of[0].optional", ProblemError, "belongs on the objective, not on the check")
	})

	t.Run("nested two deep is still caught", func(t *testing.T) {
		lvl := defaultLevel()
		lvl.Checks = `checks:
  - id: obj1
    on_fail: "Outer composite."
    all_of:
      - on_fail: "Inner composite."
        any_of:
          - type: command_matched
            pattern: 'pwd'
            on_fail: "Print the working directory first."
`
		report := validateFixture(t, fixturePack("", lvl))
		requireProblem(t, report, "nav-01", "checks[0].all_of[0].any_of[0]", ProblemError, "must not decide whether a level is passed")
	})

	t.Run("a journal check under an optional objective is legal", func(t *testing.T) {
		lvl := defaultLevel()
		lvl.Objectives += "  - id: bonus\n    text: \"Did it the short way\"\n    optional: true\n"
		lvl.Checks += `  - id: bonus
    on_fail: "There is a shorter way."
    any_of:
      - type: command_matched
        pattern: 'pwd'
        on_fail: "Print the working directory first."
`
		report := validateFixture(t, fixturePack("", lvl))

		// Legal, but the same runtime gap as the top-level cases: the branch
		// still reads a journal nothing wires yet.
		requireProblem(t, report, "nav-01", "checks[1].any_of[0]", ProblemWarning, "reads the command journal")
		if !report.OK() {
			t.Errorf("a journal check inside an optional objective should be legal:\n%s", formatReport(report))
		}
	})

	t.Run("a composite mixing a state check and a journal check still gates", func(t *testing.T) {
		lvl := defaultLevel()
		lvl.Checks = `checks:
  - id: obj1
    on_fail: "Outer composite."
    all_of:
      - type: file_exists
        path: /home/learner/quest/answer.txt
        on_fail: "No answer.txt yet."
      - type: command_matched
        pattern: 'pwd'
        on_fail: "Print the working directory first."
`
		report := validateFixture(t, fixturePack("", lvl))

		// The state branch means the tree is a legitimate required check, so
		// the "nothing can fail this level" rule must stay quiet. The journal
		// branch is still refused, because it is inside a gating tree.
		requireProblem(t, report, "nav-01", "checks[0].all_of[1]", ProblemError, "must not decide whether a level is passed")
		requireNoProblem(t, report, "nav-01", "checks")
	})
}

// TestValidateEveryObjectiveOptionalIsALevelYouCannotFail is the latent
// defect issue #97 fixes: the required counter used to read the check's own
// optional flag, so a level whose only check sat under an optional objective
// counted as required at validation time while the engine already treated it
// as optional at runtime. The validator certified a level that could not be
// failed.
func TestValidateEveryObjectiveOptionalIsALevelYouCannotFail(t *testing.T) {
	t.Run("the only objective is optional", func(t *testing.T) {
		lvl := defaultLevel()
		lvl.Objectives = `objectives:
  - id: obj1
    text: "answer.txt holds the path"
    optional: true
`
		report := validateFixture(t, fixturePack("", lvl))
		requireProblem(t, report, "nav-01", "checks", ProblemError, "a level you cannot fail is not a level")
	})

	t.Run("one optional beside one required", func(t *testing.T) {
		lvl := defaultLevel()
		lvl.Objectives += "  - id: obj2\n    text: \"A bonus thing to check\"\n    optional: true\n"
		lvl.Checks += "  - id: obj2\n    type: file_exists\n    path: /home/learner/quest/bonus.txt\n    on_fail: \"No bonus.txt yet.\"\n"

		report := validateFixture(t, fixturePack("", lvl))
		requireNoProblem(t, report, "nav-01", "checks")
	})
}

// TestValidateBranchOnlyFieldsAreRefused covers the fields that mean nothing
// on a composition branch. Ignoring them silently is how an author ends up
// believing a branch is a bonus when the objective still gates on it.
func TestValidateBranchOnlyFieldsAreRefused(t *testing.T) {
	lvl := defaultLevel()
	lvl.Checks = `checks:
  - id: obj1
    on_fail: "Outer composite."
    any_of:
      - id: inner
        type: file_exists
        path: /home/learner/quest/a.txt
        on_fail: "No a.txt."
      - type: file_exists
        path: /home/learner/quest/b.txt
        on_fail: "No b.txt."
`
	report := validateFixture(t, fixturePack("", lvl))
	requireProblem(t, report, "nav-01", "checks[0].any_of[0].id", ProblemError, "means nothing on a composition branch")
	requireNoProblem(t, report, "nav-01", "checks[0].any_of[1].id")
}

// TestValidateWarnCheckNeedsNoObjective is the regression test for the second
// blocking finding: the document's own worked example uses a severity: warn
// anti-pattern check with no objective, and the correspondence rule must
// leave it alone. A warn result is a note, not a checklist line.
func TestValidateWarnCheckNeedsNoObjective(t *testing.T) {
	lvl := defaultLevel()
	lvl.Checks += `  - id: nocheat
    severity: warn
    type: command_not_matched
    pattern: '^\s*echo\s'
    on_fail: "Hardcoding works today, not at 3 AM."
`
	report := validateFixture(t, fixturePack("", lvl))

	requireNoProblem(t, report, "nav-01", "checks[1].id")
	if !report.OK() {
		t.Errorf("a severity: warn check with no objective should be legal:\n%s", formatReport(report))
	}
}

// TestValidateOptionalCheckStillNeedsAnObjective is now a doubly invalid
// shape: the check has no objective of the same id, and it also declares
// optional itself, which belongs on the objective and nowhere else. Keeping
// it as one fixture pins that the new rule does not accidentally exempt a
// check from the older correspondence rule, or the reverse.
func TestValidateOptionalCheckStillNeedsAnObjective(t *testing.T) {
	lvl := defaultLevel()
	lvl.Checks += `  - id: bonus
    optional: true
    type: command_matched
    pattern: '^\s*pwd\s*$'
    on_fail: "There is a shorter way."
`
	report := validateFixture(t, fixturePack("", lvl))
	requireProblem(t, report, "nav-01", "checks[1].id", ProblemError, "has no objective with the same id")
	requireProblem(t, report, "nav-01", "checks[1].optional", ProblemError, "belongs on the objective")
}

// TestValidateObjectiveCorrespondence covers both directions. An objective
// with no check is a promise nothing verifies; a check with no objective is
// work the learner was never told to do.
func TestValidateObjectiveCorrespondence(t *testing.T) {
	t.Run("objective without a check", func(t *testing.T) {
		lvl := defaultLevel()
		lvl.Objectives += "  - id: obj2\n    text: \"Something nothing verifies\"\n"

		report := validateFixture(t, fixturePack("", lvl))
		requireProblem(t, report, "nav-01", "objectives[1].id", ProblemError, "has no check with the same id")
	})

	t.Run("check without an objective", func(t *testing.T) {
		lvl := defaultLevel()
		lvl.Checks += "  - id: obj2\n    type: file_exists\n    path: /home/learner/quest/b.txt\n    on_fail: \"No b.txt.\"\n"

		report := validateFixture(t, fixturePack("", lvl))
		requireProblem(t, report, "nav-01", "checks[1].id", ProblemError, "has no objective with the same id")
	})

	t.Run("duplicate check id", func(t *testing.T) {
		lvl := defaultLevel()
		lvl.Checks += "  - id: obj1\n    type: file_exists\n    path: /home/learner/quest/b.txt\n    on_fail: \"No b.txt.\"\n"

		report := validateFixture(t, fixturePack("", lvl))
		requireProblem(t, report, "nav-01", "checks[1].id", ProblemError, "declared twice")
	})
}

// TestValidateAnUnmatchedCheckIDDefaultsToGating covers what the bonus lookup
// does for a check id with no corresponding objective: it must read as
// gating (the safe default), never panic on the miss, and an unnamed
// optional objective must not be confused with an unnamed check.
func TestValidateAnUnmatchedCheckIDDefaultsToGating(t *testing.T) {
	t.Run("a journal check with no objective is still refused", func(t *testing.T) {
		lvl := defaultLevel()
		lvl.Checks += "  - id: orphan\n    type: command_matched\n    pattern: '^\\s*pwd\\s*$'\n    on_fail: \"Use pwd.\"\n"

		report := validateFixture(t, fixturePack("", lvl))

		requireProblem(t, report, "nav-01", "checks[1]", ProblemError, "must not decide whether a level is passed")
		requireProblem(t, report, "nav-01", "checks[1].id", ProblemError, "has no objective with the same id")
	})

	t.Run("a state check with no objective still gets only the correspondence error", func(t *testing.T) {
		lvl := defaultLevel()
		lvl.Checks += "  - id: orphan\n    type: file_exists\n    path: /home/learner/quest/orphan.txt\n    on_fail: \"No orphan.txt yet.\"\n"

		report := validateFixture(t, fixturePack("", lvl))

		requireProblem(t, report, "nav-01", "checks[1].id", ProblemError, "has no objective with the same id")
		requireNoProblem(t, report, "nav-01", "checks[1]")
	})

	t.Run("an unnamed optional objective does not make an unnamed check non-gating", func(t *testing.T) {
		lvl := defaultLevel()
		lvl.Objectives += "  - id: \"\"\n    text: \"Placeholder\"\n    optional: true\n"
		lvl.Checks += "  - type: command_matched\n    pattern: '^\\s*pwd\\s*$'\n    on_fail: \"Use pwd.\"\n"

		report := validateFixture(t, fixturePack("", lvl))

		requireProblem(t, report, "nav-01", "checks[1].id", ProblemError, "must have an id")
		requireProblem(t, report, "nav-01", "checks[1]", ProblemError, "must not decide whether a level is passed")
	})
}

// TestValidateDuplicateCheckIDDoesNotConfuseGating pins that a duplicate
// check id does not let one copy borrow the other's gating verdict by
// accident of map construction. Both copies of bonus must read the same
// objective, and neither may gate, alongside the existing duplicate-id
// error.
func TestValidateDuplicateCheckIDDoesNotConfuseGating(t *testing.T) {
	lvl := defaultLevel()
	lvl.Objectives += "  - id: bonus\n    text: \"Did it in one command\"\n    optional: true\n"
	lvl.Checks += "  - id: bonus\n    type: command_matched\n    pattern: '^\\s*pwd\\s*$'\n    on_fail: \"There is a shorter way.\"\n"
	lvl.Checks += "  - id: bonus\n    type: command_matched\n    pattern: '^\\s*pwd\\s*$'\n    on_fail: \"There is a shorter way, still.\"\n"

	report := validateFixture(t, fixturePack("", lvl))

	requireProblem(t, report, "nav-01", "checks[2].id", ProblemError, "declared twice")
	for _, p := range report.Problems {
		if p.LevelID == "nav-01" && p.Level == ProblemError && (p.Field == "checks[1]" || p.Field == "checks[2]") {
			t.Errorf("a duplicate check id must not produce a gating error on either copy: %s", p.Message)
		}
	}
}

// TestValidateHintsAndSolution covers the two-hint minimum and the empty
// solution rule.
func TestValidateHintsAndSolution(t *testing.T) {
	t.Run("one hint is a cliff", func(t *testing.T) {
		lvl := defaultLevel()
		lvl.Hints = "hints:\n  - cost: 5\n    text: \"Only one.\"\n"

		report := validateFixture(t, fixturePack("", lvl))
		requireProblem(t, report, "nav-01", "hints", ProblemError, "at least two hints")
	})

	t.Run("a hint with no text and no reveal", func(t *testing.T) {
		lvl := defaultLevel()
		lvl.Hints = "hints:\n  - cost: 5\n    text: \"A real hint.\"\n  - cost: 10\n"

		report := validateFixture(t, fixturePack("", lvl))
		requireProblem(t, report, "nav-01", "hints[1]", ProblemError, "reveal_solution")
	})

	t.Run("empty solution", func(t *testing.T) {
		lvl := defaultLevel()
		lvl.Solution = "solution: \"   \"\n"

		report := validateFixture(t, fixturePack("", lvl))
		requireProblem(t, report, "nav-01", "solution", ProblemError, "golden test runs it")
	})
}

// TestValidateUnknownCheckTypeAndParams covers what the registry answers for.
func TestValidateUnknownCheckTypeAndParams(t *testing.T) {
	t.Run("unknown type", func(t *testing.T) {
		lvl := defaultLevel()
		lvl.Checks = "checks:\n  - id: obj1\n    type: file_smells\n    on_fail: \"Not a type.\"\n"

		report := validateFixture(t, fixturePack("", lvl))
		requireProblem(t, report, "nav-01", "checks[0].type", ProblemError, `unknown check type "file_smells"`)
	})

	t.Run("malformed params", func(t *testing.T) {
		pack := loadFixture(t, fixturePack("", defaultLevel()))
		tc := newFakeTypeChecker()
		tc.paramError["file_exists"] = errFakeParam{}

		report := Validate(pack, tc)
		requireProblem(t, report, "nav-01", "checks[0]", ProblemError, "param \"path\" must be a string")
	})
}

// errFakeParam is the parameter error the fake registry returns.
type errFakeParam struct{}

func (errFakeParam) Error() string { return `param "path" must be a string, got int` }

// TestValidateActCoverage covers the warning that keeps a half-written pack
// usable, and the error that keeps a level reachable.
func TestValidateActCoverage(t *testing.T) {
	t.Run("planned level with no file is a warning", func(t *testing.T) {
		acts := "  - id: act1\n    title: \"Act One\"\n    levels: [nav-01, nav-02]\n"
		report := validateFixture(t, fixturePack(acts, defaultLevel()))

		requireProblem(t, report, "", "acts[0].levels[1]", ProblemWarning, `level "nav-02" has no file`)
		if !report.OK() {
			t.Errorf("a warning must not fail validation:\n%s", formatReport(report))
		}
	})

	t.Run("level no act lists is an error", func(t *testing.T) {
		acts := "  - id: act1\n    title: \"Act One\"\n    levels: [nav-02]\n"
		report := validateFixture(t, fixturePack(acts, defaultLevel()))

		requireProblem(t, report, "nav-01", "id", ProblemError, "no act in pack.yaml lists")
	})

	t.Run("level naming an act that does not exist", func(t *testing.T) {
		lvl := defaultLevel()
		lvl.Act = "act9"

		report := validateFixture(t, fixturePack("", lvl))
		requireProblem(t, report, "nav-01", "act", ProblemError, `act "act9" is not declared in pack.yaml`)
	})
}

// TestValidatePrerequisites covers the DAG rules.
func TestValidatePrerequisites(t *testing.T) {
	t.Run("dangling prerequisite is an error", func(t *testing.T) {
		lvl := defaultLevel()
		lvl.Extra = "prerequisites: [nav-00]\n"

		report := validateFixture(t, fixturePack("", lvl))
		requireProblem(t, report, "nav-01", "prerequisites[0]", ProblemError, "is not a level in this pack")
	})

	t.Run("planned but unwritten prerequisite is a warning", func(t *testing.T) {
		lvl := defaultLevel()
		lvl.Extra = "prerequisites: [nav-02]\n"

		acts := "  - id: act1\n    title: \"Act One\"\n    levels: [nav-01, nav-02]\n"
		report := validateFixture(t, fixturePack(acts, lvl))

		requireProblem(t, report, "nav-01", "prerequisites[0]", ProblemWarning, "has no level file yet")
	})

	t.Run("self prerequisite", func(t *testing.T) {
		lvl := defaultLevel()
		lvl.Extra = "prerequisites: [nav-01]\n"

		report := validateFixture(t, fixturePack("", lvl))
		requireProblem(t, report, "nav-01", "prerequisites[0]", ProblemError, "cannot be its own prerequisite")
	})

	t.Run("cycle between two levels", func(t *testing.T) {
		first := defaultLevel()
		first.Extra = "prerequisites: [nav-02]\n"
		second := defaultLevel()
		second.ID = "nav-02"
		second.Extra = "prerequisites: [nav-01]\n"

		acts := "  - id: act1\n    title: \"Act One\"\n    levels: [nav-01, nav-02]\n"
		report := validateFixture(t, fixturePack(acts, first, second))

		var found bool
		for _, p := range report.Problems {
			if strings.Contains(p.Message, "cycle") {
				found = true
			}
		}
		if !found {
			t.Errorf("a prerequisite cycle was not reported:\n%s", formatReport(report))
		}
	})
}

// TestValidateReportIsStable asserts two runs over the same pack produce
// byte-identical output. A report that reorders itself between runs cannot be
// diffed, and map iteration order is what would otherwise leak in.
func TestValidateReportIsStable(t *testing.T) {
	lvl := defaultLevel()
	lvl.OmitFields = []string{"title", "concepts", "briefing"}
	fsys := fixturePack("", lvl)

	first := render(validateFixture(t, fsys))
	for i := 0; i < 20; i++ {
		if got := render(validateFixture(t, fsys)); got != first {
			t.Fatalf("validation output is not stable across runs\nfirst:\n%s\nlater:\n%s", first, got)
		}
	}
}

func render(report Report) string {
	var b strings.Builder
	for _, p := range report.Problems {
		b.WriteString(p.Error())
		b.WriteString("\n")
	}
	return b.String()
}
