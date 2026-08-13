package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
)

// Group ids for the grouped `shellforge help` output. The titles are the ones
// the hand-rolled dispatcher used before this file replaced it with cobra.
const (
	groupSetup    = "setup"
	groupPlaying  = "playing"
	groupProgress = "progress"
	groupManage   = "manage"
	groupAuthor   = "author"
	groupMeta     = "meta"
)

// NewRootCommand builds the shellforge command tree. v carries the build
// metadata the linker fills in; see version.go.
//
// Twelve verbs come from the project brief's scope table
// (docs/design/PROJECT-BRIEF.md): doctor, init, play, run, check, hint,
// reset, skip, map, stats, sandbox, author. version and bug-report are also
// registered: version is how a learner or a bug report confirms what build
// they are running, and internal/platform/ux.Render's own fallback message
// for an unexpected error already tells the learner to run
// `shellforge bug-report`, so dropping either verb would leave that message
// pointing at a command that does not exist.
func NewRootCommand(v VersionInfo) *cobra.Command {
	root := &cobra.Command{
		Use:           "shellforge",
		Short:         "Learn the Linux command line in a terminal you can't break.",
		SilenceUsage:  true,
		SilenceErrors: true,
		// A bare `shellforge` falls back to Help, same as the old
		// dispatcher's no-args case. The real reason a RunE is set at all is
		// -v/--version below: cobra only parses a root-level flag when the
		// root has something registered to run.
		RunE: func(cmd *cobra.Command, args []string) error {
			if showVersion, _ := cmd.Flags().GetBool("version"); showVersion {
				printVersion(cmd.OutOrStdout(), v)
				return nil
			}
			return cmd.Help()
		},
	}
	// Restores the old dispatcher's `-v`/`--version` alias for the `version`
	// verb, per review on #80: cobra's own SilenceErrors turned that flag
	// into an "unknown flag" error routed through ux.Render's "this is
	// unexpected" fallback, when it used to just print the version. This is
	// a local flag, not persistent: `-v` on a subcommand still means
	// whatever that subcommand defines it to mean, matching the old
	// dispatcher, which only ever recognized -v/--version as the first
	// argument.
	root.Flags().BoolP("version", "v", false, "Print version and build information")

	// Shell completion belongs to the Day 3 doctor ticket, per #48's own
	// scope. cobra registers a `completion` subcommand by default; turn it
	// back off rather than ship it early and undocumented.
	root.CompletionOptions.DisableDefaultCmd = true

	root.AddGroup(
		&cobra.Group{ID: groupSetup, Title: "Setting up"},
		&cobra.Group{ID: groupPlaying, Title: "Playing"},
		&cobra.Group{ID: groupProgress, Title: "Progress"},
		&cobra.Group{ID: groupManage, Title: "Managing the sandbox"},
		&cobra.Group{ID: groupAuthor, Title: "Authoring content"},
		&cobra.Group{ID: groupMeta, Title: "About"},
	)

	root.AddCommand(
		stubCommand("shellforge doctor", "doctor [--fix] [--json]", groupSetup,
			"Check this machine and explain how to fix anything missing", "Day 3"),
		stubCommand("shellforge init", "init [--runtime=auto|wsl|docker]", groupSetup,
			"Provision the sandbox. Run once, takes a few minutes", "Day 1"),
		stubCommand("shellforge play", "play [level-id]", groupPlaying,
			"Start, or resume at the next level", "Day 4"),
		newRunCommand(),
		stubCommand("shellforge check", "check", groupPlaying,
			"Verify the current level", "Day 2"),
		stubCommand("shellforge hint", "hint [--reveal]", groupPlaying,
			"Reveal the next hint, after showing what it costs", "Day 4"),
		stubCommand("shellforge reset", "reset [level-id|--all]", groupPlaying,
			"Rebuild the current level world from scratch", "Day 2"),
		stubCommand("shellforge skip", "skip", groupPlaying,
			"Mark the current level skipped and move on", "Day 4"),
		stubCommand("shellforge map", "map", groupProgress,
			"Show the campaign as a tree of passed, available, and locked levels", "Day 4"),
		stubCommand("shellforge stats", "stats", groupProgress,
			"Show XP, rank, streak, and achievements", "Day 4"),
		newSandboxCommand(),
		stubCommand("shellforge bug-report", "bug-report", groupManage,
			"Bundle diagnostics for a GitHub issue, with the journal redacted", "Day 6"),
		newAuthorCommand(),
		newVersionCommand(v),
	)

	return root
}

// newRunCommand returns `shellforge run`, the one verb with real behaviour
// today. Flag parsing is disabled so the existing, already tested
// parseRunArgs in cmd_run.go keeps owning --log-level, rather than this
// ticket rewriting logic it was not asked to touch.
func newRunCommand() *cobra.Command {
	return &cobra.Command{
		Use:                "run <level-id> [--log-level=debug]",
		GroupID:            groupPlaying,
		Short:              "Play one specific level",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdRun(cmd.Context(), args)
		},
	}
}

// newSandboxCommand returns the `sandbox` group and its four stub
// subcommands.
func newSandboxCommand() *cobra.Command {
	sandbox := &cobra.Command{
		Use:     "sandbox",
		GroupID: groupManage,
		Short:   "Inspect, rebuild, or destroy the sandbox",
	}
	sandbox.AddCommand(
		stubCommand("shellforge sandbox build", "build", "",
			"Build the sandbox image", "Day 1"),
		stubCommand("shellforge sandbox rebuild", "rebuild", "",
			"Rebuild the sandbox image and container from scratch", "Day 1"),
		stubCommand("shellforge sandbox destroy", "destroy", "",
			"Remove the sandbox container and image", "Day 1"),
		stubCommand("shellforge sandbox status", "status", "",
			"Show whether the sandbox is provisioned and running", "Day 1"),
	)
	return sandbox
}

// newAuthorCommand returns the `author` group and its four stub
// subcommands.
func newAuthorCommand() *cobra.Command {
	author := &cobra.Command{
		Use:     "author",
		GroupID: groupAuthor,
		Short:   "Scaffold, validate, and golden-test levels",
	}
	author.AddCommand(
		stubCommand("shellforge author validate", "validate <pack-path>", "",
			"Validate a content pack against docs/LEVEL-FORMAT.md", "Day 2"),
		stubCommand("shellforge author scaffold", "scaffold <level-id>", "",
			"Generate a new level's YAML and asset skeleton", "Day 2"),
		stubCommand("shellforge author test", "test [--all|level-id]", "",
			"Run the golden test: setup, checks fail, solution, checks pass", "Day 2"),
		stubCommand("shellforge author record", "record <level-id>", "",
			"Capture a golden recording for one level", "Day 2"),
	)
	return author
}

// stubCommand returns a *cobra.Command for a verb the build plan has not
// reached yet. Registering it now, rather than only once it is implemented,
// keeps `shellforge help` an honest map of the product: an unbuilt verb
// answers with a remediation instead of cobra's own "unknown command".
//
// name is the full invocation for the error message, such as
// "shellforge author validate". usage is the local Use line cobra shows
// under this command, such as "validate <pack-path>".
func stubCommand(name, usage, group, short, when string) *cobra.Command {
	return &cobra.Command{
		Use:     usage,
		GroupID: group,
		Short:   short,
		Args:    cobra.ArbitraryArgs,
		RunE: func(*cobra.Command, []string) error {
			return ux.Fail(
				fmt.Sprintf("`%s` is not built yet", name),
				nil,
				fmt.Sprintf("This verb lands on %s of the build plan. Run `shellforge help` to see what works today, or read PROGRESS.md.", when),
				"",
			)
		},
	}
}
