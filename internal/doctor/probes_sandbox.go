package doctor

import "context"

// sandboxHealthProbe checks whether the sandbox is provisioned and
// running, through the caller-supplied SandboxProber. A nil prober is
// legal: it means no runtime was supplied, and this reports Warn rather
// than Fail, since a machine before `shellforge init` is not broken.
type sandboxHealthProbe struct {
	baseProbe
	prober SandboxProber
}

func (p sandboxHealthProbe) Run(ctx context.Context) Result {
	if err := ctx.Err(); err != nil {
		return p.interrupted(err)
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
