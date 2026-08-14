package content

import (
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
)

// This file builds the fixture packs the loader and validator tests run
// against.
//
// The fixtures are assembled in memory rather than committed as a directory
// per rule. A rule's fixture is then one readable block in the test that
// asserts it, instead of a pair of files two directories away, and the shared
// parts cannot drift between twenty copies. The two rules that genuinely need
// a real filesystem, symlink containment for a pack directory and for an
// asset, use t.TempDir instead and live in validate_symlink_test.go.

// packYAML is a minimal, valid pack.yaml. acts is substituted in.
const packYAML = `id: fixture-pack
name: "Fixture Pack"
version: 0.1.0
content_api: 1
author: "Test"
license: MIT
description: "A pack that exists to be validated."
acts:
%s`

// defaultActs declares one act holding one level.
const defaultActs = `  - id: act1
    title: "Act One"
    levels: [nav-01]
`

// levelFixture is a valid level with every part addressable, so a test can
// break exactly one rule and leave the rest correct. A fixture that breaks
// two rules at once cannot prove which one the validator caught.
type levelFixture struct {
	ID         string
	Act        string
	Extra      string // extra or replacement top-level keys, already indented
	Setup      string
	Objectives string
	Checks     string
	Hints      string
	Solution   string
	OmitFields []string // top-level keys to leave out entirely
}

// defaultLevel returns a fixture that validates clean.
func defaultLevel() levelFixture {
	return levelFixture{
		ID:  "nav-01",
		Act: "act1",
		Setup: `setup:
  root: /home/learner/quest
`,
		Objectives: `objectives:
  - id: obj1
    text: "answer.txt holds the path"
`,
		Checks: `checks:
  - id: obj1
    type: file_exists
    path: /home/learner/quest/answer.txt
    on_fail: "I don't see answer.txt yet. List the folder and look again."
`,
		Hints: `hints:
  - cost: 5
    text: "The prompt shows an abbreviation, not the full path."
  - cost: 40
    reveal_solution: true
`,
		Solution: `solution: |
  pwd > ~/quest/answer.txt
`,
	}
}

// YAML renders the fixture as a level file.
func (f levelFixture) YAML() string {
	omit := map[string]bool{}
	for _, k := range f.OmitFields {
		omit[k] = true
	}

	var b strings.Builder
	write := func(key, text string) {
		if !omit[key] {
			b.WriteString(text)
		}
	}

	write("id", fmt.Sprintf("id: %s\n", f.ID))
	write("version", "version: 1\n")
	write("title", "title: \"First Contact\"\n")
	write("act", fmt.Sprintf("act: %s\n", f.Act))
	write("difficulty", "difficulty: 1\n")
	write("xp", "xp: 40\n")
	write("concepts", "concepts: [pwd]\n")
	write("briefing", "briefing: |\n  Find out where you are.\n")
	write("objectives", f.Objectives)
	write("setup", f.Setup)
	write("checks", f.Checks)
	write("hints", f.Hints)
	write("solution", f.Solution)
	b.WriteString(f.Extra)

	return b.String()
}

// fixturePack builds an in-memory pack from a set of level fixtures.
func fixturePack(acts string, levels ...levelFixture) fstest.MapFS {
	if acts == "" {
		acts = defaultActs
	}
	fsys := fstest.MapFS{
		"pack.yaml":          &fstest.MapFile{Data: []byte(fmt.Sprintf(packYAML, acts))},
		"assets/README.md":   &fstest.MapFile{Data: []byte("# Assets\n")},
		"assets/welcome.txt": &fstest.MapFile{Data: []byte("Kofi was here.\n")},
	}
	for i, lvl := range levels {
		name := fmt.Sprintf("levels/%02d-%s.yaml", i+1, lvl.ID)
		fsys[name] = &fstest.MapFile{Data: []byte(lvl.YAML())}
	}
	return fsys
}

// loadFixture loads a fixture pack, failing the test if it does not parse.
// Every validator test needs a pack that loaded, so a parse failure is a
// broken fixture rather than a result.
func loadFixture(t *testing.T, fsys fstest.MapFS) *Pack {
	t.Helper()
	pack, err := LoadPack(fsys, ".")
	if err != nil {
		t.Fatalf("fixture pack failed to load: %v", err)
	}
	return pack
}

// fakeTypeChecker stands in for internal/verify's registry, so that this
// package's tests never depend on which check types happen to be registered.
type fakeTypeChecker struct {
	known      map[string]bool
	paramError map[string]error
}

// newFakeTypeChecker knows the check catalogue in docs/LEVEL-FORMAT.md
// section 3.
//
// It is a copy of the type names, not a reference to the registry, because
// this package must not import internal/verify: both are layer 3 and neither
// may depend on the other. The copy is safe because it proves nothing on its
// own. What proves the shipped pack uses real, registered types with
// well-formed parameters is TestEmbeddedPackValidatesAgainstTheRealRegistry
// in cmd/shellforge, which is the layer where the two halves legitimately
// meet.
func newFakeTypeChecker() *fakeTypeChecker {
	return &fakeTypeChecker{
		known: map[string]bool{
			"file_exists":         true,
			"file_absent":         true,
			"file_content":        true,
			"dir_exists":          true,
			"dir_tree":            true,
			"file_mode":           true,
			"file_owner":          true,
			"symlink_target":      true,
			"env_var":             true,
			"cwd_is":              true,
			"process_running":     true,
			"command_matched":     true,
			"command_not_matched": true,
			"script":              true,
		},
		paramError: map[string]error{},
	}
}

func (f *fakeTypeChecker) Known(typeName string) bool { return f.known[typeName] }

func (f *fakeTypeChecker) ValidateParams(typeName string, _ map[string]any) error {
	return f.paramError[typeName]
}

// findProblem returns the first problem matching field and containing want in
// its message, and whether one was found. Tests assert on exact fields rather
// than on a count, so that a rule firing for the wrong reason still fails.
func findProblem(report Report, levelID, field string) (Problem, bool) {
	for _, p := range report.Problems {
		if p.LevelID == levelID && p.Field == field {
			return p, true
		}
	}
	return Problem{}, false
}

// requireProblem asserts that exactly the expected problem is present.
func requireProblem(t *testing.T, report Report, levelID, field string, level ProblemLevel, messageContains string) {
	t.Helper()

	p, ok := findProblem(report, levelID, field)
	if !ok {
		t.Fatalf("no problem reported for level %q field %q\nreport:\n%s", levelID, field, formatReport(report))
	}
	if p.Level != level {
		t.Errorf("problem for %q %q has level %q, want %q", levelID, field, p.Level, level)
	}
	if messageContains != "" && !strings.Contains(p.Message, messageContains) {
		t.Errorf("problem for %q %q message = %q, want it to contain %q", levelID, field, p.Message, messageContains)
	}
	if p.File == "" {
		t.Errorf("problem for %q %q has no file, so an author cannot find it", levelID, field)
	}
}

// requireNoProblem asserts a field produced nothing, which is what catches an
// over-eager rule.
func requireNoProblem(t *testing.T, report Report, levelID, field string) {
	t.Helper()
	if p, ok := findProblem(report, levelID, field); ok {
		t.Errorf("unexpected problem for %q %q: %s", levelID, field, p.Message)
	}
}

// formatReport renders a whole report for a failure message.
func formatReport(report Report) string {
	if len(report.Problems) == 0 {
		return "  (no problems)"
	}
	var b strings.Builder
	for _, p := range report.Problems {
		fmt.Fprintf(&b, "  %s: %s\n", p.Level, p.Error())
	}
	return b.String()
}
