package setup

import (
	"context"

	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

// execCall is one recorded Exec invocation, in the order it happened.
type execCall struct {
	argv []string
	opts runtime.ExecOpts
}

// execAnswer is a canned response for the first recorded call whose argv
// satisfies match.
type execAnswer struct {
	match  func(argv []string) bool
	result runtime.ExecResult
	err    error
}

// fakeSession is a recording runtime.Session. It never talks to a real
// sandbox: every test in this package drives a Runner against one of these
// instead of a live container, which is what lets the whole suite run with
// no Docker daemon.
//
// It lives in a _test.go file, in package setup rather than a separate
// importable package, on purpose: internal/verify/verifytest had to be a
// real package and needed its own test guarding that nothing shipped ever
// imports it, because its Session panics on three of four methods. Keeping
// this fake in a test file removes that whole class of problem for this
// package.
type fakeSession struct {
	execCalls []execCall
	pushCalls []runtime.FileManifest

	// events is a single chronological log spanning both Exec and
	// PushFiles, for tests that assert relative ordering across the two
	// kinds of call. An Exec call logs "argv[0] argv[1]" (or just argv[0]
	// when there is no second element); a PushFiles call logs "push".
	events []string

	answers []execAnswer

	// pushErr, when set, is returned by every PushFiles call.
	pushErr error
}

func (f *fakeSession) logEvent(argv []string) {
	switch {
	case len(argv) >= 2:
		f.events = append(f.events, argv[0]+" "+argv[1])
	case len(argv) == 1:
		f.events = append(f.events, argv[0])
	default:
		f.events = append(f.events, "")
	}
}

// when registers a canned answer for the first call whose argv satisfies
// match. Answers are consulted in registration order; the first match wins.
func (f *fakeSession) when(match func(argv []string) bool, result runtime.ExecResult, err error) {
	f.answers = append(f.answers, execAnswer{match: match, result: result, err: err})
}

// whenArgvEquals is a convenience wrapper around when for an exact argv
// match.
func (f *fakeSession) whenArgvEquals(argv []string, result runtime.ExecResult, err error) {
	want := append([]string(nil), argv...)
	f.when(func(got []string) bool { return equalArgv(got, want) }, result, err)
}

func equalArgv(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// argvHasPrefix reports whether argv's first element equals name. It is
// mostly used by tests asserting that no destructive command was ever
// recorded.
func argvHasPrefix(argv []string, name string) bool {
	return len(argv) > 0 && argv[0] == name
}

// countArgvPrefix counts recorded calls whose argv[0] equals name.
func (f *fakeSession) countArgvPrefix(name string) int {
	n := 0
	for _, c := range f.execCalls {
		if argvHasPrefix(c.argv, name) {
			n++
		}
	}
	return n
}

// Exec records argv and opts, then answers from the scripted table. Absent a
// scripted match, "readlink -m -- X" answers with X on stdout (the sensible
// default for a level root that has not been created yet), and anything
// else answers exit 0 with no output.
func (f *fakeSession) Exec(_ context.Context, argv []string, opts runtime.ExecOpts) (runtime.ExecResult, error) {
	f.execCalls = append(f.execCalls, execCall{argv: append([]string(nil), argv...), opts: opts})
	f.logEvent(argv)

	for _, a := range f.answers {
		if a.match(argv) {
			return a.result, a.err
		}
	}
	if len(argv) >= 2 && argv[0] == "readlink" && argv[1] == "-m" {
		target := argv[len(argv)-1]
		return runtime.ExecResult{ExitCode: 0, Stdout: []byte(target)}, nil
	}
	return runtime.ExecResult{ExitCode: 0}, nil
}

// PushFiles records the manifest and returns the scripted error, if any.
func (f *fakeSession) PushFiles(_ context.Context, m runtime.FileManifest) error {
	f.pushCalls = append(f.pushCalls, m)
	f.events = append(f.events, "push")
	return f.pushErr
}

// Attach is never called by this package: setup and teardown never start an
// interactive process.
func (f *fakeSession) Attach(context.Context, runtime.AttachOpts) (runtime.PTY, error) {
	panic("fakeSession: Attach is not used by internal/content/setup")
}

// PullFile is never called by this package.
func (f *fakeSession) PullFile(context.Context, string) ([]byte, error) {
	panic("fakeSession: PullFile is not used by internal/content/setup")
}

// Close is never called by this package: the caller that owns the Session
// closes it, not the Runner.
func (f *fakeSession) Close() error {
	panic("fakeSession: Close is not used by internal/content/setup")
}

var _ runtime.Session = (*fakeSession)(nil)
