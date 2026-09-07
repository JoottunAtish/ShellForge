package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/JoottunAtish/ShellForge/internal/game"
)

// The `hint` reply, and the two round trip confirmation that makes a hint
// cost something visible before it costs XP.
//
// Why two round trips rather than a prompt: the control channel is request
// and reply. The shim writes one request and prints one response, and there
// is no way to read a "y/n" back through it without inventing a second
// protocol. `hint` then `hint --yes` gives the learner the price before
// they commit and leaves an obvious record in their own scrollback.
//
// Flags are parsed here, on the host, in Go. The shim stays dumb, which is
// the stated reason it is dumb: no game logic lives inside the sandbox
// where a learner could break it during a permissions level.

// hintFlags is what `hint` was asked to do.
type hintFlags struct {
	// confirm is --yes: spend the tier rather than quote its price.
	confirm bool

	// reveal is --reveal: ask about the solution tier rather than the next
	// one.
	reveal bool
}

// parseHintFlags reads the shim's arguments strictly.
//
// The string crossed the sandbox boundary, so it is matched against a known
// set and an unknown token is refused by name rather than ignored.
// Ignoring an unknown flag is how `hint --revealed` silently costs somebody
// five XP for a tier they did not want.
func parseHintFlags(args string) (hintFlags, error) {
	var f hintFlags
	for _, arg := range strings.Fields(args) {
		switch arg {
		case "--yes", "-y":
			f.confirm = true
		case "--reveal":
			f.reveal = true
		default:
			return hintFlags{}, fmt.Errorf("%s", arg)
		}
	}
	return f, nil
}

// hinter is the part of the orchestrator the hint reply needs. Declared
// here so render_hint_test.go can drive every reply without a sandbox.
type hinter interface {
	PeekHint(reveal bool) (game.Tier, bool)
	TakeHint(ctx context.Context, reveal bool) (game.Tier, error)

	// HasReveal reports whether the level authored a solution tier at all,
	// which is what separates "this level does not offer one" from "you
	// have already bought it".
	HasReveal() bool
}

// renderHintReply answers one `hint` request. The returned string uses
// plain "\n"; the caller applies crlf.
func renderHintReply(ctx context.Context, h hinter, args string, color bool) string {
	f, err := parseHintFlags(args)
	if err != nil {
		return fmt.Sprintf("\n`hint` does not understand the option %q, so nothing was spent.\n"+
			"Run `hint` to see what the next one costs, or `hint --reveal` to ask about the solution.\n", err.Error())
	}

	if !f.confirm {
		return quoteHint(h, f.reveal)
	}
	return spendHint(ctx, h, f.reveal, color)
}

// quoteHint is the first round trip: the price, and the exact command that
// pays it.
func quoteHint(h hinter, reveal bool) string {
	tier, ok := h.PeekHint(reveal)
	if !ok {
		return ladderUnavailable(h, reveal)
	}

	if reveal {
		return fmt.Sprintf("\nRevealing the solution costs %s and ends the hint ladder for this level.\n"+
			"To spend it, run: hint --reveal --yes\n", xpAmount(tier.Cost))
	}
	return fmt.Sprintf("\nHint %d of %d costs %s.\nTo spend it, run: hint --yes\n",
		tier.Index, tier.Total, xpAmount(tier.Cost))
}

// spendHint is the second round trip: take the tier and print it.
func spendHint(ctx context.Context, h hinter, reveal bool, color bool) string {
	tier, err := h.TakeHint(ctx, reveal)
	if err != nil {
		return hintRefused(err, reveal)
	}

	p := palette(color)
	var b strings.Builder
	b.WriteString("\n")
	if tier.Reveals {
		b.WriteString(p.dim(fmt.Sprintf("Solution revealed, %s spent.", xpAmount(tier.Cost))))
	} else {
		b.WriteString(p.dim(fmt.Sprintf("Hint %d of %d, %s spent.", tier.Index, tier.Total, xpAmount(tier.Cost))))
	}
	b.WriteString("\n\n")
	b.WriteString(indentParagraph(tier.Text))
	b.WriteString("\n")

	if tier.Reveals && tier.Solution != "" {
		// The level's own solution string, verbatim rather than
		// paraphrased: it is what the golden test runs, so a learner who
		// paid to reveal sees exactly what the level considers correct.
		b.WriteString("\n")
		b.WriteString(indentParagraph(tier.Solution))
		b.WriteString("\n")
	}
	return b.String()
}

// ladderUnavailable explains a peek that had nothing to offer.
//
// Distinguishing "this level has no solution tier" from "you have already
// bought it" matters: they are different situations and the same sentence
// would be wrong for one of them. Only HasReveal can tell them apart.
// Asking whether an ordinary tier remains cannot: a level whose reveal tier
// sits in the middle of its ladder has both a bought reveal and tiers left.
func ladderUnavailable(h hinter, reveal bool) string {
	if reveal && !h.HasReveal() {
		return noRevealTierReply
	}
	if reveal {
		return "\nYou have already revealed the solution on this level, and nothing was spent.\n" +
			"Type `brief` to read the objectives again, or `check` to see how far you have got.\n"
	}
	return ladderExhaustedReply
}

// The two replies that are worded identically whether they arrive from a
// peek or from a refused take, kept in one place so they cannot drift.
const (
	noRevealTierReply = "\nThis level does not offer to reveal its solution, so nothing was spent.\n" +
		"Run `hint` to see what the next ordinary hint costs.\n"

	ladderExhaustedReply = "\nThere are no hints left on this level, and nothing was spent.\n" +
		"Type `brief` to read the objectives again, or `check` to see how far you have got.\n"
)

// hintRefused explains a take that was turned down, and says plainly that
// nothing was spent, which is the first thing somebody wonders.
func hintRefused(err error, reveal bool) string {
	switch {
	case errors.Is(err, game.ErrNoRevealTier):
		return noRevealTierReply
	case errors.Is(err, game.ErrLadderExhausted):
		return ladderExhaustedReply
	}

	what := "take a hint"
	if reveal {
		what = "reveal the solution"
	}
	return fmt.Sprintf("\nShellforge could not %s, so nothing was spent: %v\n"+
		"Your files are safe. Try again, or type `exit` and start the level over.\n", what, err)
}

// xpAmount renders an XP quantity.
//
// It exists because plural, in author.go, appends a plain -s to its noun,
// and "5 XPs" is not a thing anybody writes. XP is already plural.
func xpAmount(n int) string { return fmt.Sprintf("%d XP", n) }
