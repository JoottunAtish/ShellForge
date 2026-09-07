package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/game"
)

// fakeHinter drives every hint reply without a sandbox, a database or an
// orchestrator. It keeps the one property the replies are written against:
// peeking is free and taking is not.
type fakeHinter struct {
	tiers []game.Tier
	taken int

	// revealIdx is the 1-based tier that reveals the solution, or zero when
	// the level authored none. It is NOT assumed to be the last tier: a
	// level may mark any tier reveal_solution, and a reveal bought in the
	// middle of a ladder leaves ordinary tiers above it. That case is
	// exactly where "no reveal tier" and "already revealed" stop being
	// distinguishable by asking whether an ordinary tier remains.
	revealIdx int

	takeErr error

	peeks int
	takes int
}

func newFakeHinter() *fakeHinter {
	return &fakeHinter{
		tiers: []game.Tier{
			{Index: 1, Cost: 5, Total: 3, Text: "Kofi kept the manifests somewhere under /srv."},
			{Index: 2, Cost: 10, Total: 3, Text: "Have a look at ls -la /srv."},
			{Index: 3, Cost: 20, Total: 3, Text: "Here is the whole thing.", Reveals: true, Solution: "grep -c ERROR /srv/app.log"},
		},
		revealIdx: 3,
	}
}

// newMidLadderRevealHinter is a level whose reveal tier is the second of
// three, so buying it leaves tier 3 still on offer.
func newMidLadderRevealHinter() *fakeHinter {
	h := newFakeHinter()
	h.tiers[2].Reveals, h.tiers[2].Solution = false, ""
	h.tiers[1].Reveals, h.tiers[1].Solution = true, "grep -c ERROR /srv/app.log"
	h.revealIdx = 2
	return h
}

func (f *fakeHinter) HasReveal() bool { return f.revealIdx != 0 }

func (f *fakeHinter) PeekHint(reveal bool) (game.Tier, bool) {
	f.peeks++
	if reveal {
		if f.revealIdx == 0 || f.taken >= f.revealIdx {
			return game.Tier{}, false
		}
		tier := f.tiers[f.revealIdx-1]
		cost := 0
		for _, t := range f.tiers[f.taken:f.revealIdx] {
			cost += t.Cost
		}
		tier.Cost = cost
		return tier, true
	}
	if f.taken >= len(f.tiers) {
		return game.Tier{}, false
	}
	return f.tiers[f.taken], true
}

func (f *fakeHinter) TakeHint(_ context.Context, reveal bool) (game.Tier, error) {
	f.takes++
	if f.takeErr != nil {
		return game.Tier{}, f.takeErr
	}
	tier, ok := f.PeekHint(reveal)
	f.peeks-- // the peek inside a take is not a learner peeking
	if !ok {
		if reveal && f.revealIdx == 0 {
			return game.Tier{}, game.ErrNoRevealTier
		}
		return game.Tier{}, game.ErrLadderExhausted
	}
	f.taken = tier.Index
	return tier, nil
}

func hintReply(t *testing.T, h *fakeHinter, args string) string {
	t.Helper()
	return renderHintReply(context.Background(), h, args, false)
}

func TestHintWithNoFlagsQuotesThePriceAndSpendsNothing(t *testing.T) {
	h := newFakeHinter()
	got := hintReply(t, h, "")

	if !strings.Contains(got, "Hint 1 of 3 costs 5 XP") {
		t.Errorf("reply does not quote the price:\n%s", got)
	}
	if !strings.Contains(got, "hint --yes") {
		t.Errorf("reply does not name the command that confirms it:\n%s", got)
	}
	if h.takes != 0 {
		t.Errorf("a bare `hint` spent %d tiers, want 0", h.takes)
	}
	// The hint text itself must not leak into the quote: that would be a
	// free hint under a message about paying for one.
	if strings.Contains(got, "Kofi") {
		t.Errorf("the quote gave the hint away:\n%s", got)
	}
}

func TestHintYesSpendsExactlyOneTierAndPrintsIt(t *testing.T) {
	h := newFakeHinter()
	got := hintReply(t, h, "--yes")

	if h.takes != 1 {
		t.Errorf("`hint --yes` took %d tiers, want 1", h.takes)
	}
	if !strings.Contains(got, "Hint 1 of 3, 5 XP spent") {
		t.Errorf("reply does not say what was spent:\n%s", got)
	}
	if !strings.Contains(got, "Kofi kept the manifests") {
		t.Errorf("reply does not contain the hint:\n%s", got)
	}
}

func TestHintRevealQuotesTheWholeRemainingLadder(t *testing.T) {
	h := newFakeHinter()
	got := hintReply(t, h, "--reveal")

	// 5 plus 10 plus 20.
	if !strings.Contains(got, "costs 35 XP") {
		t.Errorf("reply does not quote the reveal price:\n%s", got)
	}
	if !strings.Contains(got, "ends the hint ladder") {
		t.Errorf("reply does not say what revealing costs beyond XP:\n%s", got)
	}
	if !strings.Contains(got, "hint --reveal --yes") {
		t.Errorf("reply does not name the command that confirms it:\n%s", got)
	}
	if h.takes != 0 {
		t.Error("a bare `hint --reveal` spent something")
	}
}

func TestHintRevealYesPrintsTheAuthoredSolutionVerbatim(t *testing.T) {
	h := newFakeHinter()
	got := hintReply(t, h, "--reveal --yes")

	if !strings.Contains(got, "grep -c ERROR /srv/app.log") {
		t.Errorf("reply does not contain the authored solution:\n%s", got)
	}
	if !strings.Contains(got, "Solution revealed") {
		t.Errorf("reply does not say the solution was revealed:\n%s", got)
	}
}

func TestHintRevealIsRefusedOnALevelWithNoRevealTier(t *testing.T) {
	h := newFakeHinter()
	h.revealIdx = 0

	for _, args := range []string{"--reveal", "--reveal --yes"} {
		got := hintReply(t, h, args)
		if !strings.Contains(got, "does not offer to reveal its solution") {
			t.Errorf("%q: reply does not explain the refusal:\n%s", args, got)
		}
		if !strings.Contains(got, "nothing was spent") {
			t.Errorf("%q: reply does not say nothing was spent:\n%s", args, got)
		}
	}
}

func TestHintPastTheLastTierSaysSoAndSpendsNothing(t *testing.T) {
	h := newFakeHinter()
	h.taken = 3

	for _, args := range []string{"", "--yes"} {
		got := hintReply(t, h, args)
		if !strings.Contains(got, "no hints left") {
			t.Errorf("%q: reply does not say the ladder is finished:\n%s", args, got)
		}
		if !strings.Contains(got, "nothing was spent") {
			t.Errorf("%q: reply does not say nothing was spent:\n%s", args, got)
		}
	}
}

func TestAnUnknownHintFlagIsRefusedByNameAndSpendsNothing(t *testing.T) {
	h := newFakeHinter()
	got := hintReply(t, h, "--revealed")

	if !strings.Contains(got, `"--revealed"`) {
		t.Errorf("reply does not name the flag it refused:\n%s", got)
	}
	if !strings.Contains(got, "nothing was spent") {
		t.Errorf("reply does not say nothing was spent:\n%s", got)
	}
	if h.takes != 0 || h.peeks != 0 {
		t.Errorf("an unknown flag reached the ladder: %d peeks, %d takes", h.peeks, h.takes)
	}
}

func TestAFailedTakeSaysNothingWasSpent(t *testing.T) {
	h := newFakeHinter()
	h.takeErr = errors.New("database is locked")

	got := hintReply(t, h, "--yes")
	if !strings.Contains(got, "nothing was spent") {
		t.Errorf("reply does not reassure the learner:\n%s", got)
	}
	if !strings.Contains(got, "Your files are safe") {
		t.Errorf("reply does not say their work is safe:\n%s", got)
	}
}

// Every reply crosses the sandbox's raw-mode PTY, so the responder puts it
// through crlf. Here that is asserted at the point it is applied, which is
// Reply rather than this renderer.
func TestHintRepliesAreCRLFTerminatedThroughTheResponder(t *testing.T) {
	got := crlf(hintReply(t, newFakeHinter(), ""))
	for i, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
		if line != "" && !strings.HasSuffix(line, "\r") {
			t.Errorf("line %d does not end in a carriage return: %q", i, line)
		}
	}
}

func TestHintRepliesHaveNoEscapeSequenceWithColourOff(t *testing.T) {
	for _, args := range []string{"", "--yes", "--reveal", "--reveal --yes", "--nonsense"} {
		got := hintReply(t, newFakeHinter(), args)
		if strings.Contains(got, "\x1b[") {
			t.Errorf("%q: reply contains an escape sequence with colour off:\n%q", args, got)
		}
	}
}

// --- regressions found in review ---

// A reveal tier that sits in the middle of a ladder, already bought, with
// ordinary tiers still above it. Asking "is an ordinary tier available"
// cannot tell that apart from "this level has no reveal tier", which is the
// exact conflation the ladder is written to avoid.
func TestARevealAlreadyBoughtIsNotReportedAsNoRevealTier(t *testing.T) {
	// The reveal tier is the second of three and has been bought, so tier 3
	// is still on offer. Asking "is an ordinary tier available" answers yes
	// here, which is why that question cannot stand in for "did this level
	// author a reveal tier".
	h := newMidLadderRevealHinter()
	h.taken = 2

	got := hintReply(t, h, "--reveal")
	if strings.Contains(got, "does not offer to reveal") {
		t.Errorf("a level that authored a reveal tier was reported as having none:\n%s", got)
	}
	if !strings.Contains(got, "already revealed") {
		t.Errorf("reply does not say the solution was already revealed:\n%s", got)
	}
	if !strings.Contains(got, "nothing was spent") {
		t.Errorf("reply does not say nothing was spent:\n%s", got)
	}
}

// The other half: a level that genuinely authored no reveal tier still gets
// the "does not offer" sentence, whether or not ordinary tiers remain.
func TestNoRevealTierIsReportedAsSuchEvenWithTiersRemaining(t *testing.T) {
	h := newFakeHinter()
	h.revealIdx = 0
	h.tiers[2].Reveals, h.tiers[2].Solution = false, ""
	h.taken = 1 // tiers 2 and 3 still on offer

	got := hintReply(t, h, "--reveal")
	if !strings.Contains(got, "does not offer to reveal") {
		t.Errorf("a level with no reveal tier was not reported as such:\n%s", got)
	}
}
