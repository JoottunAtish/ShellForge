package verify

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

func init() {
	Register("process_running", newProcessRunningCheck)
}

type processRunningCheck struct {
	id, onFail string
	pattern    *regexp.Regexp
	negate     bool
}

func newProcessRunningCheck(s Spec) (Check, error) {
	patternRaw, err := paramString(s.Params, "pattern", true)
	if err != nil {
		return nil, factoryError("process_running", s.ID, err)
	}
	negate, err := paramBool(s.Params, "negate")
	if err != nil {
		return nil, factoryError("process_running", s.ID, err)
	}
	re, err := regexp.Compile(patternRaw)
	if err != nil {
		return nil, factoryError("process_running", s.ID, fmt.Errorf("param %q is not a valid regex: %w", "pattern", err))
	}
	return &processRunningCheck{id: s.ID, onFail: s.OnFail, pattern: re, negate: negate}, nil
}

func (c *processRunningCheck) ID() string { return c.id }

func (c *processRunningCheck) Run(ctx context.Context, env Env) Result {
	start := time.Now()
	res, err := env.Session.Exec(ctx, []string{"ps", "-eo", "pid,args"}, runtime.ExecOpts{})
	if err != nil {
		return Result{ID: c.id, Status: StatusError, Message: fmt.Sprintf("could not list processes: %s", err), Duration: time.Since(start)}
	}
	if res.ExitCode != 0 {
		return Result{ID: c.id, Status: StatusError, Message: fmt.Sprintf("ps failed: %s", strings.TrimSpace(string(res.Stderr))), Duration: time.Since(start)}
	}

	matched := false
	lines := strings.Split(string(res.Stdout), "\n")
	for i, line := range lines {
		if i == 0 {
			// The header row: "  PID COMMAND". ps always writes it first.
			continue
		}
		if line == "" {
			continue
		}
		if c.pattern.MatchString(line) {
			matched = true
			break
		}
	}

	pass := matched
	if c.negate {
		pass = !matched
	}
	if pass {
		return Result{ID: c.id, Status: StatusPass, Duration: time.Since(start)}
	}
	return Result{ID: c.id, Status: StatusFail, Message: c.onFail, Duration: time.Since(start)}
}
