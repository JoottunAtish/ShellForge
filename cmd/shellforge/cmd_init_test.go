package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/sandbox"
)

// --------------------------------------------------------------------------
// Shared fakes: cmd_sandbox_test.go reuses both of these.
// --------------------------------------------------------------------------

// fakeRuntime is a runtime.Runtime that records every call it receives and
// answers from injected hooks, so `init`, `sandbox status`, `sandbox
// destroy`, `sandbox rebuild`, and `sandbox shell` can all be tested with no
// Docker daemon and no wsl.exe.
//
// out, when set, gives each call a way to leave a marker in the same buffer
// the command under test writes to, which is what
// TestSandboxRebuildSaysWhatItWillDoThenDestroysThenProvisionsInThatOrder
// uses to prove the announcement was printed before either call ran.
type fakeRuntime struct {
	out io.Writer

	provisionCalls int
	destroyCalls   int
	callOrder      []string

	provisionFunc func(ctx context.Context) error
	provisionErr  error

	destroyFunc func(ctx context.Context) error
	destroyErr  error

	status    runtime.Status
	statusErr error
}

var _ runtime.Runtime = (*fakeRuntime)(nil)

func (f *fakeRuntime) Provision(ctx context.Context, _ runtime.ImageSpec) error {
	f.provisionCalls++
	f.callOrder = append(f.callOrder, "Provision")
	if f.out != nil {
		io.WriteString(f.out, "fakeRuntime: Provision called\n")
	}
	if f.provisionFunc != nil {
		return f.provisionFunc(ctx)
	}
	return f.provisionErr
}

func (f *fakeRuntime) Destroy(ctx context.Context) error {
	f.destroyCalls++
	f.callOrder = append(f.callOrder, "Destroy")
	if f.out != nil {
		io.WriteString(f.out, "fakeRuntime: Destroy called\n")
	}
	if f.destroyFunc != nil {
		return f.destroyFunc(ctx)
	}
	return f.destroyErr
}

func (f *fakeRuntime) Status(context.Context) (runtime.Status, error) {
	return f.status, f.statusErr
}

func (f *fakeRuntime) StartSession(context.Context, runtime.SessionSpec) (runtime.Session, error) {
	return nil, errors.New("fakeRuntime: StartSession is not implemented")
}

func (f *fakeRuntime) Capabilities() runtime.Caps { return runtime.Caps{} }

// fakeResolver is the resolveFunc seam's recording double: every call is
// counted, so a refusal test can assert nothing was ever constructed.
type fakeResolver struct {
	calls int

	// lastWant records the backend every caller of resolve most recently
	// asked for, so a test can assert that a verb actually plumbed its own
	// --runtime flag through to resolve rather than hardcoding
	// sandbox.Auto regardless of what the caller passed.
	lastWant sandbox.Backend

	// resolveFunc, if set, overrides rt/choice/err entirely: tests that need
	// the real Decide logic (with a fake GOOS) plug it in here.
	resolveFunc func(ctx context.Context, want sandbox.Backend) (runtime.Runtime, sandbox.Choice, error)

	rt     runtime.Runtime
	choice sandbox.Choice
	err    error
}

func (f *fakeResolver) resolve(ctx context.Context, want sandbox.Backend) (runtime.Runtime, sandbox.Choice, error) {
	f.calls++
	f.lastWant = want
	if f.resolveFunc != nil {
		return f.resolveFunc(ctx, want)
	}
	return f.rt, f.choice, f.err
}

// --------------------------------------------------------------------------
// init
// --------------------------------------------------------------------------

func TestInitProvisionsOnceAndNamesTheBackendAndWhatToRunNext(t *testing.T) {
	rt := &fakeRuntime{status: runtime.Status{Provisioned: true, Running: true}}
	choice := sandbox.Choice{Chosen: sandbox.Docker, Reason: "chose the docker backend because this platform has no WSL backend"}
	fake := &fakeResolver{rt: rt, choice: choice}

	var out bytes.Buffer
	if err := runInit(context.Background(), &out, fake.resolve, "auto"); err != nil {
		t.Fatalf("runInit() error = %v, want nil", err)
	}

	if fake.calls != 1 {
		t.Errorf("resolve was called %d times, want 1", fake.calls)
	}
	if rt.provisionCalls != 1 {
		t.Errorf("Provision was called %d times, want 1", rt.provisionCalls)
	}
	if rt.destroyCalls != 0 {
		t.Errorf("Destroy was called %d times, want 0", rt.destroyCalls)
	}

	got := out.String()
	if !strings.Contains(got, choice.Reason) {
		t.Errorf("output %q does not contain the Reason %q", got, choice.Reason)
	}
	if !strings.Contains(got, "shellforge run ") {
		t.Errorf("output %q does not suggest a `shellforge run` next step", got)
	}
}

// TestInitIsIdempotent asserts a second `init` provisions again rather than
// refusing, and never reaches for Destroy to get a clean slate: Provision's
// own idempotency is the only recovery mechanism this command uses.
func TestInitIsIdempotent(t *testing.T) {
	rt := &fakeRuntime{status: runtime.Status{Provisioned: true, Running: true}}
	choice := sandbox.Choice{Chosen: sandbox.Docker, Reason: "chose the docker backend"}
	fake := &fakeResolver{rt: rt, choice: choice}

	for i := 0; i < 2; i++ {
		var out bytes.Buffer
		if err := runInit(context.Background(), &out, fake.resolve, "auto"); err != nil {
			t.Fatalf("runInit() call %d: error = %v, want nil", i+1, err)
		}
	}

	if rt.provisionCalls != 2 {
		t.Errorf("Provision was called %d times, want 2", rt.provisionCalls)
	}
	if rt.destroyCalls != 0 {
		t.Errorf("Destroy was called %d times, want 0", rt.destroyCalls)
	}

	st, err := rt.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v, want nil", err)
	}
	if !st.Provisioned || !st.Running {
		t.Errorf("Status() = %+v, want Provisioned and Running true", st)
	}
}

// TestInitWithAnUnknownRuntimeFlagFailsBeforeConstructingARuntime is the
// refusal one layer up from TestParseBackendRefusesAnUnknownValue: runInit
// must validate --runtime itself before ever calling resolve.
func TestInitWithAnUnknownRuntimeFlagFailsBeforeConstructingARuntime(t *testing.T) {
	fake := &fakeResolver{}
	var out bytes.Buffer

	err := runInit(context.Background(), &out, fake.resolve, "podman")
	if err == nil {
		t.Fatal("runInit() error = nil, want a refusal")
	}
	var uxErr *ux.Error
	if !errors.As(err, &uxErr) {
		t.Fatalf("runInit() error = %v (%T), want *ux.Error", err, err)
	}
	for _, name := range []string{"auto", "wsl", "docker"} {
		if !strings.Contains(uxErr.Remediation, name) {
			t.Errorf("remediation = %q, want it to name %q", uxErr.Remediation, name)
		}
	}
	if fake.calls != 0 {
		t.Errorf("resolve was called %d times, want 0: nothing should be constructed for an unknown flag", fake.calls)
	}
}

// TestInitWithRuntimeWSLOnLinuxFailsThroughUxFail drives the real Decide
// logic through the resolver seam, with GOOS forced to linux, and asserts
// runInit hands the resulting *ux.Error straight through rather than
// stripping or rewrapping it.
func TestInitWithRuntimeWSLOnLinuxFailsThroughUxFail(t *testing.T) {
	fake := &fakeResolver{
		resolveFunc: func(ctx context.Context, want sandbox.Backend) (runtime.Runtime, sandbox.Choice, error) {
			return sandbox.Resolve(ctx, sandbox.Options{Want: want, GOOS: "linux"})
		},
	}
	var out bytes.Buffer

	err := runInit(context.Background(), &out, fake.resolve, "wsl")
	if err == nil {
		t.Fatal("runInit() error = nil, want a refusal")
	}
	var uxErr *ux.Error
	if !errors.As(err, &uxErr) {
		t.Fatalf("runInit() error = %v (%T), want *ux.Error", err, err)
	}
	if strings.TrimSpace(uxErr.Remediation) == "" {
		t.Error("remediation is empty")
	}
	if uxErr.DocAnchor != "wsl-not-installed" {
		t.Errorf("DocAnchor = %q, want %q", uxErr.DocAnchor, "wsl-not-installed")
	}
}

// TestInitCancelledMidProvisionReportsThroughUxAndLeavesNothingRunning
// asserts Ctrl-C during a re-provision of an existing sandbox is reported
// cleanly and never triggers a rollback that would destroy a working
// sandbox.
func TestInitCancelledMidProvisionReportsThroughUxAndLeavesNothingRunning(t *testing.T) {
	started := make(chan struct{})
	rt := &fakeRuntime{
		provisionFunc: func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		},
	}
	choice := sandbox.Choice{Chosen: sandbox.Docker, Reason: "chose the docker backend"}
	fake := &fakeResolver{rt: rt, choice: choice}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- runInit(ctx, io.Discard, fake.resolve, "auto")
	}()

	<-started
	cancel()
	err := <-errCh

	if err == nil {
		t.Fatal("runInit() error = nil, want a cancellation reported through ux")
	}
	var uxErr *ux.Error
	if !errors.As(err, &uxErr) {
		t.Fatalf("runInit() error = %v (%T), want *ux.Error", err, err)
	}
	if strings.TrimSpace(uxErr.Remediation) == "" {
		t.Error("remediation is empty")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("errors.Is(err, context.Canceled) = false, want true: %v", err)
	}
	if rt.destroyCalls != 0 {
		t.Errorf("Destroy was called %d times, want 0: a cancelled init must never try to clean up by destroying", rt.destroyCalls)
	}

	st, statusErr := rt.Status(context.Background())
	if statusErr != nil {
		t.Fatalf("Status() error = %v, want nil", statusErr)
	}
	if st.Provisioned || st.Running {
		t.Errorf("Status() = %+v, want neither Provisioned nor Running after a cancelled provision", st)
	}
}

// TestInitReportsAFallbackChoiceToTheUser pins the "it never picks
// silently" contract at the CLI boundary: the resolver's Reason must reach
// the learner's terminal verbatim.
func TestInitReportsAFallbackChoiceToTheUser(t *testing.T) {
	rt := &fakeRuntime{status: runtime.Status{Provisioned: true, Running: true}}
	choice := sandbox.Choice{
		Chosen:   sandbox.Docker,
		Fallback: true,
		Reason:   "chose the docker backend because WSL2 is not available (no WSL2 distribution)",
	}
	fake := &fakeResolver{rt: rt, choice: choice}

	var out bytes.Buffer
	if err := runInit(context.Background(), &out, fake.resolve, "auto"); err != nil {
		t.Fatalf("runInit() error = %v, want nil", err)
	}
	if !strings.Contains(out.String(), choice.Reason) {
		t.Errorf("output %q does not contain the fallback Reason %q", out.String(), choice.Reason)
	}
}
