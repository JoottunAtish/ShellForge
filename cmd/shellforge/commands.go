package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"text/tabwriter"

	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
)

// command is one CLI verb.
//
// Every verb in the product brief is registered here from day zero, even the
// ones that are not implemented, so that `shellforge help` is an honest map of
// the product and an unimplemented verb produces a useful message instead of
// "unknown command".
type command struct {
	name    string
	group   string
	summary string
	usage   string
	run     func(ctx context.Context, args []string) error
}

// group names, ordered as they print.
const (
	groupSetup   = "Setting up"
	groupPlaying = "Playing"
	groupProgres = "Progress"
	groupManage  = "Managing the sandbox"
	groupAuthor  = "Authoring content"
	groupMeta    = "About"
)

var groupOrder = []string{groupSetup, groupPlaying, groupProgres, groupManage, groupAuthor, groupMeta}

var commands = []command{
	{
		name:    "doctor",
		group:   groupSetup,
		summary: "Check this machine and explain how to fix anything missing",
		usage:   "shellforge doctor [--fix] [--json]",
		run:     notImplemented("doctor", "Day 3"),
	},
	{
		name:    "init",
		group:   groupSetup,
		summary: "Provision the sandbox. Run once, takes a few minutes",
		usage:   "shellforge init [--runtime=auto|wsl|docker]",
		run:     notImplemented("init", "Day 1"),
	},
	{
		name:    "play",
		group:   groupPlaying,
		summary: "Start, or resume at the next level",
		usage:   "shellforge play [level-id]",
		run:     notImplemented("play", "Day 4"),
	},
	{
		name:    "run",
		group:   groupPlaying,
		summary: "Play one specific level",
		usage:   "shellforge run <level-id> [--log-level=debug]",
		run:     cmdRun,
	},
	{
		name:    "check",
		group:   groupPlaying,
		summary: "Verify the current level",
		usage:   "shellforge check",
		run:     notImplemented("check", "Day 2"),
	},
	{
		name:    "hint",
		group:   groupPlaying,
		summary: "Reveal the next hint, after showing what it costs",
		usage:   "shellforge hint [--reveal]",
		run:     notImplemented("hint", "Day 4"),
	},
	{
		name:    "reset",
		group:   groupPlaying,
		summary: "Rebuild the current level world from scratch",
		usage:   "shellforge reset [level-id|--all]",
		run:     notImplemented("reset", "Day 2"),
	},
	{
		name:    "skip",
		group:   groupPlaying,
		summary: "Mark the current level skipped and move on",
		usage:   "shellforge skip",
		run:     notImplemented("skip", "Day 4"),
	},
	{
		name:    "map",
		group:   groupProgres,
		summary: "Show the campaign as a tree of passed, available, and locked levels",
		usage:   "shellforge map",
		run:     notImplemented("map", "Day 4"),
	},
	{
		name:    "stats",
		group:   groupProgres,
		summary: "Show XP, rank, streak, and achievements",
		usage:   "shellforge stats",
		run:     notImplemented("stats", "Day 4"),
	},
	{
		name:    "sandbox",
		group:   groupManage,
		summary: "Inspect, rebuild, or destroy the sandbox",
		usage:   "shellforge sandbox <status|shell|rebuild|destroy>",
		run:     notImplemented("sandbox", "Day 1"),
	},
	{
		name:    "bug-report",
		group:   groupManage,
		summary: "Bundle diagnostics for a GitHub issue, with the journal redacted",
		usage:   "shellforge bug-report",
		run:     notImplemented("bug-report", "Day 6"),
	},
	{
		name:    "author",
		group:   groupAuthor,
		summary: "Scaffold, validate, and golden-test levels",
		usage:   "shellforge author <scaffold|validate|test> [args]",
		run:     notImplemented("author", "Day 2"),
	},
	{
		name:    "version",
		group:   groupMeta,
		summary: "Print version and build information",
		usage:   "shellforge version",
		run:     cmdVersion,
	},
}

func lookup(name string) (command, bool) {
	for _, c := range commands {
		if c.name == name {
			return c, true
		}
	}
	return command{}, false
}

func cmdVersion(_ context.Context, _ []string) error {
	fmt.Printf("shellforge %s\n", version)
	fmt.Printf("  commit:   %s\n", commit)
	fmt.Printf("  built:    %s\n", buildDate)
	fmt.Printf("  go:       %s\n", runtime.Version())
	fmt.Printf("  platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	return nil
}

// notImplemented returns a handler for a verb that is registered but not built
// yet.
//
// It fails through the standard user-facing error path rather than printing a
// bare string, so the scaffold exercises the error contract from day zero.
func notImplemented(name, when string) func(context.Context, []string) error {
	return func(_ context.Context, _ []string) error {
		return ux.Fail(
			fmt.Sprintf("`shellforge %s` is not built yet", name),
			nil,
			fmt.Sprintf("This verb lands on %s of the build plan. Run `shellforge help` to see what works today, or read PROGRESS.md.", when),
			"",
		)
	}
}

func helpFor(w io.Writer, name string) error {
	c, ok := lookup(name)
	if !ok {
		fmt.Fprintf(os.Stderr, "shellforge: unknown command %q\n\n", name)
		printUsage(os.Stderr)
		return errUsage
	}
	fmt.Fprintf(w, "%s\n\n", c.summary)
	fmt.Fprintf(w, "Usage:\n  %s\n", c.usage)
	return nil
}

func printUsage(w io.Writer) {
	fmt.Fprintf(w, "Shellforge %s: learn the Linux command line in a terminal you can't break.\n\n", version)
	fmt.Fprintf(w, "Usage:\n  shellforge <command> [arguments]\n\n")

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, group := range groupOrder {
		fmt.Fprintf(tw, "%s\n", group)
		for _, c := range commands {
			if c.group == group {
				fmt.Fprintf(tw, "  %s\t%s\n", c.name, c.summary)
			}
		}
		fmt.Fprintln(tw)
	}
	if err := tw.Flush(); err != nil {
		return
	}

	fmt.Fprintf(w, "Run `shellforge help <command>` for usage of one command.\n")
	fmt.Fprintf(w, "First time here? Start with `shellforge doctor`.\n")
}
