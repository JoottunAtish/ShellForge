package verify

import (
	"context"
	"strings"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/verify/verifytest"
)

func init() {
	markCovered("script")
}

func TestScriptCheck(t *testing.T) {
	t.Run("pass: exit 0", func(t *testing.T) {
		c := mustFactory(t, "script", Spec{ID: "o", OnFail: "job did not run", Params: map[string]any{"run": "test -s /var/log/nightly.log"}})
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{ExitCode: 0}, nil))
		if r := c.Run(context.Background(), Env{Session: sess}); r.Status != StatusPass {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("fail: non-zero exit appends stdout to on_fail", func(t *testing.T) {
		c := mustFactory(t, "script", Spec{ID: "o", OnFail: "the nightly job still isn't producing output.", Params: map[string]any{"run": "exit 1"}})
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{ExitCode: 1, Stdout: []byte("job ran but wrote no log")}, nil))
		r := c.Run(context.Background(), Env{Session: sess})
		if r.Status != StatusFail {
			t.Fatalf("got %+v", r)
		}
		if !strings.Contains(r.Message, "the nightly job still isn't producing output.") || !strings.Contains(r.Message, "job ran but wrote no log") {
			t.Fatalf("Message %q does not contain both the authored on_fail and the script's stdout", r.Message)
		}
	})

	t.Run("fail: non-zero exit with no stdout uses only the authored on_fail", func(t *testing.T) {
		c := mustFactory(t, "script", Spec{ID: "o", OnFail: "still broken", Params: map[string]any{"run": "exit 1"}})
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{ExitCode: 1}, nil))
		r := c.Run(context.Background(), Env{Session: sess})
		if r.Message != "still broken" {
			t.Fatalf("got message %q, want exactly the authored on_fail", r.Message)
		}
	})

	t.Run("error: Exec fails outright", func(t *testing.T) {
		c := mustFactory(t, "script", Spec{ID: "o", OnFail: "x", Params: map[string]any{"run": "true"}})
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{}, context.Canceled))
		if r := c.Run(context.Background(), Env{Session: sess}); r.Status != StatusError {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("default user is learner", func(t *testing.T) {
		c := mustFactory(t, "script", Spec{ID: "o", OnFail: "x", Params: map[string]any{"run": "true"}})
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{ExitCode: 0}, nil))
		c.Run(context.Background(), Env{Session: sess})
		if got := sess.Calls()[0].Opts.User; got != "learner" {
			t.Fatalf("default as_user: got %q, want %q", got, "learner")
		}
	})

	t.Run("root is honoured when explicitly requested", func(t *testing.T) {
		c := mustFactory(t, "script", Spec{ID: "o", OnFail: "x", Params: map[string]any{"run": "true", "as_user": "root"}})
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{ExitCode: 0}, nil))
		c.Run(context.Background(), Env{Session: sess})
		if got := sess.Calls()[0].Opts.User; got != "root" {
			t.Fatalf("got %q, want %q", got, "root")
		}
	})

	t.Run("the script body is one argv element to bash -c, never concatenated with anything else", func(t *testing.T) {
		body := `cd /opt/atlas && ./run.sh >/dev/null 2>&1 || exit 1; echo "$(whoami); rm -rf /"`
		c := mustFactory(t, "script", Spec{ID: "o", OnFail: "x", Params: map[string]any{"run": body}})
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{ExitCode: 0}, nil))
		c.Run(context.Background(), Env{Session: sess})

		argv := sess.Calls()[0].Argv
		if len(argv) != 3 || argv[0] != "bash" || argv[1] != "-c" || argv[2] != body {
			t.Fatalf("argv = %#v, want exactly [\"bash\" \"-c\" body] with the body byte-identical and unsplit", argv)
		}
	})

	// --- as_user refusal tests, required individually named ---

	t.Run("refuses as_user: --privileged", func(t *testing.T) {
		if _, err := newScriptCheck(Spec{ID: "o", Params: map[string]any{"run": "true", "as_user": "--privileged"}}); err == nil {
			t.Fatal("expected a Factory error for as_user: --privileged")
		}
	})

	t.Run("refuses as_user: -u root", func(t *testing.T) {
		if _, err := newScriptCheck(Spec{ID: "o", Params: map[string]any{"run": "true", "as_user": "-u root"}}); err == nil {
			t.Fatal("expected a Factory error for as_user: -u root")
		}
	})

	t.Run("refuses as_user: root learner", func(t *testing.T) {
		if _, err := newScriptCheck(Spec{ID: "o", Params: map[string]any{"run": "true", "as_user": "root learner"}}); err == nil {
			t.Fatal("expected a Factory error for as_user: root learner")
		}
	})

	t.Run("refuses an explicitly empty as_user", func(t *testing.T) {
		// This is the as_user key present in Params with an empty string
		// value, not the key entirely absent. An absent key is the normal
		// "use the learner default" path and is covered by "default user
		// is learner" above; this case is what the ticket's fourth
		// refusal row names.
		if _, err := newScriptCheck(Spec{ID: "o", Params: map[string]any{"run": "true", "as_user": ""}}); err == nil {
			t.Fatal("expected a Factory error for an explicitly empty as_user")
		}
	})

	t.Run("factory: missing run param", func(t *testing.T) {
		if _, err := newScriptCheck(Spec{ID: "o", Params: map[string]any{}}); err == nil {
			t.Fatal("expected an error for a missing run param")
		}
	})

	t.Run("factory: as_user of the wrong type", func(t *testing.T) {
		if _, err := newScriptCheck(Spec{ID: "o", Params: map[string]any{"run": "true", "as_user": 5}}); err == nil {
			t.Fatal("expected an error when as_user is not a string")
		}
	})
}
