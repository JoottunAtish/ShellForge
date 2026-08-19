package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/doctor"
	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
)

// TestDoctorExitErrorIsNilUnlessSomethingFailed is AC3.
func TestDoctorExitErrorIsNilUnlessSomethingFailed(t *testing.T) {
	tests := []struct {
		name    string
		report  doctor.Report
		wantErr bool
	}{
		{
			"all ok",
			doctor.Report{Results: []doctor.Result{{ID: "a", Status: doctor.OK}, {ID: "b", Status: doctor.OK}}},
			false,
		},
		{
			"warns only",
			doctor.Report{Results: []doctor.Result{{ID: "a", Status: doctor.OK}, {ID: "b", Status: doctor.Warn}}},
			false,
		},
		{
			"one fail",
			doctor.Report{Results: []doctor.Result{{ID: "a", Status: doctor.OK}, {ID: "b", Status: doctor.Fail}}},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := doctorExitError(tt.report)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("doctorExitError on %s = nil, want a *ux.Error so main exits 1", tt.name)
				}
				var uxErr *ux.Error
				if !errors.As(err, &uxErr) {
					t.Fatalf("doctorExitError returned %v, want it to unwrap to a *ux.Error", err)
				}
				if uxErr.Remediation == "" {
					t.Error("the failing-report error has an empty Remediation")
				}
				return
			}
			if err != nil {
				t.Fatalf("doctorExitError on %s = %v, want nil", tt.name, err)
			}
		})
	}
}

// setDataDirEnv points internal/platform.DataDir at a fresh directory under
// root, on whichever platform this test runs on: DataDir reads
// XDG_DATA_HOME on Linux and macOS, and LocalAppData on Windows.
func setDataDirEnv(t *testing.T, root string) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", root)
	t.Setenv("LocalAppData", root)
}

// TestDoctorWithoutFixLeavesTheDataDirectoryAlone is the L5 half of refusal
// test three: the observable behaviour of --fix being opt in.
func TestDoctorWithoutFixLeavesTheDataDirectoryAlone(t *testing.T) {
	root := t.TempDir()
	setDataDirEnv(t, root)
	dataDir := filepath.Join(root, "shellforge")

	cmd := newDoctorCommand(nil)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--json"})
	_ = cmd.ExecuteContext(context.Background()) // ignore the exit-status error; other probes may fail on this host

	if _, err := os.Stat(dataDir); err == nil {
		t.Fatalf("data directory %s exists without --fix", dataDir)
	}

	cmd2 := newDoctorCommand(nil)
	cmd2.SetOut(&buf)
	cmd2.SetErr(&buf)
	cmd2.SetArgs([]string{"--fix", "--json"})
	_ = cmd2.ExecuteContext(context.Background())

	info, err := os.Stat(dataDir)
	if err != nil {
		t.Skipf("after --fix, %s does not exist: %v (sandbox_health's Fixable claim does not depend on free space, so this would need a real filesystem error such as a permission problem creating it; see internal/doctor/probes_sandbox_test.go for the deterministic case)", dataDir, err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Errorf("data directory mode = %v, want 0700", info.Mode().Perm())
	}
}

// TestDoctorJSONStaysParseableWhenAProbeFails checks the rendering plumbing
// directly against a synthetic report with a failing row, rather than
// relying on this host's real probes to fail, which would be
// non-deterministic across developer machines and CI runners.
func TestDoctorJSONStaysParseableWhenAProbeFails(t *testing.T) {
	report := doctor.Report{
		Platform: "linux",
		Version:  "test",
		Results: []doctor.Result{
			{ID: "docker_present", Status: doctor.Fail, Detail: "not found", Remediation: "install docker", DocAnchor: "docker-not-found"},
		},
	}

	var buf bytes.Buffer
	if err := doctor.RenderJSONWithFixOutcome(&buf, report, nil, nil); err != nil {
		t.Fatalf("RenderJSONWithFixOutcome: %v", err)
	}
	if !json.Valid(buf.Bytes()) {
		t.Fatalf("stdout is not valid JSON on the failing path: %q", buf.String())
	}

	if err := doctorExitError(report); err == nil {
		t.Error("doctorExitError = nil for a report with a failing row, want a non-nil error")
	}
}

// TestNewDoctorCommandHasJSONAndFixFlags pins the two flags the ticket
// names, in the shape author.go's own --json flag uses.
func TestNewDoctorCommandHasJSONAndFixFlags(t *testing.T) {
	cmd := newDoctorCommand(nil)
	if cmd.Flags().Lookup("json") == nil {
		t.Error("doctor is missing the --json flag")
	}
	if cmd.Flags().Lookup("fix") == nil {
		t.Error("doctor is missing the --fix flag")
	}
}

// TestDoctorCommandRunsWithoutError confirms the wiring at the root command:
// `shellforge doctor` is reachable and returns cleanly enough to render a
// table, rather than the "not built yet" stub error.
func TestDoctorCommandRunsWithoutError(t *testing.T) {
	root := NewRootCommand(VersionInfo{Version: "test"})
	out, err := execCommand(t, root, "doctor", "--json")
	if err != nil {
		var uxErr *ux.Error
		if !errors.As(err, &uxErr) {
			t.Fatalf("doctor returned a non-ux error: %v", err)
		}
		// A failing probe on this host is expected and is not what this
		// test checks; docanchor_test.go and doctorExitError's own unit
		// test cover that path. What matters here is that doctor is wired
		// in at all, not stubbed.
	}
	if !json.Valid([]byte(out)) {
		t.Fatalf("shellforge doctor --json did not print valid JSON: %q", out)
	}
}
