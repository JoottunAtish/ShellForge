package wsl

import (
	"context"
	goruntime "runtime"
	"strings"
	"testing"
)

// TestVersion2AvailableIsFalseOutsideWindows drives the seam with goos
// pinned to "linux" and confirms it reports unavailable without ever
// calling the probe that would shell out to wsl.exe. A probe that runs
// regardless of goos would make this test depend on PATH, which is exactly
// what the real Version2Available must not do off Windows.
//
// It pins goos rather than reading goruntime.GOOS on purpose, so it asserts
// the same thing on every leg of CI, the windows-latest one included.
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
// exported function itself rather than the seam, proving the wrapper really
// plumbs goruntime.GOOS through instead of hardcoding "windows" at the seam
// only.
//
// It skips on Windows, and the reason is worth keeping: an earlier version
// of this test asserted the non-Windows answer unconditionally, on the
// stated assumption that "this suite never runs on Windows". CI has a
// Test (windows-latest) leg, so that assumption was false and the test
// failed there. On a Windows host this function resolves wsl.exe and runs
// `wsl -l -v` for real, and what that reports depends on the machine, so
// there is no fixed answer to assert. The Windows side of the behaviour is
// covered without a subprocess by TestProbeVersion2ClassifiesNonZeroExit,
// which drives the same code through a fake runner with goos pinned to
// "windows".
func TestVersion2AvailableCallsTheRealEntryPointOnNonWindows(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("this asserts the non-Windows short circuit; on Windows the real probe shells out to wsl.exe and its answer depends on the host")
	}

	ok, detail := Version2Available(context.Background())
	if ok {
		t.Fatal("Version2Available() ok = true on a non-Windows test host, want false")
	}
	if strings.TrimSpace(detail) == "" {
		t.Error("Version2Available() detail is empty, want a reason")
	}
}

// TestProbeVersion2ClassifiesNonZeroExit drives realVersion2Probe's
// classification logic through a fake runner, exercised through
// version2Available so the assertion is on the thing a caller actually
// sees: available or not, with a detail. Before the fix, a non-zero exit
// whose failure signature lived only on stderr was invisible to this
// probe, because it discarded stderr and let parseList's forgiving read of
// an empty stdout report an empty table instead of the failure. Each of
// these cases would have reported ok=true (available) under that bug.
func TestProbeVersion2ClassifiesNonZeroExit(t *testing.T) {
	tests := []struct {
		name       string
		result     fakeResult
		wantOK     bool
		wantDetail string // substring the detail must contain when wantOK is false
	}{
		{
			name:   "empty stdout, empty stderr, exit 1 reads as no distributions",
			result: fakeResult{code: 1},
			wantOK: true,
		},
		{
			name:       "WSL_E_KERNEL on stderr, exit 1 is a genuine failure",
			result:     fakeResult{stderr: []byte("Error: WSL_E_KERNEL"), code: 1},
			wantOK:     false,
			wantDetail: "WSL_E_KERNEL",
		},
		{
			name:       "0x80370102 on stderr, exit 1 is a genuine failure",
			result:     fakeResult{stderr: []byte("Error code: 0x80370102"), code: 1},
			wantOK:     false,
			wantDetail: "0x80370102",
		},
		{
			name:       "an import-blocked signature on stderr, exit 1 is a genuine failure",
			result:     fakeResult{stderr: []byte("Access is denied."), code: 1},
			wantOK:     false,
			wantDetail: "Access is denied",
		},
		{
			name:   "a clean version 2 table, exit 0, is available",
			result: fakeResult{stdout: verboseListFixture(verboseRow{name: "shellforge-sandbox", state: "Running", version: 2}), code: 0},
			wantOK: true,
		},
		{
			name:       "a clean version 1 only table, exit 0, is unavailable with the existing detail",
			result:     fakeResult{stdout: verboseListFixture(verboseRow{name: "legacy-distro", state: "Stopped", version: 1}), code: 0},
			wantOK:     false,
			wantDetail: "version 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeRunner{results: []fakeResult{tt.result}}
			probe := func(ctx context.Context) ([]distro, error) {
				return probeVersion2(ctx, fake)
			}

			ok, detail := version2Available(context.Background(), "windows", probe)
			if ok != tt.wantOK {
				t.Errorf("version2Available ok = %v, want %v (detail %q)", ok, tt.wantOK, detail)
			}
			if !tt.wantOK && !strings.Contains(detail, tt.wantDetail) {
				t.Errorf("version2Available detail = %q, want it to contain %q", detail, tt.wantDetail)
			}
		})
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
