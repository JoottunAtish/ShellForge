package wsl

import (
	"os"
	"path/filepath"
	"runtime"
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

func TestValidateInstallDirRefusesTheUserProfile(t *testing.T) {
	tmp := t.TempDir()
	if err := validateInstallDirUnder(tmp, "%USERPROFILE%"); err == nil {
		t.Fatal("validateInstallDirUnder(tmp, an unexpanded USERPROFILE variable) = nil error, want a refusal")
	}
}

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

func TestInstallDirIsDistinctPerDistribution(t *testing.T) {
	tmp := t.TempDir()
	prod := installDirFor(tmp, sandboxDistro)
	test := installDirFor(tmp, contractDistro)
	if prod == test {
		t.Fatalf("installDirFor gave the same directory (%q) for both distributions, want distinct directories so a contract run can never delete the production backing store", prod)
	}
}
