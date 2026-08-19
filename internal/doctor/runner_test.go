package doctor

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestExecRunnerReportsANonZeroExitThroughCode re-invokes the current test
// binary itself with an environment marker, so it needs nothing installed.
// On a platform where exec.LookPath cannot resolve the test binary it skips
// rather than failing.
func TestExecRunnerReportsANonZeroExitThroughCode(t *testing.T) {
	if os.Getenv("DOCTOR_RUNNER_TEST_HELPER") == "1" {
		os.Exit(7)
	}

	self, err := os.Executable()
	if err != nil {
		t.Skipf("cannot resolve the running test binary: %v", err)
	}
	if _, err := exec.LookPath(self); err != nil {
		t.Skipf("cannot resolve the test binary to re-invoke: %v", err)
	}

	r := newExecRunner()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	t.Setenv("DOCTOR_RUNNER_TEST_HELPER", "1")
	_, _, code, err := r.run(ctx, []string{self, "-test.run=^TestExecRunnerReportsANonZeroExitThroughCode$"}, nil)
	if err != nil {
		t.Fatalf("run returned an error for a non-zero exit: %v", err)
	}
	if code != 7 {
		t.Errorf("code = %d, want 7", code)
	}
}

// TestExecRunnerReportsBinaryNotFound checks the LookPath failure path,
// which is what a missing docker or wsl.exe looks like to a probe.
func TestExecRunnerReportsBinaryNotFound(t *testing.T) {
	r := newExecRunner()
	_, _, code, err := r.run(context.Background(), []string{"shellforge-doctor-nonexistent-binary-xyz"}, nil)
	if err == nil {
		t.Fatal("run with an unresolvable binary returned a nil error")
	}
	if code != -1 {
		t.Errorf("code = %d, want -1 for a binary that could not be started", code)
	}
}

// TestExecRunnerHonoursCancellation checks that a cancelled context stops
// the child rather than leaving it to run to completion.
func TestExecRunnerHonoursCancellation(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("no sleep binary on PATH")
	}
	r := newExecRunner()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, _, code, err := r.run(ctx, []string{"sleep", "5"}, nil)
	if err == nil {
		t.Fatal("run against a cancelled context returned a nil error")
	}
	if code != -1 {
		t.Errorf("code = %d, want -1 on cancellation", code)
	}
}
