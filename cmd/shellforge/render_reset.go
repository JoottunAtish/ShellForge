package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/JoottunAtish/ShellForge/internal/content/setup"
)

// The `reset` reply: the escape hatch that makes the whole product safe to
// promise, and the confirmation that keeps it from being a fright.
//
// Same two round trip shape as `hint`, for the same reason: the control
// channel is request and reply, there is no way to read a "y/n" back, and
// deleting a learner's work on a bare `reset` typed by accident is exactly
// the experience this game exists to prevent.
//
// The first reply names the level root and states plainly that files
// elsewhere in the sandbox are untouched. Hard rule 7 of the
// destructive-safety skill says reset must not take a file the learner
// saved outside the level root, and this message is how they know that
// before they commit rather than afterwards.

// resetter is the part of the orchestrator the reset reply needs.
type resetter interface {
	Reset(ctx context.Context) error
}

// parseResetFlags reads the shim's arguments strictly, the same way
// parseHintFlags does and for the same reason.
func parseResetFlags(args string) (confirm bool, err error) {
	for _, arg := range strings.Fields(args) {
		switch arg {
		case "--yes", "-y":
			confirm = true
		default:
			return false, fmt.Errorf("%s", arg)
		}
	}
	return confirm, nil
}

// renderResetReply answers one `reset` request. The returned string uses
// plain "\n"; the caller applies crlf.
func renderResetReply(ctx context.Context, r resetter, root, args string) string {
	confirm, err := parseResetFlags(args)
	if err != nil {
		return fmt.Sprintf("\n`reset` does not understand the option %q, so nothing was deleted.\n"+
			"Run `reset` on its own to see what it would rebuild.\n", err.Error())
	}

	if !confirm {
		return fmt.Sprintf("\nThis rebuilds the level from scratch. Anything you have changed under\n"+
			"%s is deleted. Files elsewhere in your sandbox are untouched.\n"+
			"To go ahead, run: reset --yes\n", root)
	}

	if err := r.Reset(ctx); err != nil {
		return resetFailed(err, root)
	}
	return "\nLevel rebuilt. Type `brief` to see the objectives again.\n"
}

// resetFailed explains a rebuild that did not happen.
//
// The unsafe root case gets its own sentence because it is the one a
// learner can neither cause nor fix: it means the level's own YAML names a
// path Shellforge will not delete, which is a content bug and is reported
// as one.
func resetFailed(err error, root string) string {
	if errors.Is(err, setup.ErrUnsafeLevelRoot) {
		return fmt.Sprintf("\nShellforge refused to rebuild this level, and deleted nothing.\n"+
			"The level asks for its files to live at %s, which is not a path Shellforge\n"+
			"will remove. This is a problem with the level rather than with anything you did.\n"+
			"Type `exit`, then run `shellforge bug-report`.\n", root)
	}
	return fmt.Sprintf("\nreset could not finish: %v\n\n"+
		"Type `exit`, then start the level again. Setup removes the level world before\n"+
		"rebuilding it, so a half-finished rebuild cannot wedge it.\n", err)
}
