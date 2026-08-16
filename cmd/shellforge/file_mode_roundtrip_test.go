package main

import (
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/verify"
)

// The tests in this file cover the round trip for file_mode's list of
// acceptable modes, which is the half of issue #95 that neither package can
// prove on its own.
//
// internal/content owns the decoder that decides whether a key becomes a
// CheckSpec field or a Params entry. internal/verify owns the registry that
// decides whether a Params entry is a parameter the type accepts. The two are
// both layer 3 and neither may import the other, so a test inside either one
// runs against a fake and can only ever prove its own half. The parameter was
// documented for months and unreachable precisely because nothing joined
// them. This is the layer where they meet, alongside
// TestEmbeddedPackValidatesAgainstTheRealRegistry.

// checkFixtureLevel renders a level whose single check is the text given, so a
// test can vary the check and nothing else.
func checkFixtureLevel(check string) string {
	return `id: nav-01
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
    text: "deploy.sh is executable"
setup:
  root: /home/learner/quest
checks:
` + check + `hints:
  - cost: 5
    text: "Look at the permission bits."
  - cost: 40
    reveal_solution: true
solution: |
  chmod 0750 ~/quest/deploy.sh
`
}

// checkFixturePack wraps one check in a complete, otherwise valid pack.
func checkFixturePack(check string) fstest.MapFS {
	const packYAML = `id: fixture-pack
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
	return fstest.MapFS{
		"pack.yaml":             &fstest.MapFile{Data: []byte(packYAML)},
		"assets/README.md":      &fstest.MapFile{Data: []byte("# Assets\n")},
		"levels/01-nav-01.yaml": &fstest.MapFile{Data: []byte(checkFixtureLevel(check))},
	}
}

// TestFileModeModesReachesTheRegistryFromYAML is the acceptance test for #95.
// A level written the way docs/LEVEL-FORMAT.md section 3 documents must load,
// keep its list in Params rather than losing it to a composition field, and
// build against the real factory.
func TestFileModeModesReachesTheRegistryFromYAML(t *testing.T) {
	pack, err := content.LoadPack(checkFixturePack(`  - id: obj1
    type: file_mode
    path: /home/learner/quest/deploy.sh
    modes: ["0700", "0750"]
    on_fail: "deploy.sh is not executable yet. Try chmod."
`), ".")
	if err != nil {
		t.Fatalf("a level using the documented modes parameter failed to load: %v", err)
	}

	lvl, ok := pack.Level("nav-01")
	if !ok {
		t.Fatal("nav-01 is not in the fixture pack")
	}
	spec := lvl.Checks[0]

	if spec.Type != "file_mode" {
		t.Errorf("check type = %q, want file_mode", spec.Type)
	}
	if spec.IsComposite() {
		t.Fatal("the decoder read modes as a composition node; that is the #95 defect, in a new spelling")
	}
	got, ok := spec.Params["modes"]
	if !ok {
		t.Fatalf("modes did not reach Params, so the factory can never see it; params = %v", spec.Params)
	}
	if fmt.Sprint(got) != "[0700 0750]" {
		t.Errorf("modes = %v, want [0700 0750]", got)
	}

	if err := (verify.TypeChecker{}).ValidateParams(spec.Type, spec.Params); err != nil {
		t.Fatalf("the real registry rejected the documented parameter set: %v", err)
	}

	report := content.Validate(pack, verify.TypeChecker{})
	for _, p := range report.Errors() {
		t.Errorf("the documented form produced a validation error: %s", p.Error())
	}
}

// TestFileModeAnyOfIsStillRefused pins what a pack written against the
// pre-#95 documentation now gets. The rename does not make the old spelling
// work, and it must not make it silent: any_of stays a composition key, so a
// list of octal strings under it is not a list of branches and never reaches
// file_mode.
//
// The refusal lands in the decoder rather than the validator, because
// CheckSpec.AnyOf is []CheckSpec and "0700" is not a check. That is earlier
// than the collision #95 described, and it is the better error of the two: it
// names the offending key and the file it is in.
func TestFileModeAnyOfIsStillRefused(t *testing.T) {
	_, err := content.LoadPack(checkFixturePack(`  - id: obj1
    type: file_mode
    path: /home/learner/quest/deploy.sh
    any_of: ["0700", "0750"]
    on_fail: "deploy.sh is not executable yet. Try chmod."
`), ".")
	if err == nil {
		t.Fatal("a level using the old any_of spelling loaded; a list of modes under a composition key must be refused")
	}
	for _, want := range []string{"any_of", "01-nav-01.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the load error does not mention %q, so an author cannot locate the problem: %v", want, err)
		}
	}
}

// TestFileModeWithARealCompositionBranchIsRefused covers the collision #95
// actually described, which needs an any_of whose branches are well-formed
// checks. A check cannot be both a type and a composition node, and the pack
// validator says so before the engine ever builds it.
func TestFileModeWithARealCompositionBranchIsRefused(t *testing.T) {
	pack, err := content.LoadPack(checkFixturePack(`  - id: obj1
    type: file_mode
    path: /home/learner/quest/deploy.sh
    mode: "0700"
    on_fail: "deploy.sh is not executable yet. Try chmod."
    any_of:
      - type: file_exists
        path: /home/learner/quest/deploy.sh
        on_fail: "deploy.sh is missing."
`), ".")
	if err != nil {
		t.Fatalf("fixture failed to load, so the validator rule is untested: %v", err)
	}

	report := content.Validate(pack, verify.TypeChecker{})
	if len(report.Errors()) == 0 {
		t.Fatal("a check that is both a type and a composition node validated clean")
	}

	var joined strings.Builder
	for _, p := range report.Errors() {
		joined.WriteString(p.Error())
		joined.WriteString("\n")
	}
	for _, want := range []string{"file_mode", "not both", "01-nav-01.yaml"} {
		if !strings.Contains(joined.String(), want) {
			t.Errorf("no validation error mentions %q, so an author cannot locate the collision:\n%s", want, joined.String())
		}
	}
}
