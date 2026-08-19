package wsl

import (
	"context"
	"strings"
	"testing"
)

// TestVersion2AvailableIsFalseOutsideWindows drives the exported entry
// point on the real test GOOS, which is never "windows" in this suite, and
// confirms it reports unavailable without ever calling the probe that would
// shell out to wsl.exe. A probe that runs regardless of goos would make
// this test depend on PATH, which is exactly what the real Version2Available
// must not do off Windows.
func TestVersion2AvailableIsFalseOutsideWindows(t *testing.T) {
	calls := 0
	probe := func(ctx context.Context) ([]distro, error) {
		calls++
		return nil, nil
	}

	ok, detail := version2Available(context.Background(), "linux", probe)
	if ok {
		t.Error("version2Available(linux) ok = true, want false")
	}
	if strings.TrimSpace(detail) == "" {
		t.Error("version2Available(linux) detail is empty, want a reason")
	}
	if calls != 0 {
		t.Errorf("probe was called %d times on a non-Windows GOOS, want 0", calls)
	}
}

// TestVersion2AvailableCallsTheRealEntryPointOnNonWindows exercises the
// exported function itself (not the seam), on the assumption that this
// suite never runs on Windows: it must also report unavailable, proving the
// exported wrapper actually plumbs goruntime.GOOS through rather than
// hardcoding "windows" at the seam only.
func TestVersion2AvailableCallsTheRealEntryPointOnNonWindows(t *testing.T) {
	ok, detail := Version2Available(context.Background())
	if ok {
		t.Fatal("Version2Available() ok = true on a non-Windows test host, want false")
	}
	if strings.TrimSpace(detail) == "" {
		t.Error("Version2Available() detail is empty, want a reason")
	}
}

func TestClassifyVersion2Availability(t *testing.T) {
	tests := []struct {
		name      string
		rows      []distro
		wantOK    bool
		wantEmpty bool
	}{
		{
			name:      "no distributions at all",
			rows:      nil,
			wantOK:    true,
			wantEmpty: true,
		},
		{
			name:      "one version 2 row",
			rows:      []distro{{Name: "shellforge-sandbox", State: "Running", Version: 2}},
			wantOK:    true,
			wantEmpty: true,
		},
		{
			name:      "version 1 only",
			rows:      []distro{{Name: "legacy-distro", State: "Stopped", Version: 1}},
			wantOK:    false,
			wantEmpty: false,
		},
		{
			name: "a mix of version 1 and version 2",
			rows: []distro{
				{Name: "legacy-distro", State: "Stopped", Version: 1},
				{Name: "shellforge-sandbox", State: "Running", Version: 2},
			},
			wantOK:    true,
			wantEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, detail := classifyVersion2(tt.rows)
			if ok != tt.wantOK {
				t.Errorf("classifyVersion2(%v) ok = %v, want %v", tt.rows, ok, tt.wantOK)
			}
			if tt.wantEmpty && detail != "" {
				t.Errorf("classifyVersion2(%v) detail = %q, want empty", tt.rows, detail)
			}
			if !tt.wantEmpty && strings.TrimSpace(detail) == "" {
				t.Errorf("classifyVersion2(%v) detail is empty, want a reason naming WSL version 1", tt.rows)
			}
			if !tt.wantEmpty && !strings.Contains(detail, "version 1") {
				t.Errorf("classifyVersion2(%v) detail = %q, want it to name WSL version 1", tt.rows, detail)
			}
		})
	}
}
