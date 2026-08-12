// Command shellforge is a game that teaches the Linux command line by dropping
// the learner into a real, disposable Linux shell and verifying real system
// state.
//
// Layer L5. This is the only package allowed to wire a concrete runtime
// implementation to the rest of the program.
package main

import (
	"context"
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
		ux.Render(os.Stderr, err)
		os.Exit(1)
	}
}
