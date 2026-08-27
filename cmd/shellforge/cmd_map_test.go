package main

import (
	"strings"
	"testing"
)

// TestMapCommandAgainstFreshProgressDatabaseSucceeds is BLOCKING finding 1:
// nothing before this file called runMap or newMapCommand's cobra wiring,
// only Resolve/Next in isolation (internal/game/curriculum_test.go) and the
// pure renderMap function in isolation (render_map_test.go).
//
// XDG_DATA_HOME points at a fresh t.TempDir, so store.Open creates a brand
// new progress database with no rows: this is the "empty progress database"
// half of the acceptance criterion. The "no Docker daemon running" half is
// not something this test starts or stops a daemon to prove; it holds by
// construction instead. runMap's own call path is content.Embedded, plus
// platform.DatabasePath, store.Open, Store.EnsureProfile, Store.LevelStates,
// and game.Resolve, none of which import a runtime package, and
// internal/game is barred by internal/archtest from importing one at all
// (CLAUDE.md's layer rule: L4 must never import a Docker or WSL type). So
// nothing on runMap's path can reach a Docker daemon, running or not.
func TestMapCommandAgainstFreshProgressDatabaseSucceeds(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	root := NewRootCommand(VersionInfo{})
	out, err := execCommand(t, root, "map")
	if err != nil {
		t.Fatalf("shellforge map against a fresh progress database: error = %v, want nil", err)
	}
	if !strings.Contains(out, "Total:") {
		t.Errorf("shellforge map output does not contain %q: %q", "Total:", out)
	}
}

// TestMapAsciiFlagAndNoColorEnvProduceIdenticalOutput is suggestion finding
// 2's rendering half, and pins acceptance criterion 6: --ascii and NO_COLOR
// are two different roads to the same color=false argument, so they must
// produce byte-for-byte identical output against the same fresh database,
// and neither must emit ESC (0x1b).
func TestMapAsciiFlagAndNoColorEnvProduceIdenticalOutput(t *testing.T) {
	dataDir := t.TempDir()

	t.Setenv("XDG_DATA_HOME", dataDir)
	rootAscii := NewRootCommand(VersionInfo{})
	asciiOut, err := execCommand(t, rootAscii, "map", "--ascii")
	if err != nil {
		t.Fatalf("shellforge map --ascii: error = %v, want nil", err)
	}

	t.Setenv("XDG_DATA_HOME", dataDir)
	t.Setenv("NO_COLOR", "1")
	rootNoColor := NewRootCommand(VersionInfo{})
	noColorOut, err := execCommand(t, rootNoColor, "map")
	if err != nil {
		t.Fatalf("shellforge map with NO_COLOR=1: error = %v, want nil", err)
	}

	if asciiOut != noColorOut {
		t.Errorf("--ascii output and NO_COLOR output differ:\n--ascii:   %q\nNO_COLOR:  %q", asciiOut, noColorOut)
	}
	if strings.ContainsRune(asciiOut, '\x1b') {
		t.Errorf("--ascii output contains ESC (0x1b): %q", asciiOut)
	}
	if strings.ContainsRune(noColorOut, '\x1b') {
		t.Errorf("NO_COLOR output contains ESC (0x1b): %q", noColorOut)
	}
}
