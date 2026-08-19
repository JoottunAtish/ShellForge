package sandbox

import (
	"context"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

// proberTimeout bounds one Ping. A wedged Docker daemon or a hung wsl.exe
// must not hang the one command a learner runs when their machine is
// broken, `shellforge doctor`.
const proberTimeout = 5 * time.Second

// Prober answers `shellforge doctor`'s one question about the sandbox:
// is it provisioned, and is it running. It structurally satisfies
// internal/doctor.SandboxProber without importing internal/doctor: the
// interface is a single two-line method set, so there is nothing to gain
// and a whole layer to violate by importing it just to spell the name.
type Prober struct {
	// resolve finds the backend to ask. Nil means the real Resolve,
	// through the real Options zero values; tests substitute a fake to
	// drive Ping without a Docker daemon or a wsl.exe.
	resolve func(ctx context.Context) (runtime.Runtime, Choice, error)
}

// NewProber returns a Prober wired to the real backend resolution: the
// same Resolve, with Auto, that init and the sandbox commands use.
func NewProber() *Prober {
	return &Prober{}
}

// sandboxProber is internal/doctor.SandboxProber's exact method set,
// copied here rather than imported, so this compile-time assertion proves
// *Prober satisfies it without this package ever importing internal/doctor.
type sandboxProber interface {
	Ping(ctx context.Context) (provisioned, running bool, detail string, err error)
}

var _ sandboxProber = (*Prober)(nil)

// Ping resolves a backend and asks it for Status, under proberTimeout.
//
// An unresolvable or unreachable backend is reported as not-provisioned,
// with a detail explaining why, and a nil error: a machine that has never
// run `shellforge init`, or one whose Docker daemon is not running, is a
// Warn for doctor to report, not a crash. An already-cancelled context
// returns immediately, before resolve or Status is ever called, so a
// wedged backend cannot hang the caller.
func (p *Prober) Ping(ctx context.Context) (provisioned, running bool, detail string, err error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return false, false, "", ctxErr
	}

	pctx, cancel := context.WithTimeout(ctx, proberTimeout)
	defer cancel()

	resolve := p.resolve
	if resolve == nil {
		resolve = func(ctx context.Context) (runtime.Runtime, Choice, error) {
			return Resolve(ctx, Options{Want: Auto})
		}
	}

	rt, _, resolveErr := resolve(pctx)
	if resolveErr != nil {
		return false, false, "the sandbox backend is not available: " + resolveErr.Error(), nil
	}

	st, statusErr := rt.Status(pctx)
	if statusErr != nil {
		return false, false, "the sandbox backend could not be reached: " + statusErr.Error(), nil
	}
	return st.Provisioned, st.Running, st.Detail, nil
}
