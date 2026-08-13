package verify

import (
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
