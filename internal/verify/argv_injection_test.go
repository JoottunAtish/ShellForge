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

// argvCase is one check driven with a hostile path, shared by the two tests
// below: one asserts the value is never split or interpreted, the other that
// it can never be read as an option.
type argvCase struct {
	name     string
	typeName string
	params   map[string]any
	steps    []verifytest.Step

	// terminated is true when the command this check runs accepts "--" and
	// so must pass it immediately before the path operand. It is false only
	// for dir_tree: GNU find has no end-of-options terminator and fails on
	// "--" as an unknown predicate, so dir_tree requires an absolute path at
	// Factory time instead. See newDirTreeCheck.
	terminated bool
}

// argvCases covers every check whose Params flow into an argv element. Only
// the script check is exempt by design: its whole body is one argv element to
// bash -c, and that property is pinned separately in script_check_test.go.
//
// The eight filesystem checks are the whole of that set: env_var and cwd_is
// read a snapshot path derived from Snapshots.Dir rather than from Params,
// process_running runs a fixed `ps -eo pid,args` and matches its pattern in
// Go, and the two journal checks never call Exec at all. Adding a check that
// puts a Param into an argv means adding a case here.
func argvCases() []argvCase {
	statOK := runtime.ExecResult{ExitCode: 0, Stdout: []byte("regular file|644|learner|learner\n")}

	return []argvCase{
		{
			name: "file_exists", typeName: "file_exists",
			params: map[string]any{"path": maliciousPath},
			steps:  []verifytest.Step{verifytest.Const(statOK, nil)},
			// stat
			terminated: true,
		},
		{
			name: "file_absent", typeName: "file_absent",
			params: map[string]any{"path": maliciousPath},
			steps: []verifytest.Step{
				verifytest.Const(runtime.ExecResult{ExitCode: 1, Stderr: []byte("No such file or directory")}, nil),
			},
			terminated: true,
		},
		{
			name: "dir_exists", typeName: "dir_exists",
			params: map[string]any{"path": maliciousPath},
			steps: []verifytest.Step{
				verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("directory|755|learner|learner\n")}, nil),
			},
			terminated: true,
		},
		{
			name: "file_content", typeName: "file_content",
			params: map[string]any{"path": maliciousPath, "match": "contains", "value": "x"},
			steps: []verifytest.Step{
				verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("x")}, nil),
			},
			// cat
			terminated: true,
		},
		{
			name: "file_mode", typeName: "file_mode",
			params:     map[string]any{"path": maliciousPath, "mode": "0700"},
			steps:      []verifytest.Step{verifytest.Const(statOK, nil)},
			terminated: true,
		},
		{
			name: "file_owner", typeName: "file_owner",
			params:     map[string]any{"path": maliciousPath, "owner": "learner"},
			steps:      []verifytest.Step{verifytest.Const(statOK, nil)},
			terminated: true,
		},
		{
			// maliciousPath is absolute, so it satisfies dir_tree's
			// absolute-path requirement and still exercises the argv path.
			// Both sides list empty, so the names compare equal and the run
			// finishes without reaching the hashing branch.
			name: "dir_tree", typeName: "dir_tree",
			params: map[string]any{"path": maliciousPath, "compare_to": maliciousPath, "mode": "names_only"},
			steps: []verifytest.Step{
				verifytest.Const(runtime.ExecResult{ExitCode: 0}, nil),
				verifytest.Const(runtime.ExecResult{ExitCode: 0}, nil),
			},
			// find, which has no terminator.
			terminated: false,
		},
		{
			name: "symlink_target", typeName: "symlink_target",
			params: map[string]any{"path": maliciousPath, "target": "/somewhere"},
			steps: []verifytest.Step{
				verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("symbolic link|777|learner|learner\n")}, nil),
				verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("/somewhere\n")}, nil),
			},
			// stat, then readlink.
			terminated: true,
		},
	}
}

// runArgvCase builds the case's check, runs it against a fake Session, and
// returns the calls it made.
func runArgvCase(t *testing.T, tc argvCase) []verifytest.ExecCall {
	t.Helper()
	f, ok := Lookup(tc.typeName)
	if !ok {
		t.Fatalf("no factory registered for %q", tc.typeName)
	}
	c, err := f(Spec{ID: "o", OnFail: "x", Params: tc.params})
	if err != nil {
		t.Fatalf("building check: %v", err)
	}
	sess := verifytest.NewSession(tc.steps...)
	c.Run(context.Background(), Env{Session: sess})
	return sess.Calls()
}

// TestNoCheckBuildsAShellString asserts that every check whose Params flow
// into an argv element passes that value through unmodified, never split on
// whitespace and never handed to a shell for interpretation.
func TestNoCheckBuildsAShellString(t *testing.T) {
	for _, tt := range argvCases() {
		t.Run(tt.name, func(t *testing.T) {
			calls := runArgvCase(t, tt)

			found := false
			for _, call := range calls {
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
				t.Fatalf("no Exec call received %q as a single, untouched argv element; calls: %#v", maliciousPath, calls)
			}
		})
	}
}

// TestPathOperandsAreNotFlagShaped asserts the other half of the defence.
// Passing an argv vector stops a shell from interpreting a path, but it does
// nothing about a path that starts with a hyphen: stat, cat, and readlink
// would read "-f" or "--force" as an option rather than as an operand. Every
// path operand in this package is therefore preceded by "--", and the one
// command that has no such terminator, find, gets an absolute path enforced
// at Factory time instead.
func TestPathOperandsAreNotFlagShaped(t *testing.T) {
	for _, tt := range argvCases() {
		t.Run(tt.name, func(t *testing.T) {
			calls := runArgvCase(t, tt)

			for _, call := range calls {
				for i, arg := range call.Argv {
					if arg != maliciousPath {
						continue
					}
					if !tt.terminated {
						if contains(call.Argv, "--") {
							t.Fatalf("argv %q passes \"--\" to a command that has no end-of-options terminator", call.Argv)
						}
						continue
					}
					if i == 0 || call.Argv[i-1] != "--" {
						t.Fatalf("argv %q does not pass \"--\" immediately before the path operand, so a path beginning with a hyphen would be read as an option", call.Argv)
					}
				}
			}
		})
	}

	t.Run("dir_tree rejects a flag-shaped path at construction", func(t *testing.T) {
		f, ok := Lookup("dir_tree")
		if !ok {
			t.Fatal("no factory registered for dir_tree")
		}
		if _, err := f(Spec{ID: "o", OnFail: "x", Params: map[string]any{
			"path": "-delete", "compare_to": "/b", "mode": "names_only",
		}}); err == nil {
			t.Fatal("dir_tree accepted a flag-shaped path; find would read it as a predicate")
		}
	})
}

func contains(argv []string, want string) bool {
	for _, a := range argv {
		if a == want {
			return true
		}
	}
	return false
}
