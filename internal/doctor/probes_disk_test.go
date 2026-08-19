package doctor

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// dataDirEnvVar returns the name of the environment variable
// internal/platform.DataDir consults to resolve its base directory, for
// goos. DataDir reads XDG_DATA_HOME everywhere except Windows, where it
// reads LocalAppData through os.UserCacheDir instead (see
// internal/platform/paths.go). Taking goos as a parameter rather than
// reading runtime.GOOS inside this function is what makes the Windows
// branch exercisable by a test running on Linux: see
// TestDataDirEnvVarSelectsLocalAppDataOnWindows below.
func dataDirEnvVar(goos string) string {
	if goos == "windows" {
		return "LocalAppData"
	}
	return "XDG_DATA_HOME"
}

// setDataDirEnv redirects internal/platform.DataDir to dir for the
// lifetime of t, on whichever platform the test is actually running on.
// Setting only XDG_DATA_HOME would leave a Windows CI leg measuring the
// runner's real %LocalAppData%\shellforge instead of the redirected
// directory, since DataDir ignores XDG_DATA_HOME on Windows.
func setDataDirEnv(t *testing.T, dir string) {
	t.Helper()
	t.Setenv(dataDirEnvVar(runtime.GOOS), dir)
}

// TestDataDirEnvVarSelectsLocalAppDataOnWindows covers the Windows branch
// of dataDirEnvVar directly, since this suite otherwise only ever runs on
// a Windows GOOS as one leg of CI: it cannot exercise the Windows path of
// setDataDirEnv's own t.Setenv call on that leg's host OS, but the
// selection logic itself is plain data and needs no host cooperation to
// test.
func TestDataDirEnvVarSelectsLocalAppDataOnWindows(t *testing.T) {
	tests := []struct {
		goos string
		want string
	}{
		{"windows", "LocalAppData"},
		{"linux", "XDG_DATA_HOME"},
		{"darwin", "XDG_DATA_HOME"},
	}
	for _, tt := range tests {
		if got := dataDirEnvVar(tt.goos); got != tt.want {
			t.Errorf("dataDirEnvVar(%q) = %q, want %q", tt.goos, got, tt.want)
		}
	}
}

func TestClassifyDiskFree(t *testing.T) {
	tests := []struct {
		free uint64
		want Level
	}{
		{0, Fail},
		{minFreeBytes - 1, Fail},
		{minFreeBytes, OK},
		{minFreeBytes * 10, OK},
	}
	for _, tt := range tests {
		if got := classifyDiskFree(tt.free); got != tt.want {
			t.Errorf("classifyDiskFree(%d) = %v, want %v", tt.free, got, tt.want)
		}
	}
}

func TestNearestExistingAncestorWalksUpToARealDirectory(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "does", "not", "exist", "yet")

	got := nearestExistingAncestor(missing)
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("nearestExistingAncestor(%q) = %q, which does not exist: %v", missing, got, err)
	}
	if got != root {
		t.Errorf("nearestExistingAncestor(%q) = %q, want %q", missing, got, root)
	}
}

func TestNearestExistingAncestorOnAnExistingDirectoryReturnsItself(t *testing.T) {
	root := t.TempDir()
	if got := nearestExistingAncestor(root); got != root {
		t.Errorf("nearestExistingAncestor(%q) = %q, want itself", root, got)
	}
}

// TestFreeBytesAtReportsSomethingForTheTempDir is the one test in this file
// that touches the real machine. It skips rather than fails when statfs is
// unavailable, because that is an environment fact, not a defect.
func TestFreeBytesAtReportsSomethingForTheTempDir(t *testing.T) {
	free, err := freeBytesAt(t.TempDir())
	if err != nil {
		t.Skipf("statfs is unavailable in this environment: %v", err)
	}
	if free == 0 {
		t.Error("freeBytesAt reported 0 bytes free for a writable temp directory")
	}
}

func TestDiskFreeProbeIsFullyPopulated(t *testing.T) {
	setDataDirEnv(t, t.TempDir())
	p := diskFreeProbe{baseProbe: baseProbe{id: "disk_free", docAnchor: docAnchorDiskSpaceLow}}
	res := p.Run(context.Background())
	if res.ID != "disk_free" {
		t.Errorf("ID = %q, want disk_free", res.ID)
	}
	if res.Detail == "" || res.Remediation == "" || res.DocAnchor == "" {
		t.Errorf("disk_free result has an empty required field: %+v", res)
	}
}

// TestDiskFreeProbeIsNeverFixableRegardlessOfDataDirExistence pins the
// finding-4 fix: disk_free is about free space only. Whether the data
// directory exists yet is sandbox_health's row now (see
// probes_sandbox_test.go for its Fixable test), so disk_free must report
// OK, not Warn, and never Fixable, whether or not the directory exists.
func TestDiskFreeProbeIsNeverFixableRegardlessOfDataDirExistence(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "shellforge")
	setDataDirEnv(t, root)

	p := diskFreeProbe{baseProbe: baseProbe{id: "disk_free", docAnchor: docAnchorDiskSpaceLow}}
	res := p.Run(context.Background())
	if res.Status == Fail {
		t.Skip("this host does not have enough free disk space for this assertion to be meaningful")
	}
	if res.Status != OK {
		t.Errorf("Status = %v with the data directory missing and enough free space, want OK", res.Status)
	}
	if res.Fixable {
		t.Errorf("Fixable = true with the data directory missing, want false: disk_free never claims Fixable")
	}

	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("create the data directory: %v", err)
	}
	res2 := p.Run(context.Background())
	if res2.Status != OK {
		t.Errorf("Status = %v once the data directory exists, want OK", res2.Status)
	}
	if res2.Fixable {
		t.Errorf("Fixable = true once the data directory exists, want false")
	}
}

// TestFreeBytesFromStatfs is the gosec G115 regression: a raw statfs
// Bsize is a signed field on some platforms, and this pins the guard that
// keeps a bad value from turning into a wrong, huge, free-space number
// instead of an error.
func TestFreeBytesFromStatfs(t *testing.T) {
	tests := []struct {
		name    string
		bavail  uint64
		bsize   int64
		want    uint64
		wantErr bool
	}{
		{"typical 4KiB blocks", 1000, 4096, 4096000, false},
		{"zero available blocks", 0, 4096, 0, false},
		{"zero block size is rejected", 1000, 0, 0, true},
		{"negative block size is rejected", 1000, -1, 0, true},
		{"the exact overflow boundary still fits", 2, math.MaxInt64, 2 * math.MaxInt64, false},
		{"one past the overflow boundary is rejected", 3, math.MaxInt64, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := freeBytesFromStatfs(tt.bavail, tt.bsize)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("freeBytesFromStatfs(%d, %d) = %d, <nil>, want an error", tt.bavail, tt.bsize, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("freeBytesFromStatfs(%d, %d) returned an unexpected error: %v", tt.bavail, tt.bsize, err)
			}
			if got != tt.want {
				t.Errorf("freeBytesFromStatfs(%d, %d) = %d, want %d", tt.bavail, tt.bsize, got, tt.want)
			}
		})
	}
}
