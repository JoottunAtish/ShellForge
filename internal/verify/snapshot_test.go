package verify

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/verify/verifytest"
)

// joinNulRecords builds the byte form `env -0` writes: each record
// NUL-terminated, records concatenated with no separator between them.
func joinNulRecords(records ...string) []byte {
	var out []byte
	for _, r := range records {
		out = append(out, []byte(r)...)
		out = append(out, 0)
	}
	return out
}

func TestSnapshotsEnv(t *testing.T) {
	t.Run("pass, ordinary values", func(t *testing.T) {
		raw := joinNulRecords("FOO=bar", "PWD=/home/learner/quest")
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{Stdout: raw, ExitCode: 0}, nil))
		got, err := Snapshots{Dir: "/opt/shellforge/state"}.Env(context.Background(), sess)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got["FOO"] != "bar" || got["PWD"] != "/home/learner/quest" {
			t.Fatalf("got %#v", got)
		}
	})

	t.Run("value with an embedded newline", func(t *testing.T) {
		raw := joinNulRecords("MULTILINE=line one\nline two", "AFTER=ok")
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{Stdout: raw, ExitCode: 0}, nil))
		got, err := Snapshots{Dir: "/s"}.Env(context.Background(), sess)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got["MULTILINE"] != "line one\nline two" {
			t.Fatalf("embedded newline was not preserved: got %q", got["MULTILINE"])
		}
		if got["AFTER"] != "ok" {
			t.Fatalf("record after an embedded newline was not parsed: got %#v", got)
		}
	})

	t.Run("value containing an equals sign", func(t *testing.T) {
		raw := joinNulRecords("EXPR=a=b=c")
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{Stdout: raw, ExitCode: 0}, nil))
		got, err := Snapshots{Dir: "/s"}.Env(context.Background(), sess)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got["EXPR"] != "a=b=c" {
			t.Fatalf("split on first '=' only failed: got %q", got["EXPR"])
		}
	})

	t.Run("missing snapshot: cat reports no such file", func(t *testing.T) {
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{
			ExitCode: 1,
			Stderr:   []byte("cat: /opt/shellforge/state/env.snapshot: No such file or directory\n"),
		}, nil))
		_, err := Snapshots{Dir: "/opt/shellforge/state"}.Env(context.Background(), sess)
		if !errors.Is(err, ErrSnapshotMissing) {
			t.Fatalf("got %v, want an error wrapping ErrSnapshotMissing", err)
		}
	})

	t.Run("missing snapshot: Exec itself errors", func(t *testing.T) {
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{}, context.Canceled))
		_, err := Snapshots{Dir: "/s"}.Env(context.Background(), sess)
		if !errors.Is(err, ErrSnapshotMissing) {
			t.Fatalf("got %v, want an error wrapping ErrSnapshotMissing", err)
		}
	})
}

func TestSnapshotsCwd(t *testing.T) {
	t.Run("pass", func(t *testing.T) {
		raw := joinNulRecords("PWD=/home/learner/quest/logs", "FOO=bar")
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{Stdout: raw, ExitCode: 0}, nil))
		got, err := Snapshots{Dir: "/s"}.Cwd(context.Background(), sess)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "/home/learner/quest/logs" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("PWD absent from an otherwise readable snapshot", func(t *testing.T) {
		raw := joinNulRecords("FOO=bar")
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{Stdout: raw, ExitCode: 0}, nil))
		_, err := Snapshots{Dir: "/s"}.Cwd(context.Background(), sess)
		if !errors.Is(err, ErrSnapshotMissing) {
			t.Fatalf("got %v, want an error wrapping ErrSnapshotMissing", err)
		}
	})

	t.Run("missing snapshot propagates from Env", func(t *testing.T) {
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{
			ExitCode: 1,
			Stderr:   []byte("cat: /s/env.snapshot: No such file or directory\n"),
		}, nil))
		_, err := Snapshots{Dir: "/s"}.Cwd(context.Background(), sess)
		if !errors.Is(err, ErrSnapshotMissing) {
			t.Fatalf("got %v, want an error wrapping ErrSnapshotMissing", err)
		}
	})
}

// TestSnapshotErrorMessageDoesNotBlameTheLearner pins the wording checks
// that depend on Snapshots surface: it must read as "nothing to check yet",
// never as "you got it wrong".
func TestSnapshotErrorMessageDoesNotBlameTheLearner(t *testing.T) {
	msg := snapshotMissingMessage
	blaming := []string{"wrong", "fail", "incorrect", "you got it wrong"}
	lower := strings.ToLower(msg)
	for _, word := range blaming {
		if strings.Contains(lower, word) {
			t.Fatalf("snapshotMissingMessage contains %q, which reads as blaming the learner: %q", word, msg)
		}
	}
	if msg == "" {
		t.Fatal("snapshotMissingMessage must not be empty")
	}
}
