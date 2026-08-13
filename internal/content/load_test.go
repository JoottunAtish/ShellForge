package content

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
)

// workedExample is the complete level from docs/LEVEL-FORMAT.md section 6,
// verbatim. It is the round trip fixture: if the document and the loader ever
// disagree about a field, this is where it shows up.
const workedExample = `id: pipe-05
version: 1
title: "The Log Sifter"
act: act3
boss: true
difficulty: 4
xp: 150
par_commands: 6
estimated_minutes: 15
concepts: [pipes, grep, wc, redirection]
prerequisites: [pipe-04]

briefing: |
  ## 02:41 - Billing service
  Errors are pouring in. Nobody knows how many.

objectives:
  - id: obj1
    text: "report.txt holds the total ERROR count"
  - id: obj3
    text: "Counted with a single pipeline"
    optional: true

setup:
  root: /home/learner/quest
  files:
    - { path: logs/app-1.log, source: assets/app-1.log }
  script: chown -R learner:learner .

checks:
  - id: obj1
    type: file_content
    path: /home/learner/quest/report.txt
    match: trimmed_equals
    value: "147"
    on_fail: "report.txt is missing or the number is off."

  - id: obj3
    optional: true
    type: command_matched
    pattern: 'grep[^|]*\|\s*wc\s+-l'
    scope: level
    on_fail: "There is a one-liner hiding in this."

hints:
  - cost: 5
    text: "grep can take a whole directory if you tell it to look inside."
  - cost: 40
    reveal_solution: true

solution: |
  grep -ri ERROR ~/quest/logs/ | wc -l > ~/quest/report.txt

teardown: {}
tags: [text-processing, must-know]
`

// TestWorkedExampleRoundTrips decodes the document's own complete example and
// asserts every field arrived. A field that decodes to its zero value here is
// a field the loader silently dropped.
func TestWorkedExampleRoundTrips(t *testing.T) {
	var lvl Level
	if err := Unmarshal("pipe-05.yaml", []byte(workedExample), &lvl); err != nil {
		t.Fatalf("the worked example from docs/LEVEL-FORMAT.md section 6 does not load: %v", err)
	}

	if lvl.ID != "pipe-05" || lvl.Title != "The Log Sifter" || lvl.Act != "act3" {
		t.Errorf("identity fields wrong: %+v", lvl)
	}
	if lvl.Version != 1 || lvl.Difficulty != 4 || lvl.XP != 150 {
		t.Errorf("metadata wrong: version=%d difficulty=%d xp=%d", lvl.Version, lvl.Difficulty, lvl.XP)
	}
	if lvl.ParCommands != 6 || lvl.EstimatedMinutes != 15 || !lvl.Boss {
		t.Errorf("par=%d minutes=%d boss=%v", lvl.ParCommands, lvl.EstimatedMinutes, lvl.Boss)
	}
	if len(lvl.Concepts) != 4 || lvl.Concepts[0] != "pipes" {
		t.Errorf("concepts = %v", lvl.Concepts)
	}
	if len(lvl.Prerequisites) != 1 || lvl.Prerequisites[0] != "pipe-04" {
		t.Errorf("prerequisites = %v", lvl.Prerequisites)
	}
	if len(lvl.Tags) != 2 || lvl.Tags[0] != "text-processing" {
		t.Errorf("tags = %v", lvl.Tags)
	}
	if !strings.Contains(lvl.Briefing, "Billing service") {
		t.Errorf("briefing = %q", lvl.Briefing)
	}
	if !strings.Contains(lvl.Solution, "grep -ri ERROR") {
		t.Errorf("solution = %q", lvl.Solution)
	}
	if lvl.Setup.Root != "/home/learner/quest" || len(lvl.Setup.Files) != 1 {
		t.Errorf("setup = %+v", lvl.Setup)
	}
	if lvl.Setup.Files[0].Source != "assets/app-1.log" {
		t.Errorf("setup.files[0] = %+v", lvl.Setup.Files[0])
	}

	if len(lvl.Objectives) != 2 || !lvl.Objectives[1].Optional {
		t.Errorf("objectives = %+v", lvl.Objectives)
	}
	if len(lvl.Hints) != 2 || lvl.Hints[0].Cost != 5 || !lvl.Hints[1].RevealSolution {
		t.Errorf("hints = %+v", lvl.Hints)
	}

	if len(lvl.Checks) != 2 {
		t.Fatalf("checks = %+v", lvl.Checks)
	}
	first := lvl.Checks[0]
	if first.ID != "obj1" || first.Type != "file_content" || first.OnFail == "" {
		t.Errorf("checks[0] common fields = %+v", first)
	}
	for key, want := range map[string]any{"path": "/home/learner/quest/report.txt", "match": "trimmed_equals", "value": "147"} {
		if got := first.Params[key]; got != want {
			t.Errorf("checks[0].Params[%q] = %v, want %v", key, got, want)
		}
	}
	if first.Line == 0 {
		t.Error("checks[0] has no line number, so a validation problem cannot name a place")
	}
	if !lvl.Checks[1].Optional || lvl.Checks[1].Params["scope"] != "level" {
		t.Errorf("checks[1] = %+v", lvl.Checks[1])
	}
}

// TestCheckParamsExcludeCommonFields proves the split: a common field never
// leaks into Params, where a check factory would then see a parameter it does
// not recognize.
func TestCheckParamsExcludeCommonFields(t *testing.T) {
	var lvl Level
	if err := Unmarshal("pipe-05.yaml", []byte(workedExample), &lvl); err != nil {
		t.Fatalf("load: %v", err)
	}
	for key := range checkReservedKeys {
		if _, leaked := lvl.Checks[0].Params[key]; leaked {
			t.Errorf("common field %q leaked into Params", key)
		}
	}
}

// TestCompositionNodesDecode covers a nested tree, since composition is where
// a hand-rolled decoder is most likely to be wrong.
func TestCompositionNodesDecode(t *testing.T) {
	const src = `id: pipe-06
checks:
  - id: obj1
    on_fail: "Outer."
    any_of:
      - type: file_content
        path: /tmp/out
        on_fail: "First branch."
      - all_of:
          - type: file_exists
            path: /tmp/a
            on_fail: "Inner one."
          - not:
              type: file_exists
              path: /tmp/b
              on_fail: "Innermost."
            on_fail: "Inner two."
        on_fail: "Second branch."
`
	var lvl Level
	if err := Unmarshal("pipe-06.yaml", []byte(src), &lvl); err != nil {
		t.Fatalf("load: %v", err)
	}

	outer := lvl.Checks[0]
	if !outer.IsComposite() || len(outer.AnyOf) != 2 {
		t.Fatalf("outer = %+v", outer)
	}
	if outer.OnFail != "Outer." || outer.Type != "" {
		t.Errorf("a composite must keep its own on_fail and declare no type: %+v", outer)
	}

	second := outer.AnyOf[1]
	if len(second.AllOf) != 2 || second.OnFail != "Second branch." {
		t.Fatalf("second branch = %+v", second)
	}
	notNode := second.AllOf[1]
	if notNode.Not == nil || notNode.Not.Params["path"] != "/tmp/b" {
		t.Fatalf("not node = %+v", notNode)
	}
	if len(outer.Branches()) != 2 || len(notNode.Branches()) != 1 {
		t.Errorf("Branches did not return the composition children")
	}
}

// TestStrictDecodeRejectsUnknownFields is the rule that turns a typo into a
// rejection rather than a level that quietly does the wrong thing.
func TestStrictDecodeRejectsUnknownFields(t *testing.T) {
	const src = "id: nav-01\ntitel: \"typo\"\n"

	var lvl Level
	err := Unmarshal("nav-01.yaml", []byte(src), &lvl)
	if err == nil {
		t.Fatal("an unknown top-level field was accepted, so a typo would be silently dropped")
	}
	if !strings.Contains(err.Error(), "nav-01.yaml") {
		t.Errorf("the error must name the file, got %q", err)
	}
}

// TestMalformedYAMLIsRejected is the table of ways a level file can be wrong
// at the parser level rather than the schema level.
func TestMalformedYAMLIsRejected(t *testing.T) {
	cases := map[string]string{
		"unknown field":            "id: nav-01\nnot_a_field: 1\n",
		"difficulty wrong type":    "id: nav-01\ndifficulty: hard\n",
		"xp as a string":           "id: nav-01\nxp: \"forty\"\n",
		"duplicate key":            "id: nav-01\nid: nav-02\n",
		"truncated file":           "id: nav-01\nchecks:\n  - id: obj1\n    type: file_exists\n    on_fail: \"unterminated\n",
		"checks entry is a scalar": "id: nav-01\nchecks:\n  - just-a-string\n",
		"checks is a scalar":       "id: nav-01\nchecks: nope\n",
		"tab indentation":          "id: nav-01\nsetup:\n\troot: /home/learner/quest\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			var lvl Level
			if err := Unmarshal("bad.yaml", []byte(src), &lvl); err == nil {
				t.Fatalf("malformed YAML was accepted: %q", src)
			}
		})
	}
}

// TestOptionalFieldsOmittedLoadAsZero proves a minimal level is legal at the
// parser level, so that the validator is the only thing deciding what a level
// must declare.
func TestOptionalFieldsOmittedLoadAsZero(t *testing.T) {
	const src = "id: nav-01\ntitle: \"Minimal\"\n"

	var lvl Level
	if err := Unmarshal("nav-01.yaml", []byte(src), &lvl); err != nil {
		t.Fatalf("a level with only required-by-nothing fields failed to parse: %v", err)
	}
	if lvl.ParCommands != 0 || lvl.Boss || lvl.Checks != nil || lvl.Teardown.Script != "" {
		t.Errorf("omitted fields did not take zero values: %+v", lvl)
	}
}

// TestLoadPackWithNoLevelsIsClean is the repository's own state while the
// pack is being filled in. Refusing it would mean a pack could not be
// validated until it was finished.
func TestLoadPackWithNoLevelsIsClean(t *testing.T) {
	fsys := fixturePack("")
	delete(fsys, "levels/01-nav-01.yaml")

	pack, err := LoadPack(fsys, ".")
	if err != nil {
		t.Fatalf("a pack with no level files should load: %v", err)
	}
	if len(pack.Levels) != 0 {
		t.Errorf("levels = %v, want none", pack.Levels)
	}
}

// TestLoadPackSkipsNonLevelFiles keeps the levels/README.md that ships in the
// real pack from being read as a level.
func TestLoadPackSkipsNonLevelFiles(t *testing.T) {
	fsys := fixturePack("", defaultLevel())
	fsys["levels/README.md"] = &fstest.MapFile{Data: []byte("# Levels\n")}

	pack, err := LoadPack(fsys, ".")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(pack.Levels) != 1 {
		t.Errorf("loaded %d levels, want 1", len(pack.Levels))
	}
}

// TestLoadPackSetsSourceFile proves every validation message can name a file.
func TestLoadPackSetsSourceFile(t *testing.T) {
	pack := loadFixture(t, fixturePack("", defaultLevel()))
	if got := pack.Levels[0].SourceFile; got != "levels/01-nav-01.yaml" {
		t.Errorf("SourceFile = %q, want the pack-relative path", got)
	}
}

// TestLoadPackRefusesAFutureContentAPI keeps a pack written for a newer
// schema from being read with today's rules.
func TestLoadPackRefusesAFutureContentAPI(t *testing.T) {
	fsys := fixturePack("", defaultLevel())
	fsys["pack.yaml"] = &fstest.MapFile{
		Data: []byte(strings.Replace(string(fsys["pack.yaml"].Data), "content_api: 1", "content_api: 2", 1)),
	}

	_, err := LoadPack(fsys, ".")
	if err == nil {
		t.Fatal("a pack from a newer content_api was accepted")
	}
	if !errors.Is(err, ErrUnsupportedContentAPI) {
		t.Errorf("error should be ErrUnsupportedContentAPI, got %v", err)
	}
	assertUserFacing(t, err)
}

// TestLoadPackFailuresAreUserFacing covers non-negotiable 6: no bare Go error
// reaches the terminal.
func TestLoadPackFailuresAreUserFacing(t *testing.T) {
	t.Run("missing pack.yaml", func(t *testing.T) {
		_, err := LoadPack(fstest.MapFS{}, ".")
		if err == nil {
			t.Fatal("a directory with no pack.yaml was accepted")
		}
		assertUserFacing(t, err)
	})

	t.Run("unparseable level", func(t *testing.T) {
		fsys := fixturePack("", defaultLevel())
		fsys["levels/01-nav-01.yaml"] = &fstest.MapFile{Data: []byte("id: nav-01\nnot_a_field: 1\n")}

		_, err := LoadPack(fsys, ".")
		if err == nil {
			t.Fatal("an unparseable level was accepted")
		}
		assertUserFacing(t, err)
		if !strings.Contains(err.Error(), "levels/01-nav-01.yaml") {
			t.Errorf("the error must name the level file, got %q", err)
		}
	})
}

// assertUserFacing asserts an error carries a remediation, so a learner is
// never shown a dead end.
func assertUserFacing(t *testing.T, err error) {
	t.Helper()

	var uxErr *ux.Error
	if !errors.As(err, &uxErr) {
		t.Fatalf("error is not a *ux.Error, so it would reach the terminal bare: %v", err)
	}
	if strings.TrimSpace(uxErr.Remediation) == "" {
		t.Error("a user-facing error must carry a remediation naming the next command to run")
	}
}

// TestEmbeddedPackLoads proves the pack compiled into the binary is readable
// with no filesystem access and no working directory.
func TestEmbeddedPackLoads(t *testing.T) {
	pack, err := Embedded()
	if err != nil {
		t.Fatalf("the embedded pack does not load: %v", err)
	}
	if pack.ID != "core-linux-basics" {
		t.Errorf("pack id = %q", pack.ID)
	}
	if len(pack.Acts) == 0 {
		t.Error("the embedded pack declares no acts")
	}
}

// TestEmbeddedPackValidates is the acceptance criterion that the shipped pack
// passes its own validator, with warnings for the levels not yet written.
func TestEmbeddedPackValidates(t *testing.T) {
	pack, err := Embedded()
	if err != nil {
		t.Fatalf("load the embedded pack: %v", err)
	}

	report := Validate(pack, newFakeTypeChecker())
	for _, p := range report.Problems {
		if p.Level == ProblemError {
			t.Errorf("the shipped pack has a validation error: %s", p.Error())
		}
	}
}

// TestPackOrder covers act order, the prerequisite ordering inside an act,
// and the cycle error.
func TestPackOrder(t *testing.T) {
	t.Run("acts in pack order, prerequisites first", func(t *testing.T) {
		second := defaultLevel()
		second.ID = "nav-02"
		second.Extra = "prerequisites: [nav-03]\n"
		third := defaultLevel()
		third.ID = "nav-03"

		acts := "  - id: act1\n    title: \"Act One\"\n    levels: [nav-01, nav-02, nav-03]\n"
		pack := loadFixture(t, fixturePack(acts, defaultLevel(), second, third))

		order, err := pack.Order()
		if err != nil {
			t.Fatalf("Order: %v", err)
		}
		if strings.Join(order, ",") != "nav-01,nav-03,nav-02" {
			t.Errorf("order = %v, want nav-03 pulled ahead of the level that requires it", order)
		}
	})

	t.Run("a planned level with no file is skipped", func(t *testing.T) {
		acts := "  - id: act1\n    title: \"Act One\"\n    levels: [nav-01, nav-02]\n"
		pack := loadFixture(t, fixturePack(acts, defaultLevel()))

		order, err := pack.Order()
		if err != nil {
			t.Fatalf("Order: %v", err)
		}
		if strings.Join(order, ",") != "nav-01" {
			t.Errorf("order = %v, want only the level that has a file", order)
		}
	})

	t.Run("a cycle is an error naming the levels", func(t *testing.T) {
		first := defaultLevel()
		first.Extra = "prerequisites: [nav-02]\n"
		second := defaultLevel()
		second.ID = "nav-02"
		second.Extra = "prerequisites: [nav-01]\n"

		acts := "  - id: act1\n    title: \"Act One\"\n    levels: [nav-01, nav-02]\n"
		pack := loadFixture(t, fixturePack(acts, first, second))

		_, err := pack.Order()
		if err == nil {
			t.Fatal("a prerequisite cycle was accepted")
		}
		for _, id := range []string{"nav-01", "nav-02"} {
			if !strings.Contains(err.Error(), id) {
				t.Errorf("the cycle error should name %q, got %q", id, err)
			}
		}
	})
}

// TestPackLevelLookup covers the accessor the run wiring uses.
func TestPackLevelLookup(t *testing.T) {
	pack := loadFixture(t, fixturePack("", defaultLevel()))

	if lvl, ok := pack.Level("nav-01"); !ok || lvl.Title != "First Contact" {
		t.Errorf("Level(nav-01) = %+v, %v", lvl, ok)
	}
	if _, ok := pack.Level("nope"); ok {
		t.Error("Level returned a level for an id that does not exist")
	}
}
