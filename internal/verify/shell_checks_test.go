package verify

import (
	"context"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/verify/verifytest"
)

func init() {
	markCovered("env_var", "cwd_is")
}

func runShellCheck(c Check, sess *verifytest.Session) Result {
	return c.Run(context.Background(), Env{Session: sess, Snapshots: Snapshots{Dir: "/home/learner/.shellforge"}})
}

func TestEnvVarCheck(t *testing.T) {
	t.Run("pass: set to the expected value", func(t *testing.T) {
		c := mustFactory(t, "env_var", Spec{ID: "o", OnFail: "not set", Params: map[string]any{"name": "ATLAS_ENV", "value": "production"}})
		raw := joinNulRecords("ATLAS_ENV=production", "PWD=/home/learner")
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: raw}, nil))
		if r := runShellCheck(c, sess); r.Status != StatusPass {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("fail: set to the wrong value", func(t *testing.T) {
		c := mustFactory(t, "env_var", Spec{ID: "o", OnFail: "wrong value", Params: map[string]any{"name": "ATLAS_ENV", "value": "production"}})
		raw := joinNulRecords("ATLAS_ENV=staging")
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: raw}, nil))
		if r := runShellCheck(c, sess); r.Status != StatusFail || r.Message != "wrong value" {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("fail: not set at all", func(t *testing.T) {
		c := mustFactory(t, "env_var", Spec{ID: "o", OnFail: "not set", Params: map[string]any{"name": "ATLAS_ENV"}})
		raw := joinNulRecords("PWD=/home/learner")
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: raw}, nil))
		if r := runShellCheck(c, sess); r.Status != StatusFail {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("pass: no value param, set to the empty string still counts as set", func(t *testing.T) {
		c := mustFactory(t, "env_var", Spec{ID: "o", OnFail: "not set", Params: map[string]any{"name": "ATLAS_ENV"}})
		raw := joinNulRecords("ATLAS_ENV=", "PWD=/home/learner")
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: raw}, nil))
		r := runShellCheck(c, sess)
		if r.Status != StatusPass {
			t.Fatalf("a variable exported empty must still count as set: got %+v", r)
		}
	})

	t.Run("error: snapshot missing, distinct from fail", func(t *testing.T) {
		c := mustFactory(t, "env_var", Spec{ID: "o", OnFail: "not set", Params: map[string]any{"name": "ATLAS_ENV"}})
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{
			ExitCode: 1, Stderr: []byte("cat: /home/learner/.shellforge/env.snapshot: No such file or directory\n"),
		}, nil))
		r := runShellCheck(c, sess)
		if r.Status != StatusError {
			t.Fatalf("got %+v, want StatusError, not StatusFail", r)
		}
		if r.Message != snapshotMissingMessage {
			t.Fatalf("got message %q, want the shared non-blaming snapshot message", r.Message)
		}
	})

	t.Run("factory: missing name param", func(t *testing.T) {
		if _, err := newEnvVarCheck(Spec{ID: "o", Params: map[string]any{}}); err == nil {
			t.Fatal("expected an error for a missing name param")
		}
	})
}

func TestCwdIsCheck(t *testing.T) {
	t.Run("pass", func(t *testing.T) {
		c := mustFactory(t, "cwd_is", Spec{ID: "o", OnFail: "wrong directory", Params: map[string]any{"path": "/home/learner/quest/logs"}})
		raw := joinNulRecords("PWD=/home/learner/quest/logs")
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: raw}, nil))
		if r := runShellCheck(c, sess); r.Status != StatusPass {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("fail", func(t *testing.T) {
		c := mustFactory(t, "cwd_is", Spec{ID: "o", OnFail: "wrong directory", Params: map[string]any{"path": "/home/learner/quest/logs"}})
		raw := joinNulRecords("PWD=/home/learner")
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: raw}, nil))
		if r := runShellCheck(c, sess); r.Status != StatusFail || r.Message != "wrong directory" {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("error: snapshot missing, distinct from fail", func(t *testing.T) {
		c := mustFactory(t, "cwd_is", Spec{ID: "o", OnFail: "wrong directory", Params: map[string]any{"path": "/home/learner"}})
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{
			ExitCode: 1, Stderr: []byte("cat: /home/learner/.shellforge/env.snapshot: No such file or directory\n"),
		}, nil))
		r := runShellCheck(c, sess)
		if r.Status != StatusError {
			t.Fatalf("got %+v, want StatusError, not StatusFail", r)
		}
	})

	t.Run("factory: missing path param", func(t *testing.T) {
		if _, err := newCwdIsCheck(Spec{ID: "o", Params: map[string]any{}}); err == nil {
			t.Fatal("expected an error for a missing path param")
		}
	})
}
