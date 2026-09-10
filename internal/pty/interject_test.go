package pty

import (
	"context"
	"strings"
	"testing"
	"time"
)

// --- OSC wire fixtures for the prompt markers -----------------------------

func promptStartSeq() string { return "\x1b]133;A\x07" }

func commandStartSeq() string { return "\x1b]133;B\x07" }

// drawPrompt feeds a whole prompt through the fake PTY the way
// images/rc/instrument.bash emits one: OSC 133;A, the visible prompt bytes,
// then OSC 133;B.
func drawPrompt(t *testing.T, p *fakePTY, prompt string) {
	t.Helper()
	p.feedOutput(t, promptStartSeq()+prompt+commandStartSeq())
}

const testPrompt = "learner@quest:~$ "

// TestInterjectAtAnIdlePromptReprintsIt is the regression test for the bug a
// learner hit on nav-01: they solved the level, the live checker printed the
// objective tick, and the terminal looked hung because the prompt was now
// scrolled off above the cursor. Pressing Enter was the only way to get a
// prompt back.
//
// The message must land, and a prompt must follow it, in that order.
func TestInterjectAtAnIdlePromptReprintsIt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p, mux, _, out := newTestMux(t)
	done := runAsync(mux, ctx)

	drawPrompt(t, p, testPrompt)
	waitUntil(t, 2*time.Second, func() bool {
		return strings.Contains(out.String(), testPrompt)
	})

	mux.Interject("\r\n  [ok] you are standing in the right place\r\n")

	waitUntil(t, 2*time.Second, func() bool {
		return strings.Count(out.String(), testPrompt) == 2
	})

	got := out.String()
	tick := strings.Index(got, "[ok]")
	second := strings.LastIndex(got, testPrompt)
	if tick < 0 {
		t.Fatalf("the message never reached the terminal: %q", got)
	}
	if second < tick {
		t.Errorf("the prompt was not reprinted under the message: %q", got)
	}

	cancel()
	<-done
}

// TestInterjectWhileACommandRunsWaitsForTheNextPrompt covers the case that
// makes keystroke injection unusable here: the foreground program between
// OSC 133;C and the next prompt may be vim, and nothing but vim may write to
// that screen. The message must be held, then printed just above the prompt
// that follows.
func TestInterjectWhileACommandRunsWaitsForTheNextPrompt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p, mux, _, out := newTestMux(t)
	done := runAsync(mux, ctx)

	// The marker and the first line of output are fed together, so waiting
	// for that line proves the parser has already consumed the marker.
	// Waiting on the prompt alone would not: the marker would still be in
	// flight, and the test would assert against a shell that is, as far as
	// the Mux knows, still sitting at its prompt.
	drawPrompt(t, p, testPrompt)
	p.feedOutput(t, preExecSeq()+"first line\r\n")
	waitUntil(t, 2*time.Second, func() bool {
		return strings.Contains(out.String(), "first line")
	})

	mux.Interject("\r\n  [ok] held\r\n")

	// Give the message every chance to escape before the assertion.
	p.feedOutput(t, "second line\r\n")
	waitUntil(t, 2*time.Second, func() bool {
		return strings.Contains(out.String(), "second line")
	})
	if strings.Contains(out.String(), "[ok] held") {
		t.Fatalf("the message printed into a running command's output: %q", out.String())
	}

	p.feedOutput(t, commandDoneSeq(0)+cwdReportSeq("/home/learner"))
	drawPrompt(t, p, testPrompt)

	// Waiting for the message alone would race the assertion below: the
	// flush happens at OSC 133;A, which is BEFORE the prompt bytes it is
	// meant to land above have been forwarded. Waiting for the second
	// prompt is what makes both halves of the ordering present to compare.
	waitUntil(t, 2*time.Second, func() bool {
		return strings.Count(out.String(), testPrompt) == 2
	})

	got := out.String()
	if strings.Index(got, "[ok] held") > strings.LastIndex(got, testPrompt) {
		t.Errorf("the held message printed under the new prompt instead of above it: %q", got)
	}

	cancel()
	<-done
}

// TestInterjectWhileTheLearnerIsTypingWaitsForTheNextPrompt covers the other
// unsafe moment. Reprinting a prompt under a half typed line would leave the
// screen disagreeing with readline's buffer about what the learner has
// entered, so the message waits instead.
func TestInterjectWhileTheLearnerIsTypingWaitsForTheNextPrompt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p, mux, in, out := newTestMux(t)
	done := runAsync(mux, ctx)

	drawPrompt(t, p, testPrompt)
	waitUntil(t, 2*time.Second, func() bool {
		return strings.Contains(out.String(), testPrompt)
	})

	if _, err := in.Write([]byte("ech")); err != nil {
		t.Fatalf("write to the fake stdin: %v", err)
	}
	waitUntil(t, 2*time.Second, func() bool {
		return strings.Contains(string(p.writesSnapshot()), "ech")
	})

	mux.Interject("\r\n  [ok] held\r\n")

	p.feedOutput(t, "ech")
	waitUntil(t, 2*time.Second, func() bool {
		return strings.Contains(out.String(), testPrompt+"ech")
	})
	if strings.Contains(out.String(), "[ok] held") {
		t.Fatalf("the message printed over a line the learner was typing: %q", out.String())
	}

	drawPrompt(t, p, testPrompt)
	waitUntil(t, 2*time.Second, func() bool {
		return strings.Contains(out.String(), "[ok] held")
	})

	cancel()
	<-done
}

// TestInterjectBeforeAnyPromptWaitsForOne pins the startup case: the very
// first live pass can finish before the shell has drawn a prompt at all, and
// there is then no prompt to reprint. The message must be held rather than
// dropped or printed with no prompt after it.
func TestInterjectBeforeAnyPromptWaitsForOne(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p, mux, _, out := newTestMux(t)
	done := runAsync(mux, ctx)

	mux.Interject("\r\n  [ok] early\r\n")

	// Waiting for the prompt, not the message: the flush happens at OSC
	// 133;A, before the prompt bytes it is meant to land above have been
	// forwarded, so waiting for the message alone would race the ordering
	// assertion below.
	drawPrompt(t, p, testPrompt)
	waitUntil(t, 2*time.Second, func() bool {
		return strings.Contains(out.String(), testPrompt)
	})

	got := out.String()
	if !strings.Contains(got, "[ok] early") {
		t.Fatalf("the held message was dropped instead of printed at the first prompt: %q", got)
	}
	if strings.Index(got, "[ok] early") > strings.Index(got, testPrompt) {
		t.Errorf("the held message printed under the first prompt instead of above it: %q", got)
	}

	cancel()
	<-done
}

// TestPromptCaptureDoesNotAlterTheForwardedStream is the one assertion this
// whole mechanism cannot be allowed to break. Capturing the prompt means
// reading the bytes on their way to the terminal, and the learner's shell is
// real bash: vim depends on that stream arriving literally, not
// approximately, unmodified.
func TestPromptCaptureDoesNotAlterTheForwardedStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p, mux, _, out := newTestMux(t)
	done := runAsync(mux, ctx)

	const body = "\x1b[2J\x1b[H~\r\n~\r\n\x1b[1;31mVIM\x1b[0m"
	p.feedOutput(t, promptStartSeq()+"\x1b[32m"+testPrompt+"\x1b[0m"+commandStartSeq()+preExecSeq()+body)

	want := "\x1b[32m" + testPrompt + "\x1b[0m" + body
	waitUntil(t, 2*time.Second, func() bool { return out.String() == want })

	if got := out.String(); got != want {
		t.Errorf("forwarded stream = %q, want %q", got, want)
	}

	cancel()
	<-done
}

// TestInterjectAfterRunReturnsIsDropped pins the teardown rule. Run restores
// the host terminal out of raw mode on its way out, and a message printed
// after that lands in a terminal the game no longer owns, after "Shell
// exited."
func TestInterjectAfterRunReturnsIsDropped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p, mux, _, out := newTestMux(t)
	done := runAsync(mux, ctx)

	drawPrompt(t, p, testPrompt)
	waitUntil(t, 2*time.Second, func() bool {
		return strings.Contains(out.String(), testPrompt)
	})

	cancel()
	<-done

	before := out.Len()
	mux.Interject("\r\n  [ok] too late\r\n")
	if out.Len() != before {
		t.Errorf("Interject wrote to a restored terminal: %q", out.String())
	}
}

// TestInterjectDropsTheOldestWhenTheQueueFills pins the bound. A learner who
// starts a long command and walks away must not grow this queue without
// limit, and when something has to go it is the stale description of an
// objective, never the current one.
func TestInterjectDropsTheOldestWhenTheQueueFills(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p, mux, _, out := newTestMux(t)
	done := runAsync(mux, ctx)

	// No prompt has been drawn, so every one of these queues.
	mux.Interject("oldest\r\n")
	for i := 0; i < maxQueuedInterjections; i++ {
		mux.Interject("filler\r\n")
	}
	mux.Interject("newest\r\n")

	drawPrompt(t, p, testPrompt)
	waitUntil(t, 2*time.Second, func() bool {
		return strings.Contains(out.String(), "newest")
	})

	if strings.Contains(out.String(), "oldest") {
		t.Errorf("the queue grew past its bound instead of dropping the oldest message: %q", out.String())
	}

	cancel()
	<-done
}

// TestAnOversizedPromptIsNotReprinted pins the capture bound. The learner
// owns the shell that emits the prompt markers, so a prompt can be any size
// they like; past the bound the game forgets it rather than reprinting half
// of one, and the message waits for the next prompt instead.
func TestAnOversizedPromptIsNotReprinted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p, mux, _, out := newTestMux(t)
	done := runAsync(mux, ctx)

	huge := strings.Repeat("x", maxPromptCapture+1)
	drawPrompt(t, p, huge)
	waitUntil(t, 2*time.Second, func() bool {
		return strings.Contains(out.String(), huge)
	})

	mux.Interject("\r\n  [ok] held\r\n")

	drawPrompt(t, p, testPrompt)
	waitUntil(t, 2*time.Second, func() bool {
		return strings.Contains(out.String(), testPrompt)
	})

	got := out.String()
	if !strings.Contains(got, "[ok] held") {
		t.Fatalf("the held message was dropped instead of printed at the next prompt: %q", got)
	}
	if strings.Index(got, "[ok] held") > strings.Index(got, testPrompt) {
		t.Errorf("the message printed under the prompt instead of above it: %q", got)
	}

	cancel()
	<-done
}

// TestAForgedPromptEndMarkerReprintsNothing covers the learner in Act VI who
// types printf '\e]133;B\a' by hand. The bytes before a marker that closes a
// window nothing opened are ordinary command output, not a prompt, and
// reprinting them as one would put a stale line under every tick. With no
// prompt to reprint the message waits for a real one, which is the same path
// a tick that lands before the first prompt takes.
func TestAForgedPromptEndMarkerReprintsNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p, mux, _, out := newTestMux(t)
	done := runAsync(mux, ctx)

	p.feedOutput(t, "not a prompt at all"+commandStartSeq()+"\r\n")
	waitUntil(t, 2*time.Second, func() bool {
		return strings.Contains(out.String(), "not a prompt at all")
	})

	mux.Interject("\r\n  [ok] tick\r\n")

	drawPrompt(t, p, testPrompt)
	waitUntil(t, 2*time.Second, func() bool {
		return strings.Contains(out.String(), "[ok] tick")
	})

	if strings.Count(out.String(), "not a prompt at all") != 1 {
		t.Errorf("command output was reprinted as if it were a prompt: %q", out.String())
	}

	cancel()
	<-done
}

// --- the clear banner -----------------------------------------------------

const testBanner = "----------\r\nFirst Contact\r\n\r\nFind out where you are standing.\r\n----------\r\n\r\n"

// clearSeq is what `clear` and readline's Ctrl-L both put on the wire: home
// the cursor, erase the display, and on a modern terminfo entry erase the
// scrollback with it. That third one is why a learner cannot simply scroll
// back to the briefing.
func clearSeq() string { return "\x1b[H\x1b[2J\x1b[3J" }

// runCommand feeds the marker sequence one command produces, from the shell
// reading the line to the shell reporting where it ended up.
func runCommand(t *testing.T, p *fakePTY, output string) {
	t.Helper()
	p.feedOutput(t, preExecSeq()+output+commandDoneSeq(0)+cwdReportSeq("/home/learner"))
}

// TestClearReprintsTheBriefing is the regression test for the third defect a
// learner hit on nav-01: the quest text is printed once, before the shell is
// attached, so `clear` takes it away along with the scrollback and there is
// nothing left saying what the level asked for.
func TestClearReprintsTheBriefing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p, mux, _, out := newTestMux(t)
	mux.SetClearBanner(testBanner)
	done := runAsync(mux, ctx)

	drawPrompt(t, p, testPrompt)
	runCommand(t, p, clearSeq())
	drawPrompt(t, p, testPrompt)

	waitUntil(t, 2*time.Second, func() bool {
		return strings.Count(out.String(), testPrompt) == 2
	})

	got := out.String()
	if !strings.Contains(got, "First Contact") {
		t.Fatalf("the briefing was not reprinted after a clear: %q", got)
	}
	if strings.Index(got, "First Contact") > strings.LastIndex(got, testPrompt) {
		t.Errorf("the briefing printed under the new prompt instead of above it: %q", got)
	}

	cancel()
	<-done
}

// TestAnOrdinaryCommandDoesNotReprintTheBriefing is the other half: the
// briefing comes back when the screen was erased and at no other time, or
// every prompt in the level would carry it.
func TestAnOrdinaryCommandDoesNotReprintTheBriefing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p, mux, _, out := newTestMux(t)
	mux.SetClearBanner(testBanner)
	done := runAsync(mux, ctx)

	drawPrompt(t, p, testPrompt)
	runCommand(t, p, "quest  welcome.txt\r\n")
	drawPrompt(t, p, testPrompt)

	waitUntil(t, 2*time.Second, func() bool {
		return strings.Count(out.String(), testPrompt) == 2
	})

	if strings.Contains(out.String(), "First Contact") {
		t.Errorf("the briefing was reprinted after a command that cleared nothing: %q", out.String())
	}

	cancel()
	<-done
}

// TestVimDoesNotReprintTheBriefing is the assertion that decides whether this
// feature is usable at all. vim, less, man and htop erase the screen
// constantly, and man is what nav-04 asks the learner to run; a briefing
// reprinted every time one of them repaints would be unreadable noise. The
// alternate screen buffer is what tells them apart, and it is also why
// quitting them restores the screen that was there before, briefing
// included.
func TestVimDoesNotReprintTheBriefing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p, mux, _, out := newTestMux(t)
	mux.SetClearBanner(testBanner)
	done := runAsync(mux, ctx)

	drawPrompt(t, p, testPrompt)
	// Enter the alternate screen, repaint it several times, leave.
	runCommand(t, p, "\x1b[?1049h"+clearSeq()+"~\r\n"+clearSeq()+"~\r\n\x1b[?1049l")
	drawPrompt(t, p, testPrompt)

	waitUntil(t, 2*time.Second, func() bool {
		return strings.Count(out.String(), testPrompt) == 2
	})

	if strings.Contains(out.String(), "First Contact") {
		t.Errorf("a full screen program's own repaint reprinted the briefing: %q", out.String())
	}

	cancel()
	<-done
}

// TestClearWithNoBannerSetPrintsNothing pins the off switch. A session with
// no briefing to restore, which is every use of Mux outside a level, must
// behave exactly as it did before this existed.
func TestClearWithNoBannerSetPrintsNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p, mux, _, out := newTestMux(t)
	done := runAsync(mux, ctx)

	drawPrompt(t, p, testPrompt)
	runCommand(t, p, clearSeq())
	drawPrompt(t, p, testPrompt)

	waitUntil(t, 2*time.Second, func() bool {
		return strings.Count(out.String(), testPrompt) == 2
	})

	want := testPrompt + clearSeq() + testPrompt
	if got := out.String(); got != want {
		t.Errorf("forwarded stream = %q, want %q", got, want)
	}

	cancel()
	<-done
}

// TestTheBriefingPrintsAboveALiveTick pins the order the two writers land in
// when a learner clears the screen at the moment an objective ticks. The
// reprinted briefing shows every box unchecked, the way it did at the start
// of the level, so a tick printed above it would be contradicted by the
// checklist underneath.
func TestTheBriefingPrintsAboveALiveTick(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p, mux, _, out := newTestMux(t)
	mux.SetClearBanner(testBanner)
	done := runAsync(mux, ctx)

	drawPrompt(t, p, testPrompt)
	p.feedOutput(t, preExecSeq()+clearSeq())
	waitUntil(t, 2*time.Second, func() bool {
		return strings.Contains(out.String(), clearSeq())
	})

	mux.Interject("\r\n  [ok] tick\r\n")

	p.feedOutput(t, commandDoneSeq(0)+cwdReportSeq("/home/learner"))
	drawPrompt(t, p, testPrompt)
	waitUntil(t, 2*time.Second, func() bool {
		return strings.Count(out.String(), testPrompt) == 2
	})

	got := out.String()
	if !strings.Contains(got, "[ok] tick") || !strings.Contains(got, "First Contact") {
		t.Fatalf("both the briefing and the tick should have printed: %q", got)
	}
	if strings.Index(got, "First Contact") > strings.Index(got, "[ok] tick") {
		t.Errorf("the tick printed above the briefing that contradicts it: %q", got)
	}

	cancel()
	<-done
}
