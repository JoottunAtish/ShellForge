package verify

import (
	"strings"
	"testing"
)

// TestTypesReturnsExactlyTheCatalogue pins the full registered set against
// the ticket's own list of names to register. The ticket's prose calls this
// "exactly these 13" while its own comma-separated list, and
// docs/LEVEL-FORMAT.md section 3's own heading, both actually name 14: the
// count in both places appears to be off by one against script, which the
// doc groups separately under "escape hatch" but which is still a
// registered, tested check type like the rest. This test pins the real
// count (14) rather than the prose number, since Types() is the contract
// the validator and the tests both depend on.
func TestTypesReturnsExactlyTheCatalogue(t *testing.T) {
	want := []string{
		"command_matched",
		"command_not_matched",
		"cwd_is",
		"dir_exists",
		"dir_tree",
		"env_var",
		"file_absent",
		"file_content",
		"file_exists",
		"file_mode",
		"file_owner",
		"process_running",
		"script",
		"symlink_target",
	}
	got := Types()
	if len(got) != len(want) {
		t.Fatalf("Types() returned %d types, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Types()[%d] = %q, want %q (Types() must be sorted): %v", i, got[i], want[i], got)
		}
	}
}

// TestEveryTypeRefusesAnUnknownParam proves the rule Factory's own contract
// states and that no Factory actually enforced: a parameter the type does not
// accept is an error, not a silently ignored key.
//
// It matters most for a level author, who reaches these factories through
// `shellforge author validate`. A `sha526` where `sha256` was meant, or a
// `compare-to` where `compare_to` was meant, used to validate clean and
// produce a check that asserted something other than what the YAML said. The
// level would then pass or fail for a reason its author never wrote.
//
// It runs over Types() rather than a hand-written list, so a check type added
// later is covered the day it is registered. The unknown key is supplied on
// its own: the guard runs before the Factory, so no type-specific set of
// valid parameters is needed to reach it.
func TestEveryTypeRefusesAnUnknownParam(t *testing.T) {
	for _, typeName := range Types() {
		t.Run(typeName, func(t *testing.T) {
			factory, ok := Lookup(typeName)
			if !ok {
				t.Fatalf("Types() named %q but Lookup does not know it", typeName)
			}

			_, err := factory(Spec{Type: typeName, ID: "probe", Params: map[string]any{
				"definitely_not_a_param": "x",
			}})
			if err == nil {
				t.Fatal("an unknown param was accepted, so a mistyped parameter in a level would be silently ignored")
			}
			if !strings.Contains(err.Error(), "definitely_not_a_param") {
				t.Errorf("the error does not name the offending key, so an author cannot find it: %v", err)
			}
		})
	}
}

// TestUnknownParamErrorListsWhatTheTypeAccepts pins the other half of the
// message. Naming only the bad key leaves an author guessing at the spelling
// they meant, which is the same problem one step further along.
func TestUnknownParamErrorListsWhatTheTypeAccepts(t *testing.T) {
	factory, ok := Lookup("file_content")
	if !ok {
		t.Fatal("file_content is not registered")
	}

	_, err := factory(Spec{Type: "file_content", ID: "probe", Params: map[string]any{
		"path":       "/home/learner/quest/answer.txt",
		"match":      "sha256",
		"value":      "abc",
		"ignore_cas": true,
	}})
	if err == nil {
		t.Fatal("a mistyped ignore_case was accepted")
	}
	for _, want := range []string{"ignore_cas", "ignore_case", "match", "path", "value"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q, so it does not tell the author what file_content accepts: %v", want, err)
		}
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Register did not panic on a duplicate type name")
		}
	}()
	Register("file_exists", func(Spec) (Check, error) { return nil, nil })
}

func TestLookupUnknownType(t *testing.T) {
	if _, ok := Lookup("does_not_exist"); ok {
		t.Fatal("Lookup reported an unregistered type as known")
	}
}

func TestLookupKnownType(t *testing.T) {
	f, ok := Lookup("file_exists")
	if !ok || f == nil {
		t.Fatal("Lookup did not find the registered file_exists type")
	}
}

// TestAllRegisteredTypesHaveCoverage proves every type Types() returns has
// at least one table test exercising it, via the shared coveredTypes map
// each *_checks_test.go file populates from its own init. A new check type
// registered without a table test fails here instead of shipping silently
// untested.
func TestAllRegisteredTypesHaveCoverage(t *testing.T) {
	for _, typeName := range Types() {
		if !coveredTypes[typeName] {
			t.Errorf("check type %q is registered but has no table test calling markCovered(%q)", typeName, typeName)
		}
	}
}
