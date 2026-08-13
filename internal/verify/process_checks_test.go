package verify

import (
	"context"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/verify/verifytest"
)

func init() {
	markCovered("process_running")
}

const psOutput = "  PID COMMAND\n" +
	"    1 /sbin/init\n" +
	"  842 /usr/local/bin/heartbeat.sh --interval 5\n" +
	" 1200 bash\n"

func TestProcessRunningCheck(t *testing.T) {
	t.Run("pass: matches", func(t *testing.T) {
		c := mustFactory(t, "process_running", Spec{ID: "o", OnFail: "not running", Params: map[string]any{"pattern": `heartbeat\.sh`}})
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte(psOutput)}, nil))
		if r := c.Run(context.Background(), Env{Session: sess}); r.Status != StatusPass {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("fail: no match", func(t *testing.T) {
		c := mustFactory(t, "process_running", Spec{ID: "o", OnFail: "not running", Params: map[string]any{"pattern": `nonexistent-job`}})
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte(psOutput)}, nil))
		r := c.Run(context.Background(), Env{Session: sess})
		if r.Status != StatusFail || r.Message != "not running" {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("negate: pass because it is NOT running", func(t *testing.T) {
		c := mustFactory(t, "process_running", Spec{ID: "o", OnFail: "still running", Params: map[string]any{"pattern": `nonexistent-job`, "negate": true}})
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte(psOutput)}, nil))
		if r := c.Run(context.Background(), Env{Session: sess}); r.Status != StatusPass {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("negate: fail because it IS running", func(t *testing.T) {
		c := mustFactory(t, "process_running", Spec{ID: "o", OnFail: "still running", Params: map[string]any{"pattern": `heartbeat\.sh`, "negate": true}})
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte(psOutput)}, nil))
		r := c.Run(context.Background(), Env{Session: sess})
		if r.Status != StatusFail || r.Message != "still running" {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("error: ps itself fails", func(t *testing.T) {
		c := mustFactory(t, "process_running", Spec{ID: "o", OnFail: "x", Params: map[string]any{"pattern": `heartbeat\.sh`}})
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{ExitCode: 1, Stderr: []byte("ps: unrecognized option\n")}, nil))
		if r := c.Run(context.Background(), Env{Session: sess}); r.Status != StatusError {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("error: Exec fails outright", func(t *testing.T) {
		c := mustFactory(t, "process_running", Spec{ID: "o", OnFail: "x", Params: map[string]any{"pattern": `heartbeat\.sh`}})
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{}, context.Canceled))
		if r := c.Run(context.Background(), Env{Session: sess}); r.Status != StatusError {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("factory: invalid regex", func(t *testing.T) {
		if _, err := newProcessRunningCheck(Spec{ID: "o", Params: map[string]any{"pattern": "("}}); err == nil {
			t.Fatal("expected an error for an invalid regex pattern")
		}
	})

	t.Run("factory: missing pattern", func(t *testing.T) {
		if _, err := newProcessRunningCheck(Spec{ID: "o", Params: map[string]any{}}); err == nil {
			t.Fatal("expected an error for a missing pattern param")
		}
	})
}
