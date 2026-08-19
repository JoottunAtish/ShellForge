package sandbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

// fakeStatusRuntime is a minimal runtime.Runtime whose Status is the only
// method these tests exercise, and which records how many times it was
// called.
type fakeStatusRuntime struct {
	runtime.Runtime
	status     runtime.Status
	statusErr  error
	statusCall int
}

func (f *fakeStatusRuntime) Status(ctx context.Context) (runtime.Status, error) {
	f.statusCall++
	return f.status, f.statusErr
}

// TestProberReportsNotProvisionedRatherThanAnErrorWhenNoBackendIsAvailable
// pins the doctor contract: a machine that has simply never run
// `shellforge init` is a Warn, not a Fail, so Ping must never propagate the
// resolve error itself.
func TestProberReportsNotProvisionedRatherThanAnErrorWhenNoBackendIsAvailable(t *testing.T) {
	fake := &fakeAvailable{wslOK: false, dockerOK: false}
	p := &Prober{
		resolve: func(ctx context.Context) (runtime.Runtime, Choice, error) {
			return Resolve(ctx, Options{
				Want:      Auto,
				GOOS:      "windows",
				Available: fake.probe,
			})
		},
	}

	provisioned, running, detail, err := p.Ping(context.Background())
	if err != nil {
		t.Fatalf("Ping() err = %v, want nil", err)
	}
	if provisioned {
		t.Error("provisioned = true, want false")
	}
	if running {
		t.Error("running = true, want false")
	}
	if detail == "" {
		t.Error("detail is empty, want a reason")
	}
}

// TestProberReportsNotProvisionedWhenTheBackendCannotBeReached covers the
// other half of the same contract: a backend that resolved fine but whose
// Status call itself failed is still a Warn-shaped answer, not an error.
func TestProberReportsNotProvisionedWhenTheBackendCannotBeReached(t *testing.T) {
	fakeRT := &fakeStatusRuntime{statusErr: errors.New("boom")}
	p := &Prober{
		resolve: func(ctx context.Context) (runtime.Runtime, Choice, error) {
			return fakeRT, Choice{Chosen: Docker}, nil
		},
	}

	provisioned, running, detail, err := p.Ping(context.Background())
	if err != nil {
		t.Fatalf("Ping() err = %v, want nil", err)
	}
	if provisioned || running {
		t.Errorf("provisioned=%v running=%v, want both false", provisioned, running)
	}
	if detail == "" {
		t.Error("detail is empty, want a reason")
	}
	if fakeRT.statusCall != 1 {
		t.Errorf("Status was called %d times, want 1", fakeRT.statusCall)
	}
}

// TestProberReportsAHealthySandbox is Ping's positive control: a resolved,
// reachable backend reports exactly what Status said.
func TestProberReportsAHealthySandbox(t *testing.T) {
	fakeRT := &fakeStatusRuntime{status: runtime.Status{
		Provisioned: true,
		Running:     true,
		Backend:     "docker",
		Detail:      "shellforge-sandbox",
	}}
	p := &Prober{
		resolve: func(ctx context.Context) (runtime.Runtime, Choice, error) {
			return fakeRT, Choice{Chosen: Docker}, nil
		},
	}

	provisioned, running, detail, err := p.Ping(context.Background())
	if err != nil {
		t.Fatalf("Ping() err = %v, want nil", err)
	}
	if !provisioned || !running {
		t.Errorf("provisioned=%v running=%v, want both true", provisioned, running)
	}
	if detail != "shellforge-sandbox" {
		t.Errorf("detail = %q, want %q", detail, "shellforge-sandbox")
	}
	if fakeRT.statusCall != 1 {
		t.Errorf("Status was called %d times, want 1", fakeRT.statusCall)
	}
}

// TestProberHonoursContextCancellation asserts that a wedged daemon cannot
// hang `shellforge doctor`: with an already-cancelled context, Ping returns
// promptly and never even calls Status.
func TestProberHonoursContextCancellation(t *testing.T) {
	fakeRT := &fakeStatusRuntime{status: runtime.Status{Provisioned: true, Running: true}}
	p := &Prober{
		resolve: func(ctx context.Context) (runtime.Runtime, Choice, error) {
			return fakeRT, Choice{Chosen: Docker}, nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		p.Ping(ctx) //nolint:errcheck // only the promptness and call count matter here
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Ping did not return promptly on an already-cancelled context")
	}

	if fakeRT.statusCall != 0 {
		t.Errorf("Status was called %d times, want 0: a cancelled context must never reach it", fakeRT.statusCall)
	}
}

// TestNewProberResolvesForReal is a thin smoke test that NewProber wires up
// the real Resolve path rather than leaving resolve nil, which would panic
// on the first Ping.
func TestNewProberResolvesForReal(t *testing.T) {
	p := NewProber()
	if p == nil {
		t.Fatal("NewProber() = nil")
	}
	// Calling Ping must not panic, regardless of what backend (if any) this
	// host actually has: a nil resolve func would panic on the call, and
	// that is the only thing this test needs to catch.
	_, _, _, _ = p.Ping(context.Background())
}
