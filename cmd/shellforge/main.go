// Command shellforge is a game that teaches the Linux command line by dropping
// the learner into a real, disposable Linux shell and verifying real system
// state.
//
// Layer L5. This is the only package allowed to wire a concrete runtime
// implementation to the rest of the program.
package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
)

// Build information, injected by the linker. See the Makefile.
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	// Ctrl-C must always exit cleanly and leave no orphan containers or
	// half-imported distros. Every operation that touches a runtime takes this
	// context and honours cancellation.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := NewRootCommand(VersionInfo{Version: version, Commit: commit, BuildDate: buildDate})
	if err := root.ExecuteContext(ctx); err != nil {
		ux.Render(os.Stderr, renderableError(err))
		os.Exit(1)
	}
}

// renderableError ensures every error reaching ux.Render is a *ux.Error.
//
// root.go sets SilenceErrors, so a parse-time failure cobra generates
// itself, an unknown command or an unknown flag, reaches here as a plain
// error rather than one already wrapped by a verb's own RunE. Left
// unwrapped, ux.Render's own fallback treats it as "this is unexpected,
// which means it is our bug", which misdirects a learner into filing a bug
// report over their own typo instead of telling them what to try next.
func renderableError(err error) error {
	var uxErr *ux.Error
	if errors.As(err, &uxErr) {
		return err
	}
	return ux.Fail(err.Error(), nil, "Run `shellforge help` to see the available commands.", "")
}
