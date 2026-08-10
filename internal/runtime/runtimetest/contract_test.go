package runtimetest

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

// stubRuntime and stubSession are a deliberately minimal in-memory fake used
// only to exercise this package's own plumbing: RunContract's dispatch,
// newRuntime's cleanup registration, and the skip path. They are NOT a
// reference implementation. Passing the contract against a fake would prove
// nothing about a real backend, which is why every contract assertion below
// is only ever run against a Factory that skips before touching either type.
type stubRuntime struct{}

func (s *stubRuntime) Provision(ctx context.Context, spec runtime.ImageSpec) error {
	return runtime.ErrNotSupported
}

func (s *stubRuntime) Destroy(ctx context.Context) error {
	return runtime.ErrNotSupported
}

func (s *stubRuntime) Status(ctx context.Context) (runtime.Status, error) {
	return runtime.Status{}, runtime.ErrNotSupported
}

func (s *stubRuntime) StartSession(ctx context.Context, spec runtime.SessionSpec) (runtime.Session, error) {
	return nil, runtime.ErrNotSupported
}

func (s *stubRuntime) Capabilities() runtime.Caps {
	return runtime.Caps{}
}

type stubSession struct{}

func (s *stubSession) Exec(ctx context.Context, argv []string, opts runtime.ExecOpts) (runtime.ExecResult, error) {
	return runtime.ExecResult{}, runtime.ErrNotSupported
}

func (s *stubSession) Attach(ctx context.Context, opts runtime.AttachOpts) (runtime.PTY, error) {
	return nil, runtime.ErrNotSupported
}

func (s *stubSession) PushFiles(ctx context.Context, m runtime.FileManifest) error {
	return runtime.ErrNotSupported
}

func (s *stubSession) PullFile(ctx context.Context, path string) ([]byte, error) {
	return nil, runtime.ErrNotSupported
}

func (s *stubSession) Close() error {
	return runtime.ErrNotSupported
}

var _ runtime.Runtime = (*stubRuntime)(nil)
var _ runtime.Session = (*stubSession)(nil)

// skippingFactory returns a Factory that calls t.Skip before building
// anything, and counts how many times it was invoked.
func skippingFactory(count *int32, reason string) Factory {
	return func(t *testing.T) (runtime.Runtime, func()) {
		if count != nil {
			atomic.AddInt32(count, 1)
		}
		t.Skip(reason)
		return nil, nil
	}
}

func TestRunContractDispatchesEveryAssertion(t *testing.T) {
	var calls int32
	factory := skippingFactory(&calls, "runtimetest: no backend in this test")

	ok := t.Run("dispatch", func(t *testing.T) {
		RunContract(t, factory)
	})
	if !ok {
		t.Fatal("RunContract reported failure with an all-skip Factory")
	}
	if got := atomic.LoadInt32(&calls); got != 12 {
		t.Fatalf("Factory calls: want 12, got %d", got)
	}
}

func TestSkipFactoryDoesNotFail(t *testing.T) {
	factory := skippingFactory(nil, "runtimetest: no backend available")

	ok := t.Run("skip", func(t *testing.T) {
		RunContract(t, factory)
	})
	if !ok {
		t.Fatal("RunContract reported failure for an all-skip Factory")
	}
}

func TestNewRuntimeRegistersCleanup(t *testing.T) {
	var calls int32
	t.Run("cleanup", func(t *testing.T) {
		factory := func(t *testing.T) (runtime.Runtime, func()) {
			return &stubRuntime{}, func() { atomic.AddInt32(&calls, 1) }
		}
		_ = newRuntime(t, factory)
	})
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("cleanup calls after the subtest ended: want 1, got %d", got)
	}
}

func TestNewRuntimeToleratesNilCleanup(t *testing.T) {
	factory := func(t *testing.T) (runtime.Runtime, func()) {
		return &stubRuntime{}, nil
	}
	rt := newRuntime(t, factory)
	if rt == nil {
		t.Fatal("newRuntime returned nil for a Factory that returned a non-nil Runtime")
	}
}

func TestEachAssertionIsCallableAlone(t *testing.T) {
	skipFactory := skippingFactory(nil, "runtimetest: no backend in this test")

	for _, a := range contract {
		a := a
		t.Run(a.name, func(t *testing.T) {
			a.run(t, skipFactory)
		})
	}
}
