package wsl

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/platform"
)

func TestValidateInstallDirRefusesAnEmptyPath(t *testing.T) {
	tmp := t.TempDir()
	err := validateInstallDirUnder(tmp, "")
	if err == nil {
		t.Fatal("validateInstallDirUnder(tmp, \"\") = nil error, want a refusal")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error = %q, want it to say the path is empty", err.Error())
	}
}

func TestValidateInstallDirRefusesADriveRoot(t *testing.T) {
	tmp := t.TempDir()
	if err := validateInstallDirUnder(tmp, `C:\`); err == nil {
		t.Fatal(`validateInstallDirUnder(tmp, "C:\\") = nil error, want a refusal`)
	}
}

func TestValidateInstallDirRefusesTheDataDirRootItself(t *testing.T) {
	tmp := t.TempDir()
	err := validateInstallDirUnder(tmp, tmp)
	if err == nil {
		t.Fatal("validateInstallDirUnder(tmp, tmp) = nil error, want a refusal")
	}
	if !strings.Contains(err.Error(), "progress database") && !strings.Contains(err.Error(), "data dir") && !strings.Contains(err.Error(), "root itself") {
		t.Errorf("error = %q, want it to explain the root is excluded because it holds the progress database", err.Error())
	}
}

// TestValidateInstallDirRefusesTheUserProfile exercises the actual case the
// safety review names: an install directory that resolves to the real
// profile directory, not the literal, unexpanded string "%USERPROFILE%",
// which is refused as merely not-absolute and would still be refused with
// the DataDir prefix check deleted entirely.
func TestValidateInstallDirRefusesTheUserProfile(t *testing.T) {
	tmp := t.TempDir()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no user home directory on this host to test against: %v", err)
	}
	// The refusal formats the data directory with %q, so a Windows path
	// arrives in the message with every backslash doubled. Look for the
	// quoted form rather than the raw one: comparing against the raw path
	// passes on Linux and fails on Windows for a reason that has nothing
	// to do with what this test is checking.
	if err := validateInstallDirUnder(tmp, home); err == nil {
		t.Fatalf("validateInstallDirUnder(tmp, %q) (the real user profile directory) = nil error, want a refusal", home)
	} else if !strings.Contains(err.Error(), strconv.Quote(tmp)) {
		t.Errorf("validateInstallDirUnder(tmp, %q) error = %q, want it to name the data directory %q", home, err.Error(), tmp)
	}
}

// TestValidateInstallDirRefusesADotDotTraversal covers two cases that reach
// validateInstallDirUnder's refusal by a route other than the pre-Clean ".."
// loop: `\..\..\Windows` has no "/" on either CI leg once
// filepath.ToSlash sees it as a single segment, so the not-absolute check
// refuses it first, and filepath.Join(tmp, "..", "..", "Windows") is
// cleaned by Join itself before validateInstallDirUnder ever sees a ".."
// token, so the not-under-dataDir check refuses that one. Neither pins the
// loop that is checked before any cleaning specifically because cleaning is
// what would hide it, which is why a third, raw and unjoined case follows.
func TestValidateInstallDirRefusesADotDotTraversal(t *testing.T) {
	tmp := t.TempDir()
	cases := []string{
		`\..\..\Windows`,
		filepath.Join(tmp, "..", "..", "Windows"),
	}
	for _, c := range cases {
		if err := validateInstallDirUnder(tmp, c); err == nil {
			t.Errorf("validateInstallDirUnder(tmp, %q) = nil error, want a refusal", c)
		}
	}

	raw := tmp + "/wsl/../../Windows"
	err := validateInstallDirUnder(tmp, raw)
	if err == nil {
		t.Fatalf("validateInstallDirUnder(tmp, %q) = nil error, want a refusal", raw)
	}
	if !strings.Contains(err.Error(), "..") {
		t.Errorf("validateInstallDirUnder(tmp, %q) error = %q, want it to name the .. segment", raw, err.Error())
	}
}

func TestValidateInstallDirRefusesASymlinkEscapingDataDir(t *testing.T) {
	tmp := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(tmp, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("cannot create a symlink on this OS/filesystem: %v", err)
	}
	if err := validateInstallDirUnder(tmp, link); err == nil {
		t.Fatal("validateInstallDirUnder with a symlink escaping DataDir = nil error, want a refusal")
	}
}

func TestValidateInstallDirRefusesARelativePath(t *testing.T) {
	tmp := t.TempDir()
	err := validateInstallDirUnder(tmp, "wsl/shellforge-sandbox")
	if err == nil {
		t.Fatal("validateInstallDirUnder(tmp, \"wsl/shellforge-sandbox\") = nil error, want a refusal")
	}
	if !strings.Contains(err.Error(), "absolute") {
		t.Errorf("error = %q, want it to say the path is not absolute", err.Error())
	}
}

func TestValidateInstallDirAcceptsTheDerivedPath(t *testing.T) {
	tmp := t.TempDir()
	dir := installDirFor(tmp, sandboxDistro)
	if err := validateInstallDirUnder(tmp, dir); err != nil {
		t.Errorf("validateInstallDirUnder(tmp, installDirFor(tmp, sandboxDistro)) = %v, want nil (the positive control)", err)
	}
}

func TestInstallDirDerivesFromPlatformDataDir(t *testing.T) {
	tmp := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", tmp)
	} else {
		t.Setenv("XDG_DATA_HOME", tmp)
	}

	dataDir, err := platform.DataDir()
	if err != nil {
		t.Fatalf("platform.DataDir: %v", err)
	}

	rt := &wslRuntime{distro: sandboxDistro}
	got, err := rt.installDir()
	if err != nil {
		t.Fatalf("installDir: %v", err)
	}
	want := filepath.Join(dataDir, "wsl", sandboxDistro)
	if got != want {
		t.Errorf("installDir() = %q, want %q", got, want)
	}
}

// TestDefaultRootfsFindsWhatDownloadDestWrote pins downloadDest and
// defaultRootfs's cache fallback to the same path. Before this was factored
// through cachedRootfsPath, downloadDest wrote to
// <CacheDir>/rootfs/rootfs.tar.gz while defaultRootfs's cache check looked
// at <CacheDir>/rootfs.tar.gz: a rootfs this package downloaded and
// digest-verified was invisible to the next Provision with an empty
// ImageSpec.Reference, which refused with rootfs-not-found while a verified
// tarball sat on disk one directory level away.
func TestDefaultRootfsFindsWhatDownloadDestWrote(t *testing.T) {
	if repoPath, err := repoRootRelative(defaultRootfsRel); err == nil {
		if _, statErr := os.Stat(repoPath); statErr == nil {
			t.Skipf("skipping: %s exists on this checkout and would shadow the cache fallback this test exercises", repoPath)
		}
	}

	tmp := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", tmp)
	} else {
		t.Setenv("XDG_CACHE_HOME", tmp)
	}

	dest, err := downloadDest()
	if err != nil {
		t.Fatalf("downloadDest: %v", err)
	}
	if err := platform.EnsureDir(filepath.Dir(dest)); err != nil {
		t.Fatalf("EnsureDir(%s): %v", filepath.Dir(dest), err)
	}
	if err := os.WriteFile(dest, []byte("a downloaded rootfs"), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", dest, err)
	}

	got, err := defaultRootfs()
	if err != nil {
		t.Fatalf("defaultRootfs did not find the file downloadDest wrote to %s: %v", dest, err)
	}
	if got.path != dest {
		t.Errorf("defaultRootfs().path = %q, want %q (the same path downloadDest writes to)", got.path, dest)
	}
}

func TestInstallDirIsDistinctPerDistribution(t *testing.T) {
	tmp := t.TempDir()
	prod := installDirFor(tmp, sandboxDistro)
	test := installDirFor(tmp, contractDistro)
	if prod == test {
		t.Fatalf("installDirFor gave the same directory (%q) for both distributions, want distinct directories so a contract run can never delete the production backing store", prod)
	}
}
