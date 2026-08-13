package verify

import (
	"context"
	"strings"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/verify/verifytest"
)

// maliciousPath looks like a shell injection attempt: command substitution,
// a semicolon, and a redirection, none of which this package may ever
// interpret. It must reach Session.Exec as one untouched argv element.
const maliciousPath = "/tmp/$(rm -rf /); echo pwned > /etc/passwd"

// TestNoCheckBuildsAShellString asserts that every check whose Params flow
// into an argv element passes that value through unmodified, never split on
// whitespace and never handed to a shell for interpretation. Only the
// script check is exempt by design: its whole body is one argv element to
// bash -c, and that property is pinned separately in script_check_test.go.
func TestNoCheckBuildsAShellString(t *testing.T) {
	statOK := runtime.ExecResult{ExitCode: 0, Stdout: []byte("regular file|644|learner|learner\n")}

	tests := []struct {
		name     string
		typeName string
		params   map[string]any
		steps    []verifytest.Step
	}{
		{
			"file_exists", "file_exists",
			map[string]any{"path": maliciousPath},
			[]verifytest.Step{verifytest.Const(statOK, nil)},
		},
		{
			"file_absent", "file_absent",
			map[string]any{"path": maliciousPath},
			[]verifytest.Step{verifytest.Const(runtime.ExecResult{ExitCode: 1, Stderr: []byte("No such file or directory")}, nil)},
		},
		{
			"dir_exists", "dir_exists",
			map[string]any{"path": maliciousPath},
			[]verifytest.Step{verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("directory|755|learner|learner\n")}, nil)},
		},
		{
			"file_content", "file_content",
			map[string]any{"path": maliciousPath, "match": "contains", "value": "x"},
			[]verifytest.Step{verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("x")}, nil)},
		},
		{
			"file_mode", "file_mode",
			map[string]any{"path": maliciousPath, "mode": "0700"},
			[]verifytest.Step{verifytest.Const(statOK, nil)},
		},
		{
			"file_owner", "file_owner",
			map[string]any{"path": maliciousPath, "owner": "learner"},
			[]verifytest.Step{verifytest.Const(statOK, nil)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, ok := Lookup(tt.typeName)
			if !ok {
				t.Fatalf("no factory registered for %q", tt.typeName)
			}
			c, err := f(Spec{ID: "o", OnFail: "x", Params: tt.params})
			if err != nil {
				t.Fatalf("building check: %v", err)
			}
			sess := verifytest.NewSession(tt.steps...)
			c.Run(context.Background(), Env{Session: sess})

			found := false
			for _, call := range sess.Calls() {
				for _, arg := range call.Argv {
					if arg == maliciousPath {
						found = true
					}
					if arg != maliciousPath && (strings.Contains(arg, "rm -rf") || strings.Contains(arg, "echo pwned")) {
						t.Fatalf("argv element %q looks like a fragment of the malicious path, meaning it was split rather than passed whole: call %#v", arg, call)
					}
				}
			}
			if !found {
				t.Fatalf("no Exec call received %q as a single, untouched argv element; calls: %#v", maliciousPath, sess.Calls())
			}
		})
	}
}
