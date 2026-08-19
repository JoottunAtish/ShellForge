package doctor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

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
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	p := diskFreeProbe{baseProbe: baseProbe{id: "disk_free", docAnchor: docAnchorDiskSpaceLow}}
	res := p.Run(context.Background())
	if res.ID != "disk_free" {
		t.Errorf("ID = %q, want disk_free", res.ID)
	}
	if res.Detail == "" || res.Remediation == "" || res.DocAnchor == "" {
		t.Errorf("disk_free result has an empty required field: %+v", res)
	}
}

// TestDiskFreeProbeIsFixableOnlyWhenTheDirectoryIsMissing pins the one case
// in the whole package where Fixable is true.
func TestDiskFreeProbeIsFixableOnlyWhenTheDirectoryIsMissing(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "shellforge")
	t.Setenv("XDG_DATA_HOME", root)

	p := diskFreeProbe{baseProbe: baseProbe{id: "disk_free", docAnchor: docAnchorDiskSpaceLow}}
	res := p.Run(context.Background())
	if res.Status == Fail {
		t.Skip("this host does not have enough free disk space for this assertion to be meaningful")
	}
	if !res.Fixable {
		t.Errorf("Fixable = false with the data directory missing (%s) and enough free space, want true", dataDir)
	}

	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("create the data directory: %v", err)
	}
	res2 := p.Run(context.Background())
	if res2.Fixable {
		t.Errorf("Fixable = true once the data directory already exists, want false")
	}
}
