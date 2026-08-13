package content

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The refusal tests for the two containment rules this package owns:
// setup.root, which decides what teardown later hands to a recursive delete
// inside the sandbox, and source:, which decides what the pack is allowed to
// read off the host.
//
// Each case is named individually rather than driven from a shared list of
// paths, so a failure says which containment case broke rather than only that
// one of seven did.

// requireRootRefused asserts a setup.root is reported, and reports which case
// leaked when it is not.
func requireRootRefused(t *testing.T, root string) {
	t.Helper()

	lvl := defaultLevel()
	lvl.Setup = "setup:\n  root: " + quoteYAML(root) + "\n"

	report := validateFixture(t, fixturePack("", lvl))
	if _, ok := findProblem(report, "nav-01", "setup.root"); !ok {
		t.Fatalf("setup.root %q was accepted; teardown would hand it to a recursive delete\nreport:\n%s",
			root, formatReport(report))
	}
}

func TestSetupRootRefusesFilesystemRoot(t *testing.T) { requireRootRefused(t, "/") }

func TestSetupRootRefusesHome(t *testing.T) { requireRootRefused(t, "/home") }

// TestSetupRootRefusesLearnerHomeExactly is the case that matters most and
// the one most likely to be missed. It satisfies a naive "starts with
// /home/learner" test, and it is the learner's entire home directory.
func TestSetupRootRefusesLearnerHomeExactly(t *testing.T) {
	requireRootRefused(t, "/home/learner")
}

// TestSetupRootRefusesLearnerHomeWithTrailingSlash covers the same directory
// written the other way, which path.Clean collapses to the case above.
func TestSetupRootRefusesLearnerHomeWithTrailingSlash(t *testing.T) {
	requireRootRefused(t, "/home/learner/")
}

func TestSetupRootRefusesDotDotEscape(t *testing.T) {
	requireRootRefused(t, "/home/learner/../../etc")
}

func TestSetupRootRefusesDotDotInsideLearnerHome(t *testing.T) {
	requireRootRefused(t, "/home/learner/quest/../../../etc")
}

func TestSetupRootRefusesRelativePath(t *testing.T) { requireRootRefused(t, "quest") }

func TestSetupRootRefusesEmpty(t *testing.T) { requireRootRefused(t, "") }

// TestSetupRootAcceptsALevelWorld is the other half: the rule must still
// accept the shape every real level uses, or it is a rule that refuses
// everything.
func TestSetupRootAcceptsALevelWorld(t *testing.T) {
	for _, root := range []string{"/home/learner/quest", "/home/learner/warehouse/bay-3"} {
		lvl := defaultLevel()
		lvl.Setup = "setup:\n  root: " + root + "\n"

		report := validateFixture(t, fixturePack("", lvl))
		requireNoProblem(t, report, "nav-01", "setup.root")
	}
}

// TestAssetSourceRefusesEscapingPaths covers the lexical half of source
// containment, which needs no host filesystem.
func TestAssetSourceRefusesEscapingPaths(t *testing.T) {
	cases := map[string]string{
		"parent traversal":  "../../../etc/passwd",
		"absolute path":     "/etc/passwd",
		"single parent":     "..",
		"leading traversal": "../assets/app.log",
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			lvl := defaultLevel()
			lvl.Setup = "setup:\n  root: /home/learner/quest\n  files:\n" +
				"    - path: notes.txt\n      source: " + quoteYAML(source) + "\n"

			report := validateFixture(t, fixturePack("", lvl))
			if _, ok := findProblem(report, "nav-01", "setup.files[0].source"); !ok {
				t.Fatalf("source %q was accepted, so the pack can read outside itself\nreport:\n%s",
					source, formatReport(report))
			}
		})
	}
}

// TestAssetSourceRefusesMissingAsset covers a source that is inside the pack
// but names nothing.
func TestAssetSourceRefusesMissingAsset(t *testing.T) {
	lvl := defaultLevel()
	lvl.Setup = "setup:\n  root: /home/learner/quest\n  files:\n" +
		"    - path: notes.txt\n      source: assets/not-there.txt\n"

	report := validateFixture(t, fixturePack("", lvl))
	requireProblem(t, report, "nav-01", "setup.files[0].source", ProblemError, "is not in the pack")
}

// TestAssetSourceAcceptsARealAsset is the over-eager-rule guard.
func TestAssetSourceAcceptsARealAsset(t *testing.T) {
	lvl := defaultLevel()
	lvl.Setup = "setup:\n  root: /home/learner/quest\n  files:\n" +
		"    - path: welcome.txt\n      source: assets/welcome.txt\n"

	report := validateFixture(t, fixturePack("", lvl))
	requireNoProblem(t, report, "nav-01", "setup.files[0].source")
}

// TestAssetSourceRefusesSymlinkOutOfThePack is the case a lexical test cannot
// see. The asset name has no .. in it and the file opens successfully; only
// resolving the link before comparing prefixes catches it.
//
// It needs a real filesystem, so it runs against t.TempDir rather than the
// in-memory fixtures. On Windows, creating a symlink requires either
// developer mode or elevation, so the test skips rather than failing on a
// machine that will not let it build the fixture.
func TestAssetSourceRefusesSymlinkOutOfThePack(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "passwd")
	if err := os.WriteFile(secret, []byte("root:x:0:0\n"), 0o600); err != nil {
		t.Fatalf("write the file outside the pack: %v", err)
	}

	packDir := t.TempDir()
	writePackFiles(t, packDir)

	link := filepath.Join(packDir, "assets", "leaked.txt")
	if err := os.Symlink(secret, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("this machine will not create a symlink, so the fixture cannot be built: %v", err)
		}
		t.Fatalf("create the symlink fixture: %v", err)
	}

	report := validateHostPack(t, packDir)

	p, ok := findProblem(report, "nav-01", "setup.files[0].source")
	if !ok {
		t.Fatalf("a symlink pointing outside the pack was accepted\nreport:\n%s", formatReport(report))
	}
	if !strings.Contains(p.Message, "outside the pack") {
		t.Errorf("message should say the asset resolves outside the pack, got %q", p.Message)
	}
}

// TestAssetSourceAcceptsSymlinkInsideThePack proves the rule refuses escape
// and not symlinks, which is the difference between a containment rule and a
// blanket ban.
func TestAssetSourceAcceptsSymlinkInsideThePack(t *testing.T) {
	packDir := t.TempDir()
	writePackFiles(t, packDir)

	real := filepath.Join(packDir, "assets", "welcome.txt")
	link := filepath.Join(packDir, "assets", "leaked.txt")
	if err := os.Symlink(real, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("this machine will not create a symlink, so the fixture cannot be built: %v", err)
		}
		t.Fatalf("create the symlink fixture: %v", err)
	}

	report := validateHostPack(t, packDir)
	requireNoProblem(t, report, "nav-01", "setup.files[0].source")
}

// writePackFiles materializes a minimal valid pack on the host, with one
// level whose single asset is assets/leaked.txt. The caller decides what
// that name points at.
func writePackFiles(t *testing.T, dir string) {
	t.Helper()

	mkdir := func(sub string) {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatalf("create %s: %v", sub, err)
		}
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	mkdir("assets")
	mkdir("levels")

	lvl := defaultLevel()
	lvl.Setup = "setup:\n  root: /home/learner/quest\n  files:\n" +
		"    - path: notes.txt\n      source: assets/leaked.txt\n"

	write("pack.yaml", strings.Replace(packYAML, "%s", defaultActs, 1))
	write(filepath.Join("assets", "welcome.txt"), "Kofi was here.\n")
	write(filepath.Join("levels", "01-nav-01.yaml"), lvl.YAML())
}

// validateHostPack loads and validates a pack from a real directory, with
// both the filesystem and the host root wired in so every containment rule is
// active.
func validateHostPack(t *testing.T, dir string) Report {
	t.Helper()

	fsys := os.DirFS(dir)
	pack, err := LoadPack(fsys, ".")
	if err != nil {
		t.Fatalf("load the host pack: %v", err)
	}
	return Validate(pack, newFakeTypeChecker(), WithPackFS(fsys, "."), WithHostRoot(dir))
}

// quoteYAML renders s as a double-quoted YAML scalar, so that a value which
// is empty or begins with a slash still parses as a string.
func quoteYAML(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}
