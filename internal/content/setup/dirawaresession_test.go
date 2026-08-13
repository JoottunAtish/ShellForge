package setup

import (
	"context"

	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

// dirAwareFakeSession wraps fakeSession with a minimal notion of which
// directories exist in the sandbox: a set populated by mkdir -p and
// answered against by chown -R and bash -c's WorkDir.
//
// A plain fakeSession answers every unscripted call with exit 0 no matter
// what came before it, which is exactly what let Setup ship without ever
// creating setup.root for a level whose setup.files is empty: the chown and
// the setup script both target a directory nothing created, and the fake
// said yes anyway. This type exists to say no instead, the way the real
// docker backend would, so TestSetupCreatesLevelRootWithNoFiles actually
// pins the mkdir step rather than passing regardless of whether it runs.
type dirAwareFakeSession struct {
	*fakeSession
	existing map[string]bool
}

// newDirAwareFakeSession returns a dirAwareFakeSession seeded with
// /home/learner, the sandbox user's home directory, which exists before any
// level runs.
func newDirAwareFakeSession() *dirAwareFakeSession {
	return &dirAwareFakeSession{
		fakeSession: &fakeSession{},
		existing:    map[string]bool{"/home/learner": true},
	}
}

// record appends argv and opts to the embedded fakeSession's log without
// consulting its scripted answers or its exit-0 default, for the two cases
// below that must answer non-zero instead.
func (d *dirAwareFakeSession) record(argv []string, opts runtime.ExecOpts) {
	d.execCalls = append(d.execCalls, execCall{argv: append([]string(nil), argv...), opts: opts})
	d.logEvent(argv)
}

// Exec intercepts mkdir -p (to learn a directory now exists), chown -R (to
// refuse against a directory that does not), and bash -c (to refuse a
// WorkDir that does not), then falls back to the embedded fakeSession for
// everything else.
func (d *dirAwareFakeSession) Exec(ctx context.Context, argv []string, opts runtime.ExecOpts) (runtime.ExecResult, error) {
	switch {
	case len(argv) >= 2 && argv[0] == "mkdir" && argv[1] == "-p":
		res, err := d.fakeSession.Exec(ctx, argv, opts)
		if err == nil && res.ExitCode == 0 {
			d.existing[argv[len(argv)-1]] = true
		}
		return res, err

	case len(argv) >= 2 && argv[0] == "chown" && argv[1] == "-R":
		target := argv[len(argv)-1]
		if !d.existing[target] {
			d.record(argv, opts)
			return runtime.ExecResult{ExitCode: 1, Stderr: []byte("chown: cannot access '" + target + "': No such file or directory")}, nil
		}

	case len(argv) == 3 && argv[0] == "bash" && argv[1] == "-c":
		if opts.WorkDir != "" && !d.existing[opts.WorkDir] {
			d.record(argv, opts)
			return runtime.ExecResult{ExitCode: 1, Stderr: []byte("bash: line 0: cd: " + opts.WorkDir + ": No such file or directory")}, nil
		}
	}
	return d.fakeSession.Exec(ctx, argv, opts)
}

var _ runtime.Session = (*dirAwareFakeSession)(nil)
