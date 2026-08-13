package verifytest

import (
	"context"
	"fmt"
	"sync"

	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

// Step answers one Session.Exec call.
type Step func(argv []string, opts runtime.ExecOpts) (runtime.ExecResult, error)

// Const returns a Step that ignores its argv and opts and always answers
// with result and err. Use it for a check that makes exactly one Exec call
// and the test does not need to inspect what was sent.
func Const(result runtime.ExecResult, err error) Step {
	return func([]string, runtime.ExecOpts) (runtime.ExecResult, error) {
		return result, err
	}
}

// ExecCall is one recorded Session.Exec invocation.
type ExecCall struct {
	Argv []string
	Opts runtime.ExecOpts
}

// Session is a fake runtime.Session. Exec answers from steps in order, one
// Step per call; a call past the end of steps panics, since that means the
// test under-scripted a check that made more Exec calls than expected.
//
// Attach, PushFiles, PullFile, and Close all panic unconditionally: no check
// in internal/verify calls any of them, and a check that started would be a
// bug this fake is built to surface immediately rather than paper over.
type Session struct {
	mu    sync.Mutex
	steps []Step
	calls []ExecCall
}

// NewSession returns a Session that answers successive Exec calls from
// steps, in order.
func NewSession(steps ...Step) *Session {
	return &Session{steps: steps}
}

// Exec implements runtime.Session.
func (s *Session) Exec(_ context.Context, argv []string, opts runtime.ExecOpts) (runtime.ExecResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls = append(s.calls, ExecCall{Argv: argv, Opts: opts})

	idx := len(s.calls) - 1
	if idx >= len(s.steps) {
		panic(fmt.Sprintf("verifytest: Exec call %d (argv %q) has no scripted Step; the test under-scripted this check", idx+1, argv))
	}
	return s.steps[idx](argv, opts)
}

// Calls returns every Exec call recorded so far, in order.
func (s *Session) Calls() []ExecCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ExecCall(nil), s.calls...)
}

// Attach always panics. No check in internal/verify calls it.
func (s *Session) Attach(context.Context, runtime.AttachOpts) (runtime.PTY, error) {
	panic("verifytest: Attach called; no verify check should ever attach an interactive session")
}

// PushFiles always panics. No check in internal/verify calls it: checks are
// read-only.
func (s *Session) PushFiles(context.Context, runtime.FileManifest) error {
	panic("verifytest: PushFiles called; verify checks are read-only and must never materialize files")
}

// PullFile always panics. No check in internal/verify calls it; every check
// reads sandbox content through Exec instead, matching component-traps'
// rule that a check observes state through Session.Exec.
func (s *Session) PullFile(context.Context, string) ([]byte, error) {
	panic("verifytest: PullFile called; verify checks read sandbox content through Exec, not PullFile")
}

// Close always panics. No check in internal/verify owns the Session's
// lifecycle.
func (s *Session) Close() error {
	panic("verifytest: Close called; no verify check should close the Session it was handed")
}

var _ runtime.Session = (*Session)(nil)
