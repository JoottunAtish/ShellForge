package verify

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/verify/verifytest"
)

// The purity test. doc.go's first invariant is that checks are read-only, and a
// check that mutates sandbox state is a bug.
//
// This file is the hermetic half of that guarantee: it needs no Docker and runs
// on every `go test ./...`, so it cannot rot the way a test gated behind a
// daemon can. It works by inspecting what every check actually asks the sandbox
// to do, through the fake Session, and asserting that none of it can change
// anything.
//
// The other half is a filesystem hash across a real check run, which lives with
// the golden harness because it needs a container, a level file and
// internal/content, none of which this package may have. The two are
// complementary rather than redundant: a hash catches a mutation this file's
// allowlist did not anticipate, and this file catches one on a code path a
// particular level never exercises.
//
// Relationship to argv_injection_test.go, which covers overlapping ground for a
// different reason: that file drives the same checks with a deliberately hostile
// path to prove the value is never split or interpreted. Its fixture path
// contains the literal text "rm -rf", so reusing its cases here would make the
// mutating-command scan below either trip on its own fixture or need an
// exception carved out of the exact assertion that matters. These cases use
// benign paths instead, and TestPurityCoversEveryCheckThatTouchesTheSandbox
// keeps the two lists from drifting apart in coverage.

// readOnlyCommands is every command this package may ever run in the sandbox.
//
// An allowlist rather than a denylist, deliberately: a denylist of mutating
// commands is a guess about what a future edit might reach for, and the set of
// things that can write to a filesystem is not enumerable. This list is short
// because verification only ever reads, and adding to it should feel like a
// decision.
var readOnlyCommands = map[string]string{
	"stat":      "reads metadata",
	"cat":       "reads content",
	"find":      "walks a tree; TestFindIsInvokedWithReadOnlyPredicates bounds the predicates it may use",
	"readlink":  "reads a symlink target",
	"ps":        "lists processes",
	"sha256sum": "hashes content, used only under find -exec",
}

// mutatingTokens are commands and flags that write. Their presence anywhere in
// an argv this package builds is a bug, and the list exists to make the failure
// message name the offender rather than saying an allowlist was violated.
//
// This is the denylist half, and it is a diagnostic rather than the guarantee:
// the guarantee is the allowlist above. Keeping both means a new mutating call
// fails two assertions with two different explanations.
var mutatingTokens = []string{
	"rm", "rmdir", "mv", "cp", "chmod", "chown", "chgrp", "ln", "install",
	"tee", "truncate", "dd", "mkdir", "touch", "sed", "shred", "unlink",
	"-delete", "-execdir", "-ok", "-okdir", "-fls", "-fprint", "-fprintf",
	">", ">>",
}

// purityCase is one check driven with valid, benign parameters.
type purityCase struct {
	name     string
	typeName string
	params   map[string]any
	steps    []verifytest.Step
}

// purityCases covers every registered check type that reaches Session.Exec,
// except script.
//
// script is exempt by design and by necessity: its whole body is one argv
// element handed to bash -c, so it is the one check whose contents this package
// cannot reason about. A level author can write a mutating script, and the level
// authoring rules say they must not; what polices that is the filesystem hash in
// the golden harness, not an allowlist here. TestScriptCheckIsTheOnlyExemption
// pins the exemption so it stays one check rather than a category.
//
// command_matched and command_not_matched are absent because they never call
// Exec at all: they read the journal in Go. TestJournalChecksNeverTouchTheSandbox
// asserts that rather than leaving it as a comment.
func purityCases() []purityCase {
	const benignPath = "/home/learner/quest/report.txt"
	const benignDir = "/home/learner/quest/logs"

	statFile := runtime.ExecResult{ExitCode: 0, Stdout: []byte("regular file|644|learner|learner\n")}
	statDir := runtime.ExecResult{ExitCode: 0, Stdout: []byte("directory|755|learner|learner\n")}
	envSnapshot := runtime.ExecResult{ExitCode: 0, Stdout: joinNulRecords("ATLAS_ENV=production", "PWD="+benignDir)}

	return []purityCase{
		{
			name: "file_exists", typeName: "file_exists",
			params: map[string]any{"path": benignPath},
			steps:  []verifytest.Step{verifytest.Const(statFile, nil)},
		},
		{
			name: "file_absent", typeName: "file_absent",
			params: map[string]any{"path": benignPath},
			steps: []verifytest.Step{verifytest.Const(
				runtime.ExecResult{ExitCode: 1, Stderr: []byte("No such file or directory")}, nil)},
		},
		{
			name: "file_content", typeName: "file_content",
			params: map[string]any{"path": benignPath, "match": "trimmed_equals", "value": "147"},
			steps:  []verifytest.Step{verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("147\n")}, nil)},
		},
		{
			name: "dir_exists", typeName: "dir_exists",
			params: map[string]any{"path": benignDir},
			steps:  []verifytest.Step{verifytest.Const(statDir, nil)},
		},
		{
			name: "dir_tree", typeName: "dir_tree",
			params: map[string]any{"path": benignDir, "compare_to": benignDir + ".bak", "mode": "names_and_hashes"},
			steps: []verifytest.Step{
				verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("a.log\n")}, nil),
				verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("a.log\n")}, nil),
				verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("abc  /x/a.log\n")}, nil),
				verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("abc  /y/a.log\n")}, nil),
			},
		},
		{
			name: "file_mode", typeName: "file_mode",
			params: map[string]any{"path": benignPath, "mode": "0644"},
			steps:  []verifytest.Step{verifytest.Const(statFile, nil)},
		},
		{
			name: "file_owner", typeName: "file_owner",
			params: map[string]any{"path": benignPath, "owner": "learner"},
			steps:  []verifytest.Step{verifytest.Const(statFile, nil)},
		},
		{
			name: "symlink_target", typeName: "symlink_target",
			params: map[string]any{"path": benignDir + "/current", "target": "/home/learner/quest/v2"},
			steps: []verifytest.Step{
				verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("symbolic link|777|learner|learner\n")}, nil),
				verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("/home/learner/quest/v2\n")}, nil),
			},
		},
		{
			name: "env_var", typeName: "env_var",
			params: map[string]any{"name": "ATLAS_ENV", "value": "production"},
			steps:  []verifytest.Step{verifytest.Const(envSnapshot, nil)},
		},
		{
			name: "cwd_is", typeName: "cwd_is",
			params: map[string]any{"path": benignDir},
			steps:  []verifytest.Step{verifytest.Const(envSnapshot, nil)},
		},
		{
			name: "process_running", typeName: "process_running",
			params: map[string]any{"pattern": `heartbeat\.sh`},
			steps: []verifytest.Step{verifytest.Const(
				runtime.ExecResult{ExitCode: 0, Stdout: []byte("  1 /bin/bash\n 42 /usr/local/bin/heartbeat.sh\n")}, nil)},
		},
	}
}

// runPurityCase builds the case's check through the real registry and returns
// every call it made to the sandbox.
func runPurityCase(t *testing.T, tc purityCase) []verifytest.ExecCall {
	t.Helper()

	check := mustFactory(t, tc.typeName, Spec{
		ID: "obj1", Text: "an objective", OnFail: "an authored on_fail", Params: tc.params,
	})

	sess := verifytest.NewSession(tc.steps...)
	check.Run(context.Background(), Env{
		Session:   sess,
		Root:      "/home/learner/quest",
		Snapshots: Snapshots{Dir: "/home/learner/.shellforge"},
		LevelID:   "purity-01",
	})
	return sess.Calls()
}

// TestChecksOnlyRunReadOnlyCommands is the core purity assertion: every command
// a check runs in the sandbox is on the read-only allowlist.
func TestChecksOnlyRunReadOnlyCommands(t *testing.T) {
	for _, tc := range purityCases() {
		t.Run(tc.name, func(t *testing.T) {
			calls := runPurityCase(t, tc)
			if len(calls) == 0 {
				t.Fatal("the check made no Exec calls, so this case asserts nothing; fix the fixture")
			}

			for _, call := range calls {
				if len(call.Argv) == 0 {
					t.Fatal("an Exec call was made with an empty argv")
				}
				command := call.Argv[0]
				if _, ok := readOnlyCommands[command]; !ok {
					t.Errorf("runs %q, which is not on the read-only allowlist.\n"+
						"A check may only read. If this command genuinely reads and nothing else, "+
						"add it to readOnlyCommands with a note saying what it reads.\nfull argv: %q",
						command, call.Argv)
				}
			}
		})
	}
}

// TestChecksNeverNameAMutatingCommand is the denylist diagnostic. It overlaps
// the allowlist above on purpose: a new mutating call then fails twice, once
// saying it is not allowed and once naming what it would have done.
//
// It scans every argv element, not just the command, because find can be handed
// a mutating predicate and -exec can carry a whole second command.
func TestChecksNeverNameAMutatingCommand(t *testing.T) {
	for _, tc := range purityCases() {
		t.Run(tc.name, func(t *testing.T) {
			for _, call := range runPurityCase(t, tc) {
				for _, arg := range call.Argv {
					for _, token := range mutatingTokens {
						if arg == token {
							t.Errorf("argv contains %q, which writes.\nfull argv: %q", token, call.Argv)
						}
					}
				}
			}
		})
	}
}

// TestFindIsInvokedWithReadOnlyPredicates guards the one command on the
// allowlist that can be turned into a mutating one without changing argv[0].
//
// `find -delete` removes files, and `find -exec` runs anything at all. dir_tree
// uses find twice today, once with -printf and once with -exec sha256sum, so the
// -exec door is already open and what keeps it safe is which command goes
// through it.
func TestFindIsInvokedWithReadOnlyPredicates(t *testing.T) {
	sawFind := false

	for _, tc := range purityCases() {
		for _, call := range runPurityCase(t, tc) {
			if len(call.Argv) == 0 || call.Argv[0] != "find" {
				continue
			}
			sawFind = true

			for i, arg := range call.Argv {
				switch arg {
				case "-delete", "-execdir", "-ok", "-okdir":
					t.Errorf("%s: find is invoked with the mutating or interactive predicate %q: %q", tc.name, arg, call.Argv)
				case "-exec":
					if i+1 >= len(call.Argv) {
						t.Errorf("%s: find ends with -exec and no command: %q", tc.name, call.Argv)
						continue
					}
					executed := call.Argv[i+1]
					if _, ok := readOnlyCommands[executed]; !ok {
						t.Errorf("%s: find -exec runs %q, which is not on the read-only allowlist: %q",
							tc.name, executed, call.Argv)
					}
				}
			}
		}
	}

	if !sawFind {
		t.Fatal("no case ran find, so this test asserted nothing; dir_tree should have")
	}
}

// TestChecksNeverWriteThroughTheSession asserts the three Session methods that
// could change sandbox state are never called.
//
// verifytest.Session panics on PushFiles, PullFile, Attach and Close, so this
// passes by the checks simply not calling them. The test is here anyway, rather
// than left implicit, because the panic only fires if some test happens to
// exercise the path; naming the guarantee means a future fake that returned an
// error instead of panicking would not quietly weaken it.
func TestChecksNeverWriteThroughTheSession(t *testing.T) {
	for _, tc := range purityCases() {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if v := recover(); v != nil {
					t.Fatalf("a check called a Session method beyond Exec: %v", v)
				}
			}()
			runPurityCase(t, tc)
		})
	}
}

// TestRunningTwiceIssuesTheSameCallsIsIdempotent is the "checks never mutate"
// property stated as repeatability: if a check changed something, the second run
// would be looking at a different world.
//
// It compares the argv sequences of two independent runs. Identical sequences
// mean the check carries no state between runs and asks the same questions every
// time, which is what makes a learner's repeated `check` meaningful.
func TestRunningTwiceIssuesTheSameCalls(t *testing.T) {
	for _, tc := range purityCases() {
		t.Run(tc.name, func(t *testing.T) {
			first := argvSequence(runPurityCase(t, tc))
			second := argvSequence(runPurityCase(t, tc))

			if !reflect.DeepEqual(first, second) {
				t.Errorf("two runs asked the sandbox different things, so the check carries state across runs.\nfirst:  %q\nsecond: %q",
					first, second)
			}
		})
	}
}

// TestJournalChecksNeverTouchTheSandbox asserts the two journal check types make
// no Exec call at all. They answer from the journal in Go, and a journal check
// that reached into the sandbox would be reading state to decide something the
// journal is supposed to decide.
func TestJournalChecksNeverTouchTheSandbox(t *testing.T) {
	for _, typeName := range []string{"command_matched", "command_not_matched"} {
		t.Run(typeName, func(t *testing.T) {
			check := mustFactory(t, typeName, Spec{
				ID: "obj1", OnFail: "authored", Optional: true,
				Params: map[string]any{"pattern": `grep`, "scope": "level"},
			})

			// A Session with no scripted steps panics on the first Exec, which
			// is exactly the assertion: reaching Exec at all is the failure.
			sess := verifytest.NewSession()
			defer func() {
				if v := recover(); v != nil {
					t.Fatalf("%s called Session.Exec; journal checks read the journal, not the sandbox: %v", typeName, v)
				}
			}()

			check.Run(context.Background(), Env{
				Session: sess,
				Journal: fixedJournal{commands: []string{"grep -ri ERROR ."}},
			})

			if calls := sess.Calls(); len(calls) != 0 {
				t.Errorf("%s made %d Exec calls, want none: %#v", typeName, len(calls), calls)
			}
		})
	}
}

// fixedJournal is a JournalReader returning a fixed command list.
type fixedJournal struct {
	commands []string
}

func (f fixedJournal) Commands(Scope) []string { return f.commands }

// TestScriptCheckIsTheOnlyExemption pins the boundary of the exemption above.
//
// script is exempt from the allowlist because its body is opaque to this
// package. That exemption has to stay exactly one check wide: if a second check
// type started running bash -c with author-supplied content, the allowlist would
// be guarding nothing and nobody would notice, because the allowlist itself
// would still pass.
func TestScriptCheckIsTheOnlyExemption(t *testing.T) {
	check := mustFactory(t, "script", Spec{
		ID: "obj1", OnFail: "authored",
		Params: map[string]any{"run": "test -s /home/learner/quest/report.txt", "as_user": "learner"},
	})

	sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{ExitCode: 0}, nil))
	check.Run(context.Background(), Env{Session: sess, Root: "/home/learner/quest"})

	calls := sess.Calls()
	if len(calls) != 1 {
		t.Fatalf("script made %d Exec calls, want exactly 1", len(calls))
	}
	argv := calls[0].Argv
	if len(argv) != 3 || argv[0] != "bash" || argv[1] != "-c" {
		t.Fatalf("script argv = %q, want exactly [bash -c <body>]: the body must be one argv element, never concatenated into a command line", argv)
	}

	// And the exemption's own boundary: no other check type may use bash.
	for _, tc := range purityCases() {
		for _, call := range runPurityCase(t, tc) {
			if len(call.Argv) > 0 && (call.Argv[0] == "bash" || call.Argv[0] == "sh") {
				t.Errorf("%s runs a shell (%q); only the script check may, and its body is the author's own", tc.name, call.Argv)
			}
		}
	}
}

// TestPurityCoversEveryCheckThatTouchesTheSandbox keeps this file honest as the
// catalogue grows.
//
// Types() is the authored catalogue. Every name in it must be accounted for
// here: covered by a purityCase, or listed in one of the two documented
// exemptions. A fifteenth check type added without a decision about its purity
// fails this test rather than shipping unexamined.
func TestPurityCoversEveryCheckThatTouchesTheSandbox(t *testing.T) {
	// Exempt with a reason, each stated where it is enforced.
	exempt := map[string]string{
		"script":              "body is opaque to this package; policed by the filesystem hash in the golden harness, and bounded by TestScriptCheckIsTheOnlyExemption",
		"command_matched":     "never calls Exec; asserted by TestJournalChecksNeverTouchTheSandbox",
		"command_not_matched": "never calls Exec; asserted by TestJournalChecksNeverTouchTheSandbox",
	}

	covered := map[string]bool{}
	for _, tc := range purityCases() {
		covered[tc.typeName] = true
	}

	var missing []string
	for _, typeName := range Types() {
		if covered[typeName] || exempt[typeName] != "" {
			continue
		}
		missing = append(missing, typeName)
	}
	sort.Strings(missing)

	if len(missing) > 0 {
		t.Errorf("registered check types with no purity coverage: %s.\n"+
			"Add a case to purityCases, or add an entry to the exempt map above saying which test polices it instead.",
			strings.Join(missing, ", "))
	}
}

// argvSequence flattens a call list to the argv vectors, which is what the
// repeatability comparison is about.
func argvSequence(calls []verifytest.ExecCall) [][]string {
	out := make([][]string, 0, len(calls))
	for _, call := range calls {
		out = append(out, call.Argv)
	}
	return out
}
