package setup

import (
	"context"
	"fmt"
	"path"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/platform"
	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

// userLearner is the sandbox user every non-root call in this package runs
// as: the sentinel read and write, the resolve-root readlink, and the final
// rm -rf. See runner.go's Teardown doc comment for why root cannot do the
// deletion itself.
const userLearner = "learner"

// userRoot is the sandbox user the chown steps and the author's own
// setup.script and teardown.script run as.
const userRoot = "root"

// sentinelPath returns the path of the SETUP_OK marker for a level, under
// stateDir. It never lives under the level's own root: a sentinel inside the
// level root would be visible to `ls -a`, deletable by the learner, and
// counted by any level that asks how many files are under the root, which
// would make the runner a source of content bugs.
//
// stateDir is caller-supplied (WithStateDir), not pack-supplied, but it
// still becomes an argv element below, so it is validated here, every time
// it is about to matter. This is the one place all three public entry
// points (Setup and Teardown through writeSentinel and removeSentinel,
// IsSetUp directly) go through, so validating it here, rather than
// separately in resolveLevelRoot, means a broken WithStateDir value fails
// closed on every path instead of on only two of the three.
//
// levelID becomes a path element too, so it is validated against
// platform.ValidIdentifier before that happens: a level id is pack-supplied
// data, and this is the same allowlist every other sandbox-bound identifier
// in this codebase goes through.
func sentinelPath(stateDir, levelID string) (string, error) {
	if err := validateLevelRoot(stateDir); err != nil {
		return "", ux.Fail("read the level definition",
			fmt.Errorf("the configured state directory %q is not valid: %w", stateDir, err),
			remediationUnsafeLevelRoot, "unsafe-level-root")
	}
	if !platform.ValidIdentifier(levelID) {
		return "", ux.Fail("read the level definition",
			fmt.Errorf("level id %q does not match %s", levelID, platform.IdentifierPattern),
			remediationLevelPackInvalid, "level-pack-invalid")
	}
	return path.Join(stateDir, "levels", levelID, SentinelName), nil
}

// writeSentinel marks lvl as set up. It writes through two shell-free Exec
// calls as the learner, mkdir -p then touch, and never through PushFiles:
// PushFiles's tar creates every ancestor directory as uid 0, so pushing the
// sentinel would silently reassign the state directory to root and every
// later journal write would then fail. Writing it as the learner sidesteps
// that entirely.
func (r *Runner) writeSentinel(ctx context.Context, levelID string) error {
	p, err := sentinelPath(r.stateDir, levelID)
	if err != nil {
		return err
	}
	dir := path.Dir(p)

	if res, err := r.sess.Exec(ctx, []string{"mkdir", "-p", "--", dir}, runtime.ExecOpts{User: userLearner}); err != nil {
		return ux.Fail("record that the level is set up", err, remediationSandboxUnhealthy, "sandbox-unhealthy")
	} else if res.ExitCode != 0 {
		return ux.Fail("record that the level is set up",
			fmt.Errorf("mkdir -p %q exited %d: %s", dir, res.ExitCode, res.Stderr),
			remediationSandboxUnhealthy, "sandbox-unhealthy")
	}

	if res, err := r.sess.Exec(ctx, []string{"touch", "--", p}, runtime.ExecOpts{User: userLearner}); err != nil {
		return ux.Fail("record that the level is set up", err, remediationSandboxUnhealthy, "sandbox-unhealthy")
	} else if res.ExitCode != 0 {
		return ux.Fail("record that the level is set up",
			fmt.Errorf("touch %q exited %d: %s", p, res.ExitCode, res.Stderr),
			remediationSandboxUnhealthy, "sandbox-unhealthy")
	}
	return nil
}

// removeSentinel clears the marker. rm -f on a missing path exits zero, so
// this is safe to call whether or not setup ever ran.
func (r *Runner) removeSentinel(ctx context.Context, levelID string) error {
	p, err := sentinelPath(r.stateDir, levelID)
	if err != nil {
		return err
	}
	res, err := r.sess.Exec(ctx, []string{"rm", "-f", "--", p}, runtime.ExecOpts{User: userLearner})
	if err != nil {
		return ux.Fail("remove the level world", err, remediationLevelStateCorrupted, "level-state-corrupted")
	}
	if res.ExitCode != 0 {
		return ux.Fail("remove the level world",
			fmt.Errorf("rm -f %q exited %d: %s", p, res.ExitCode, res.Stderr),
			remediationLevelStateCorrupted, "level-state-corrupted")
	}
	return nil
}

// IsSetUp reports whether lvl's sentinel exists. It never mutates: the only
// call it makes is test -f.
func (r *Runner) IsSetUp(ctx context.Context, lvl *content.Level) (bool, error) {
	p, err := sentinelPath(r.stateDir, lvl.ID)
	if err != nil {
		return false, err
	}
	// No "--" terminator here: unlike mkdir, touch, rm, and chown, the
	// POSIX test utility does not recognize "--" as an end-of-options
	// marker and reports a syntax error if given one. This is safe without
	// it regardless, because p is always absolute (it is built from
	// stateDir, a compile-time-rooted constant, plus a validated level id),
	// so it can never be mistaken for a flag.
	res, err := r.sess.Exec(ctx, []string{"test", "-f", p}, runtime.ExecOpts{User: userLearner})
	if err != nil {
		return false, ux.Fail("check whether the level is already set up", err, remediationSandboxUnhealthy, "sandbox-unhealthy")
	}
	return res.ExitCode == 0, nil
}
