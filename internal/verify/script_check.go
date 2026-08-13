package verify

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/platform"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

func init() {
	Register("script", newScriptCheck)
}

// defaultScriptUser is who a script check runs as when as_user is absent
// from Params entirely.
const defaultScriptUser = "learner"

type scriptCheck struct {
	id, body, onFail, asUser string
}

func newScriptCheck(s Spec) (Check, error) {
	body, err := paramString(s.Params, "run", true)
	if err != nil {
		return nil, factoryError("script", s.ID, err)
	}

	asUser := defaultScriptUser
	if raw, present, err := paramStringPresence(s.Params, "as_user"); err != nil {
		return nil, factoryError("script", s.ID, err)
	} else if present {
		// as_user is untrusted pack input reaching ExecOpts.User, which
		// reaches an argv element on a real sandbox. Reject, never
		// sanitize: an explicitly-provided value, including an explicitly
		// empty string, must satisfy the same allowlist every other
		// sandbox-bound identifier does. platform.ValidIdentifier already
		// rejects the empty string, so no separate empty-string branch is
		// needed here; "as_user key entirely absent" is the only path that
		// reaches the learner default without validation.
		if !platform.ValidIdentifier(raw) {
			return nil, factoryError("script", s.ID, fmt.Errorf("param %q value %q is not a valid sandbox user: must match %s", "as_user", raw, platform.IdentifierPattern))
		}
		asUser = raw
	}

	return &scriptCheck{id: s.ID, body: body, onFail: s.OnFail, asUser: asUser}, nil
}

func (c *scriptCheck) ID() string { return c.id }

func (c *scriptCheck) Run(ctx context.Context, env Env) Result {
	start := time.Now()

	// c.body is one argv element to bash -c, never string-concatenated
	// with anything else. This is the one place in this package that runs
	// arbitrary shell syntax, and it is confined to exactly this element.
	res, err := env.Session.Exec(ctx, []string{"bash", "-c", c.body}, runtime.ExecOpts{User: c.asUser})
	if err != nil {
		return Result{ID: c.id, Status: StatusError, Message: fmt.Sprintf("could not run the script: %s", err), Duration: time.Since(start)}
	}
	if res.ExitCode == 0 {
		return Result{ID: c.id, Status: StatusPass, Duration: time.Since(start)}
	}

	message := c.onFail
	if stdout := strings.TrimRight(string(res.Stdout), "\n"); stdout != "" {
		message = message + "\n\n" + stdout
	}
	return Result{ID: c.id, Status: StatusFail, Message: message, Duration: time.Since(start)}
}
