package verify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

// ErrSnapshotMissing reports that instrument.bash has not written the shell
// state snapshot files yet, or that reading them failed for any reason: the
// file is genuinely absent, Session.Exec itself errored, or the snapshot
// content could not be parsed. All three collapse to this one sentinel
// because a check that depends on Snapshots can only tell a learner one
// thing regardless of which happened: press Enter at the sandbox prompt and
// try again. This is never the learner's fault at this point; they have not
// run a command yet, so there is nothing to check.
//
// DocAnchor: "missing-snapshot"
var ErrSnapshotMissing = errors.New("shell instrumentation has not produced state yet")

// Snapshots reads the files instrument.bash writes on every prompt, under
// $SF_STATE. Dir is that directory, threaded in from the level's runtime
// configuration rather than hardcoded, because the sandbox-side default
// belongs to whatever provisions SF_STATE, not to this package.
//
// Both Env and Cwd read through sess.Exec, never a local filesystem call:
// the sandbox is remote, and there is no path shared with this process.
type Snapshots struct {
	Dir string
}

// envSnapshotPath is the file instrument.bash writes with `env -0 >
// env.snapshot`.
func (s Snapshots) envSnapshotPath() string {
	return filepath.Join(s.Dir, "env.snapshot")
}

// Env reads and parses env.snapshot, which instrument.bash writes with
// `env -0`: NUL-separated KEY=VALUE records. Splitting only on NUL, never on
// newline, is required because a value can legitimately contain a newline.
// Each record is split on the first '=' only, because a value can also
// contain '='.
//
// A missing or unreadable snapshot returns ErrSnapshotMissing, wrapped with
// the underlying detail.
func (s Snapshots) Env(ctx context.Context, sess runtime.Session) (map[string]string, error) {
	path := s.envSnapshotPath()

	res, err := sess.Exec(ctx, []string{"cat", path}, runtime.ExecOpts{})
	if err != nil {
		return nil, fmt.Errorf("%w: reading %s: %s", ErrSnapshotMissing, path, err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("%w: reading %s: %s", ErrSnapshotMissing, path, strings.TrimSpace(string(res.Stderr)))
	}

	return parseEnvSnapshot(res.Stdout), nil
}

// Cwd reports the learner's current working directory.
//
// There is no dedicated cwd snapshot file: instrument.bash writes exactly
// four files (env.snapshot, alias.snapshot, func.snapshot, shopt.snapshot),
// and none of them is a cwd file. bash auto-exports PWD, so it is already
// present in env.snapshot; Cwd calls Env internally and reads PWD out of the
// result rather than reading a file that does not exist.
func (s Snapshots) Cwd(ctx context.Context, sess runtime.Session) (string, error) {
	env, err := s.Env(ctx, sess)
	if err != nil {
		return "", err
	}
	cwd, ok := env["PWD"]
	if !ok {
		return "", fmt.Errorf("%w: PWD is not present in %s", ErrSnapshotMissing, s.envSnapshotPath())
	}
	return cwd, nil
}

// parseEnvSnapshot parses the NUL-separated KEY=VALUE records `env -0`
// writes. A record with no '=' is skipped rather than treated as an error:
// well-formed env -0 output never produces one, so tolerating it costs
// nothing and a hard failure here would turn one stray byte into an outage
// for every check that depends on Snapshots.
func parseEnvSnapshot(raw []byte) map[string]string {
	out := make(map[string]string)
	for _, rec := range bytes.Split(raw, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		s := string(rec)
		idx := strings.IndexByte(s, '=')
		if idx < 0 {
			continue
		}
		out[s[:idx]] = s[idx+1:]
	}
	return out
}
