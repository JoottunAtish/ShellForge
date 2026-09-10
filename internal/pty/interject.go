package pty

import (
	"io"
	"sync"
)

// maxPromptCapture bounds the bytes screen will remember as one prompt.
//
// A prompt is a handful of bytes plus colour escapes, so this is generous by
// two orders of magnitude. It exists because the capture window is opened by
// a marker the sandbox emits, and the learner owns the shell that emits it:
// `PS1='\[\e]133;A\a\]'"$(head -c 10000000 /dev/zero | tr '\0' x)"` is a
// thing a curious learner can type. Overshooting the bound abandons that
// prompt rather than truncating it, because half a prompt redrawn is worse
// than no redraw at all.
const maxPromptCapture = 4096

// maxQueuedInterjections bounds the messages waiting for the next prompt.
//
// One live verification pass produces at most one message, and a pass is
// debounced to a few per second, so a learner who starts a long-running
// command and walks away is the only way to reach even a fraction of this.
// Past the bound the OLDEST message is dropped: the newest description of an
// objective's status is the true one, and the stale one is what nobody
// needs.
const maxQueuedInterjections = 16

// screen owns every byte written to the host terminal, and exists so that a
// goroutine other than the output copy goroutine can write one safely.
//
// Before this type there was exactly one writer, io.Copy through the OSC
// parser, and the live objective printer wrote to os.Stdout beside it with
// nothing ordering the two. That is two problems in one. The small one is
// interleaving: a message landing in the middle of a colour escape the shell
// was emitting corrupts both. The visible one is that the shell had already
// drawn its prompt, so a message printed after it left the cursor sitting on
// a blank line under a prompt that was now scrolled away, and a learner who
// had just solved the level saw what looked like a hung terminal and pressed
// Enter to get their prompt back.
//
// The fix is not to redraw the prompt by injecting a keystroke: the shell is
// real bash and the foreground program might be vim, where a stray key edits
// the learner's file. It is to write only at a moment when writing is
// provably safe, and to know the prompt's own bytes so it can be reprinted
// verbatim rather than guessed at:
//
//   - The shell is at a prompt (between OSC 133;B and the next OSC 133;C)
//     with an empty input line. Write now, then reprint the prompt.
//   - Anything else: a command is running, the learner is mid-line, or the
//     prompt is not captured. Queue, and let onOSCEvent flush the queue at
//     the next OSC 133;A, immediately before the prompt is drawn.
//
// Every field is guarded by mu, which is also held across the write to out,
// so the two writers can never interleave within a single write.
//
// TODO(v0.2): the queued path can still make a learner wait for their next
// prompt to see a tick they earned a minute ago. Printing above a line the
// learner is typing into means owning the screen, which is the TUI CLAUDE.md
// cuts for v0.1.
type screen struct {
	mu  sync.Mutex
	out io.Writer

	// prompt is the last fully captured prompt, markers already stripped by
	// the parser, ready to be written back verbatim. Nil until one prompt
	// has been seen end to end.
	prompt []byte

	// capture accumulates the prompt currently being drawn. capturing says
	// whether the window between OSC 133;A and OSC 133;B is open, and
	// spoiled says this prompt overran maxPromptCapture and must not be
	// remembered.
	capture   []byte
	capturing bool
	spoiled   bool

	// atPrompt is true only between OSC 133;B and the next OSC 133;C: the
	// prompt is drawn and readline, not some full screen program, owns the
	// screen.
	atPrompt bool

	// queued holds messages waiting for the next prompt, oldest first.
	queued []string

	// banner is reprinted immediately before the next prompt whenever the
	// learner clears their screen, and is the level briefing: it is the only
	// thing on that screen the learner cannot get back for themselves, since
	// it was printed once before the shell was attached and `clear` takes
	// the scrollback with it. Empty until a caller sets one, which turns the
	// whole behaviour off for a session that has no briefing to restore.
	banner string

	// cleared records that the primary screen was erased in full since the
	// last prompt, per csi. It is consumed by the flush at the next prompt.
	cleared bool

	// csi is the scanner that sets cleared. See csi.go for why an erase
	// inside the alternate screen buffer, which is vim or less or man
	// painting itself, is not one.
	csi csiScanner

	// closed stops every write once Run is unwinding, so nothing prints
	// into a terminal whose raw mode has been restored.
	closed bool
}

// open points the screen at the host terminal writer. New calls it, not Run,
// and the difference is load bearing: the caller starts its live printer
// goroutine before it calls Run, so a tick that arrives in that window would
// be dropped by a screen that had no writer yet. With the writer set at
// construction it is queued instead, and printed at the first prompt.
func (s *screen) open(out io.Writer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.out = out
}

// close stops every later write. Anything still queued is dropped: the
// terminal it was meant for is about to stop belonging to the game.
func (s *screen) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.queued = nil
}

// setBanner sets the text reprinted before the next prompt after the learner
// clears the screen. An empty string turns the behaviour off.
func (s *screen) setBanner(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.banner = text
}

// Write implements io.Writer and is what the OSC parser forwards the shell's
// output through. It writes to the host terminal unmodified and, while a
// prompt is being drawn, keeps a copy of the bytes for Interject to reprint.
//
// The returned count is the underlying writer's own, so the parser's byte
// accounting, and therefore io.Copy's, is unaffected by the capture.
func (s *screen) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.csi.scan(p) {
		s.cleared = true
	}

	if s.capturing && !s.spoiled {
		if len(s.capture)+len(p) > maxPromptCapture {
			s.spoiled = true
			s.capture = nil
		} else {
			s.capture = append(s.capture, p...)
		}
	}
	if s.out == nil {
		return len(p), nil
	}
	return s.out.Write(p)
}

// beginPrompt opens the capture window at OSC 133;A.
func (s *screen) beginPrompt() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.capturing = true
	s.spoiled = false
	s.capture = nil
	s.atPrompt = false
}

// endPrompt closes the capture window at OSC 133;B and promotes what it
// caught to the prompt Interject may reprint.
//
// A window that was never opened, which is what a stray OSC 133;B from a
// learner running `printf '\e]133;B\a'` looks like, promotes nothing: the
// bytes before it are ordinary command output, not a prompt.
func (s *screen) endPrompt() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.capturing && !s.spoiled {
		s.prompt = s.capture
	}
	s.capture = nil
	s.capturing = false
	s.spoiled = false
	s.atPrompt = true
}

// leavePrompt closes the safe-to-write window at OSC 133;C. From here until
// the next prompt the foreground program owns the screen.
func (s *screen) leavePrompt() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.atPrompt = false
	s.capturing = false
	s.capture = nil
}

// flushBeforePrompt writes everything waiting, in order, and is called at
// OSC 133;A: immediately before the shell draws its prompt, so everything it
// writes lands above that prompt with no reprint needed and no line to
// disturb.
//
// The briefing goes first and the queued ticks after it, so the newest thing
// the learner is told is the one nearest their cursor. The order matters on
// exactly one screen: a learner who clears at the moment an objective ticks
// would otherwise read the tick and then a checklist that contradicts it,
// since the reprinted briefing shows every box unchecked the way it did at
// the start of the level.
func (s *screen) flushBeforePrompt() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed || s.out == nil {
		return
	}

	if s.cleared {
		s.cleared = false
		if s.banner != "" {
			_, _ = io.WriteString(s.out, s.banner)
		}
	}

	for _, msg := range s.queued {
		_, _ = io.WriteString(s.out, msg)
	}
	s.queued = nil
}

// interject writes msg to the host terminal, either now or at the next
// prompt. See screen's own doc comment for which, and why.
//
// typed reports whether the learner has pressed a key since the current
// prompt finished drawing. It is passed in rather than read here because it
// lives on the Mux as an atomic, written by the stdin copy goroutine, and
// this type deliberately owns nothing it does not guard with mu.
func (s *screen) interject(msg string, typed bool) {
	if msg == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed || s.out == nil {
		return
	}

	// Everything this condition asks about is settled under mu, except
	// typed, which the learner can change at any instant. That race is
	// benign in the only direction it can go: a keystroke whose echo has
	// already reached the screen was seen by the tap first, so it is
	// already true here, and a keystroke that has not been echoed yet
	// arrives after the reprinted prompt, which is where it belongs. See
	// stdinTap.
	if !s.atPrompt || typed || len(s.prompt) == 0 {
		s.queued = append(s.queued, msg)
		if len(s.queued) > maxQueuedInterjections {
			s.queued = s.queued[len(s.queued)-maxQueuedInterjections:]
		}
		return
	}

	// The cursor is sitting at the end of an empty prompt line. msg opens
	// with its own CRLF, so it starts on a fresh line rather than on the end
	// of the prompt, and closes with one, so the reprinted prompt starts on
	// a fresh line too. Reprinting rather than moving the cursor is what
	// makes this correct with the prompt on the last row of the screen,
	// where inserting a line above it would push it off the bottom.
	if _, err := io.WriteString(s.out, msg); err != nil {
		return
	}
	_, _ = s.out.Write(s.prompt)
}

// Interject delivers msg to the learner's terminal without corrupting what
// the shell is doing with it.
//
// It writes msg immediately when the shell is idle at a prompt the learner
// has not typed into, and reprints that prompt underneath so the learner is
// left looking at a live prompt rather than a bare cursor. Otherwise it
// holds msg and writes it just before the shell draws its next prompt.
// Either way it never blocks on the learner and never writes a byte into a
// full screen program's display.
//
// msg is written verbatim. A caller writing into a terminal Run holds in raw
// mode must terminate its own lines with CRLF, because a bare LF does not
// return the carriage there. It should also begin with one, so the message
// starts on a line of its own rather than on the end of the prompt.
//
// Safe for concurrent use, and a no-op once Run has begun unwinding.
func (m *Mux) Interject(msg string) {
	m.screen.interject(msg, m.typedSincePrompt.Load())
}

// SetClearBanner sets text the multiplexer reprints, immediately above the
// next prompt, whenever the learner clears their screen.
//
// It exists for one thing, the level briefing, which is printed once before
// the shell is attached and is therefore the only thing on that screen the
// learner cannot get back for themselves: `clear` erases the scrollback
// along with the display, and a learner three commands into a level who has
// forgotten which file they were asked to write has nothing to read.
//
// The text is opaque here. Mux is a byte pump at L2 and knows nothing about
// levels; a caller renders the briefing and hands the bytes over. Passing an
// empty string turns the behaviour off.
//
// text is written verbatim, so a caller must terminate its lines with CRLF:
// the terminal is in raw mode and a bare LF does not return the carriage.
// Unlike Interject's message it should NOT open with one, because the cursor
// is at the top left of a screen that was just erased.
//
// What counts as clearing the screen is decided in csi.go, and the short
// version is that vim does not: an erase inside the alternate screen buffer
// is a full screen program painting itself, and the learner's own screen
// comes back intact when it exits.
//
// Safe for concurrent use, and safe to call before Run.
func (m *Mux) SetClearBanner(text string) {
	m.screen.setBanner(text)
}
