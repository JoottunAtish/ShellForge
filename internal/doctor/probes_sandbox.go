package doctor

import (
	"context"
	"os"

	"github.com/JoottunAtish/ShellForge/internal/platform"
)

// sandboxHealthProbe checks whether the sandbox is provisioned and
// running, through the caller-supplied SandboxProber. A nil prober is
// legal: it means no runtime was supplied, and this reports Warn rather
// than Fail, since a machine before `shellforge init` is not broken.
//
// It also owns the one observation that used to live on disk_free: whether
// the Shellforge data directory exists yet. That is a provisioning fact,
// not a disk-space fact, and this probe's own sandbox-unhealthy anchor
// already covers a not-yet-provisioned install, so this is also the one
// probe --fix is ever allowed to act on: see fixActions in doctor.go.
type sandboxHealthProbe struct {
	baseProbe
	prober SandboxProber
}

func (p sandboxHealthProbe) Run(ctx context.Context) Result {
	if err := ctx.Err(); err != nil {
		return p.interrupted(err)
	}

	dataDir, err := platform.DataDir()
	if err != nil {
		return p.result(Warn, "could not resolve the data directory: "+err.Error(), "Run `shellforge doctor` again.")
	}
	if _, statErr := os.Stat(dataDir); statErr != nil {
		if !os.IsNotExist(statErr) {
			return p.result(Warn, "could not check the data directory: "+statErr.Error(), "Run `shellforge doctor` again.")
		}
		return p.fixableResult(Warn, "the Shellforge data directory does not exist yet",
			"Run `shellforge doctor --fix` to create it, or run `shellforge init`.")
	}

	if p.prober == nil {
		return p.result(Warn, "no runtime was supplied to check the sandbox", "Run `shellforge init` to provision the sandbox.")
	}

	provisioned, running, detail, err := p.prober.Ping(ctx)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return p.interrupted(ctxErr)
	}
	if err != nil {
		return p.result(Fail, nonEmpty(detail, "the sandbox could not be reached: "+err.Error()), "Run `shellforge sandbox rebuild`.")
	}

	switch {
	case !provisioned:
		return p.result(Warn, nonEmpty(detail, "the sandbox is not provisioned"), "Run `shellforge init` to provision the sandbox.")
	case !running:
		return p.result(Warn, nonEmpty(detail, "the sandbox is provisioned but not running"), "Run `shellforge sandbox rebuild`.")
	default:
		return p.result(OK, nonEmpty(detail, "the sandbox is provisioned and running"), "No action needed.")
	}
}
