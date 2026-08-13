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

			requireProblem(t, report, "nav-01", "checks[0]", ProblemError, "optional: true or severity: warn")
			// It is also the only check, so the level has nothing that can
			// fail it. Both rules must fire, not one masking the other.
			requireProblem(t, report, "nav-01", "checks", ProblemError, "a level you cannot fail is not a level")
		})

		t.Run(checkType+" optional is accepted", func(t *testing.T) {
			lvl := defaultLevel()
			lvl.Objectives += "  - id: bonus\n    text: \"Did it in one command\"\n    optional: true\n"
			lvl.Checks += "  - id: bonus\n    optional: true\n    type: " + checkType +
				"\n    pattern: '^\\s*pwd\\s*$'\n    on_fail: \"There is a shorter way.\"\n"

			report := validateFixture(t, fixturePack("", lvl))

			requireNoProblem(t, report, "nav-01", "checks[1]")
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

			requireNoProblem(t, report, "nav-01", "checks[1]")
			if !report.OK() {
				t.Errorf("a severity: warn journal check should be legal:\n%s", formatReport(report))
			}
		})
	}
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
