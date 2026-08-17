package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/verify"
)

// shippedPack is the pack that ships in the binary, as a path relative to
// this package.
const shippedPack = "../../packs/core-linux-basics"

// TestEmbeddedPackValidatesAgainstTheRealRegistry is the assertion that the
// levels in the repository are real: every check names a type that is
// actually registered, and every parameter set builds.
//
// It lives here rather than in internal/content because this is the layer
// where the two halves legitimately meet. internal/content declares the
// TypeChecker interface and internal/verify implements it, and neither may
// import the other, so a test inside either one can only ever run against a
// fake. A fake proves the validator works; only this proves the content does.
func TestEmbeddedPackValidatesAgainstTheRealRegistry(t *testing.T) {
	pack, err := content.Embedded()
	if err != nil {
		t.Fatalf("load the embedded pack: %v", err)
	}

	report := content.Validate(pack, verify.TypeChecker{})
	for _, p := range report.Problems {
		if p.Level == content.ProblemError {
			t.Errorf("the shipped pack has a validation error: %s", p.Error())
		}
	}
}

// TestEmbeddedPackHasTheDayTwoLevels pins the eight levels the first Day 2
// exit criterion names, so one cannot quietly disappear.
func TestEmbeddedPackHasTheDayTwoLevels(t *testing.T) {
	pack, err := content.Embedded()
	if err != nil {
		t.Fatalf("load the embedded pack: %v", err)
	}

	want := []string{
		"nav-01", "nav-02", "nav-03", "nav-04",
		"files-01", "files-02", "files-03", "files-04",
	}
	for _, id := range want {
		lvl, ok := pack.Level(id)
		if !ok {
			t.Errorf("level %q is not in the shipped pack", id)
			continue
		}
		if lvl.Solution == "" || len(lvl.Hints) < 2 || len(lvl.Checks) == 0 {
			t.Errorf("level %q is present but incomplete: %d hints, %d checks", id, len(lvl.Hints), len(lvl.Checks))
		}
	}
}

// TestValidateShippedPackExitsZero is the Day 2 exit criterion the CLI has to
// demonstrate: the pack in the repository validates today, warnings and all.
func TestValidateShippedPackExitsZero(t *testing.T) {
	var out bytes.Buffer
	if err := runValidate(&out, shippedPack, false); err != nil {
		t.Fatalf("the shipped pack does not validate:\n%s\nerror: %v", out.String(), err)
	}
	if !strings.Contains(out.String(), "valid") {
		t.Errorf("output should say the pack is valid, got:\n%s", out.String())
	}
}

// TestValidateWarningsDoNotFail proves the pack's normal half-written state
// is reported without failing the run. pack.yaml lists all twenty five levels
// and only eight are written.
func TestValidateWarningsDoNotFail(t *testing.T) {
	var out bytes.Buffer
	if err := runValidate(&out, shippedPack, false); err != nil {
		t.Fatalf("warnings must not fail validation: %v", err)
	}
	if !strings.Contains(out.String(), "warning:") {
		t.Errorf("the shipped pack should report warnings for the unwritten levels, got:\n%s", out.String())
	}
}

// TestValidateJSONShape covers the --json contract.
func TestValidateJSONShape(t *testing.T) {
	var out bytes.Buffer
	if err := runValidate(&out, shippedPack, true); err != nil {
		t.Fatalf("runValidate: %v", err)
	}

	var doc validateReport
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("--json did not produce valid JSON: %v\n%s", err, out.String())
	}
	if !doc.OK {
		t.Errorf("ok = false for a pack that validates")
	}
	if doc.Pack == "" {
		t.Error("the JSON report does not name the pack")
	}
	if len(doc.Problems) == 0 {
		t.Error("the JSON report should carry the warnings for the unwritten levels")
	}
	for _, p := range doc.Problems {
		if p.File == "" || p.Message == "" {
			t.Errorf("a JSON problem is missing a file or a message: %+v", p)
		}
	}
}

// TestValidateJSONProblemsIsNeverNull keeps a consumer from having to handle
// both an empty list and a null.
func TestValidateJSONProblemsIsNeverNull(t *testing.T) {
	dir := writeMinimalPack(t)

	var out bytes.Buffer
	if err := runValidate(&out, dir, true); err != nil {
		t.Fatalf("runValidate: %v", err)
	}
	if !strings.Contains(out.String(), `"problems": []`) {
		t.Errorf("an empty problem list should render as [], got:\n%s", out.String())
	}
}

// TestValidateFailureModes covers each way the exit criterion says the
// command must exit non-zero, one broken pack per mode.
func TestValidateFailureModes(t *testing.T) {
	cases := map[string]struct {
		mutate func(t *testing.T, dir string)
		expect string
	}{
		"empty on_fail": {
			mutate: func(t *testing.T, dir string) {
				replaceInLevel(t, dir, `on_fail: "No answer.txt yet."`, `on_fail: ""`)
			},
			expect: "on_fail",
		},
		"unknown check type": {
			mutate: func(t *testing.T, dir string) {
				replaceInLevel(t, dir, "type: file_exists", "type: file_smells")
			},
			expect: "unknown check type",
		},
		"objective without a check": {
			mutate: func(t *testing.T, dir string) {
				appendToLevel(t, dir, "  - id: ghost\n    text: \"Nothing verifies this\"\n")
			},
			expect: "no check with the same id",
		},
		"setup root outside the learner home": {
			mutate: func(t *testing.T, dir string) {
				replaceInLevel(t, dir, "root: /home/learner/quest", "root: /home/learner")
			},
			expect: "strictly inside",
		},
		"journal check gating a pass": {
			mutate: func(t *testing.T, dir string) {
				replaceInLevel(t, dir,
					"    type: file_exists\n    path: /home/learner/quest/answer.txt\n",
					"    type: command_matched\n    pattern: 'pwd'\n")
			},
			expect: "optional: true on this check's objective",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := writeMinimalPack(t)
			tc.mutate(t, dir)

			var out bytes.Buffer
			err := runValidate(&out, dir, false)
			if err == nil {
				t.Fatalf("a pack breaking %q was accepted:\n%s", name, out.String())
			}
			assertUserFacing(t, err)
			if !strings.Contains(out.String(), tc.expect) {
				t.Errorf("the report should mention %q, got:\n%s", tc.expect, out.String())
			}
		})
	}
}

// TestValidateReportsOneProblemPerLine keeps the output pasteable into an
// editor's quickfix list.
func TestValidateReportsOneProblemPerLine(t *testing.T) {
	dir := writeMinimalPack(t)
	replaceInLevel(t, dir, `on_fail: "No answer.txt yet."`, `on_fail: ""`)
	replaceInLevel(t, dir, "type: file_exists", "type: file_smells")

	var out bytes.Buffer
	if err := runValidate(&out, dir, false); err == nil {
		t.Fatal("a broken pack was accepted")
	}

	var problems int
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if strings.HasPrefix(line, "error:") {
			problems++
			if !strings.Contains(line, "levels/") {
				t.Errorf("a problem line does not name its file: %q", line)
			}
		}
	}
	if problems < 2 {
		t.Errorf("both problems should be reported, got %d:\n%s", problems, out.String())
	}
}

// TestValidateOnAMissingDirectoryIsUserFacing covers non-negotiable 6 for the
// most likely mistake: a wrong path.
func TestValidateOnAMissingDirectoryIsUserFacing(t *testing.T) {
	var out bytes.Buffer
	err := runValidate(&out, filepath.Join(t.TempDir(), "not-there"), false)
	if err == nil {
		t.Fatal("a missing pack directory was accepted")
	}
	assertUserFacing(t, err)
}

// TestValidateOnAFileRatherThanADirectoryReadsAsEnglish covers the other half
// of the "this is not a pack directory" case.
//
// A path that exists but is a regular file leaves os.Stat's error nil, so
// wrapping it produced "read go.mod: %!w(<nil>)": a message that names no
// cause and reads like a crash. Non-negotiable 6 in CLAUDE.md says every
// user-facing error names what failed and why, and a formatting verb is
// neither.
func TestValidateOnAFileRatherThanADirectoryReadsAsEnglish(t *testing.T) {
	file := filepath.Join(t.TempDir(), "pack.yaml")
	if err := os.WriteFile(file, []byte("id: not-a-directory\n"), 0o644); err != nil {
		t.Fatalf("write the fixture: %v", err)
	}

	var out bytes.Buffer
	err := runValidate(&out, file, false)
	if err == nil {
		t.Fatal("a regular file was accepted as a pack directory")
	}
	assertUserFacing(t, err)

	if strings.Contains(err.Error(), "%!") {
		t.Errorf("the error carries an unexpanded formatting verb, so it names no cause: %v", err)
	}
	if !strings.Contains(err.Error(), "not a pack directory") {
		t.Errorf("the error does not say what is wrong with the path given: %v", err)
	}
}

// TestValidateCommandIsWiredIntoTheAuthorGroup proves the verb reaches the
// real implementation rather than the stub it replaced.
func TestValidateCommandIsWiredIntoTheAuthorGroup(t *testing.T) {
	root := NewRootCommand(VersionInfo{})

	_, err := execCommand(t, root, "author", "validate", shippedPack)
	if err != nil {
		t.Fatalf("author validate on the shipped pack failed: %v", err)
	}
}

// minimalPack is a complete, valid one-level pack the failure-mode tests
// break in exactly one place each.
const minimalPack = `id: fixture-pack
name: "Fixture Pack"
version: 0.1.0
content_api: 1
author: "Test"
license: MIT
description: "A pack that exists to be validated."
acts:
  - id: act1
    title: "Act One"
    levels: [nav-01]
`

const minimalLevel = `id: nav-01
version: 1
title: "First Contact"
act: act1
difficulty: 1
xp: 40
concepts: [pwd]
briefing: |
  Find out where you are.
objectives:
  - id: obj1
    text: "answer.txt holds the path"
setup:
  root: /home/learner/quest
checks:
  - id: obj1
    type: file_exists
    path: /home/learner/quest/answer.txt
    on_fail: "No answer.txt yet."
hints:
  - cost: 5
    text: "The prompt is an abbreviation."
  - cost: 40
    reveal_solution: true
solution: |
  pwd > ~/quest/answer.txt
`

// writeMinimalPack materializes minimalPack into a temporary directory.
func writeMinimalPack(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "levels"), 0o755); err != nil {
		t.Fatalf("create levels/: %v", err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("pack.yaml", minimalPack)
	write(filepath.Join("levels", "01-nav-01.yaml"), minimalLevel)
	return dir
}

// levelPath is the one level file in a pack written by writeMinimalPack.
func levelPath(dir string) string {
	return filepath.Join(dir, "levels", "01-nav-01.yaml")
}

// replaceInLevel rewrites the level file, failing if the text to replace is
// not there. A mutation that silently does nothing would make the test pass
// for the wrong reason.
func replaceInLevel(t *testing.T, dir, old, new string) {
	t.Helper()

	data, err := os.ReadFile(levelPath(dir))
	if err != nil {
		t.Fatalf("read the level: %v", err)
	}
	if !strings.Contains(string(data), old) {
		t.Fatalf("the fixture does not contain %q, so this test would prove nothing", old)
	}
	updated := strings.Replace(string(data), old, new, 1)
	if err := os.WriteFile(levelPath(dir), []byte(updated), 0o600); err != nil {
		t.Fatalf("write the level: %v", err)
	}
}

// appendToLevel adds text to the end of the objectives block.
func appendToLevel(t *testing.T, dir, text string) {
	t.Helper()

	data, err := os.ReadFile(levelPath(dir))
	if err != nil {
		t.Fatalf("read the level: %v", err)
	}
	const anchor = "setup:\n"
	updated := strings.Replace(string(data), anchor, text+anchor, 1)
	if err := os.WriteFile(levelPath(dir), []byte(updated), 0o600); err != nil {
		t.Fatalf("write the level: %v", err)
	}
}
