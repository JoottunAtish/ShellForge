// Command shellforge is a game that teaches the Linux command line by dropping
// the learner into a real, disposable Linux shell and verifying real system
// state.
//
// Layer L5. This is the only package allowed to wire a concrete runtime
// implementation to the rest of the program.
//
// The dispatcher here is deliberately small and dependency-free so that the
// repository builds and CI is green from the first commit. Day 1 Session A
// replaces it with spf13/cobra. See docs/design/SESSION-PROMPTS.md.
package main

import (
	"context"
	"errors"
	"fmt"
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

// errUsage signals a usage problem, which exits 2 rather than 1.
var errUsage = errors.New("usage")

func main() {
	// Ctrl-C must always exit cleanly and leave no orphan containers or
	// half-imported distros. Every operation that touches a runtime takes this
	// context and honours cancellation.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:]); err != nil {
		if errors.Is(err, errUsage) {
			os.Exit(2)
		}
		ux.Render(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		printUsage(os.Stdout)
		return nil
	}

	name := args[0]
	rest := args[1:]

	switch name {
	case "-h", "--help", "help":
		if len(rest) > 0 {
			return helpFor(os.Stdout, rest[0])
		}
		printUsage(os.Stdout)
		return nil
	case "-v", "--version":
		name = "version"
	}

	cmd, ok := lookup(name)
	if !ok {
		fmt.Fprintf(os.Stderr, "shellforge: unknown command %q\n\n", name)
		printUsage(os.Stderr)
		return errUsage
	}

	return cmd.run(ctx, rest)
}
