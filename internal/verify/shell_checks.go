package verify

import (
	"context"
	"errors"
	"time"
)

func init() {
	Register("env_var", newEnvVarCheck, "name", "value")
	Register("cwd_is", newCwdIsCheck, "path")
}

// snapshotMissingMessage is shown for any check whose Snapshots read failed.
// It must never read as "you got it wrong": at this point the learner has
// simply not pressed Enter at the sandbox prompt yet, which is when
// instrument.bash first writes its state files.
const snapshotMissingMessage = "the shell has not reported its state yet: run a command at the sandbox prompt, then run check again"

// --- env_var ---

type envVarCheck struct {
	id, name, onFail string
	value            string
	checkValue       bool
}

func newEnvVarCheck(s Spec) (Check, error) {
	name, err := paramString(s.Params, "name", true)
	if err != nil {
		return nil, factoryError("env_var", s.ID, err)
	}
	value, present, err := paramStringPresence(s.Params, "value")
	if err != nil {
		return nil, factoryError("env_var", s.ID, err)
	}
	return &envVarCheck{id: s.ID, name: name, onFail: s.OnFail, value: value, checkValue: present}, nil
}

func (c *envVarCheck) ID() string { return c.id }

func (c *envVarCheck) Run(ctx context.Context, env Env) Result {
	start := time.Now()
	snapshot, err := env.Snapshots.Env(ctx, env.Session)
	if err != nil {
		return snapshotErrorResult(c.id, err, start)
	}

	actual, ok := snapshot[c.name]
	if !ok {
		return Result{ID: c.id, Status: StatusFail, Message: c.onFail, Duration: time.Since(start)}
	}
	if c.checkValue && actual != c.value {
		return Result{ID: c.id, Status: StatusFail, Message: c.onFail, Duration: time.Since(start)}
	}
	return Result{ID: c.id, Status: StatusPass, Duration: time.Since(start)}
}

// --- cwd_is ---

type cwdIsCheck struct {
	id, path, onFail string
}

func newCwdIsCheck(s Spec) (Check, error) {
	path, err := paramString(s.Params, "path", true)
	if err != nil {
		return nil, factoryError("cwd_is", s.ID, err)
	}
	return &cwdIsCheck{id: s.ID, path: path, onFail: s.OnFail}, nil
}

func (c *cwdIsCheck) ID() string { return c.id }

func (c *cwdIsCheck) Run(ctx context.Context, env Env) Result {
	start := time.Now()
	cwd, err := env.Snapshots.Cwd(ctx, env.Session)
	if err != nil {
		return snapshotErrorResult(c.id, err, start)
	}
	if cwd != c.path {
		return Result{ID: c.id, Status: StatusFail, Message: c.onFail, Duration: time.Since(start)}
	}
	return Result{ID: c.id, Status: StatusPass, Duration: time.Since(start)}
}

// snapshotErrorResult builds the distinct, non-blaming Result for any
// Snapshots read failure. Every Snapshots error wraps ErrSnapshotMissing
// (snapshot.go), so this is the one place that translates it into a Result.
func snapshotErrorResult(id string, err error, start time.Time) Result {
	if errors.Is(err, ErrSnapshotMissing) {
		return Result{ID: id, Status: StatusError, Message: snapshotMissingMessage, Duration: time.Since(start)}
	}
	return Result{ID: id, Status: StatusError, Message: err.Error(), Duration: time.Since(start)}
}
