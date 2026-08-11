package pty

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

// eventBufferSize sizes Mux's buffered Events channel. 64 is large enough
// that a normal play session's burst of quick commands, a pasted one-liner,
// a shell script running unattended, does not trip the drop path in emit,
// and small enough that a consumer that has stopped calling Events entirely
// is detected within the session rather than growing without bound.
const eventBufferSize = 64

// CommandEvent is one command the learner ran, assembled from the OSC 133
// and OSC 7 marker stream that images/rc/instrument.bash emits around every
// prompt.
type CommandEvent struct {
	// Raw is the command line the learner typed. It is always empty today.
	//
	// TODO(v0.2): populate from the journal TSV once #51 threads a
	// runtime.Session (or an equivalent correlation step) into the event
	// pipeline. Mux has no Session reference today, only a runtime.PTY, and
	// the journal lives inside the sandbox filesystem.
	Raw string

	// ExitCode is the command's exit status, from the OSC 133;D marker.
	ExitCode int

	// Cwd is the working directory the shell reported right after the
	// command finished, from the OSC 7 marker.
	Cwd string

	// StartedAt is stamped from the OSC 133;C (PreExec) marker's own time.
	StartedAt time.Time

	// Duration is the time between PreExec and the OSC 133;D (CommandDone)
	// marker, both stamped by the OSC parser.
	Duration time.Duration
}

// fdHaver is satisfied by anything that can report its own OS file
// descriptor. New needs one to find the host terminal whose raw mode Run
// controls. *os.File satisfies it, which is what os.Stdin and os.Stdout
// both are.
type fdHaver interface {
	Fd() uintptr
}

// Mux is the host side PTY multiplexer. It forwards the host's stdin to a
// sandboxed shell's pseudo terminal unmodified, strips this project's OSC
// 133 and OSC 7 markers from the shell's output while forwarding every
// other byte unmodified, keeps the host terminal in raw mode sized to match
// the sandbox for the duration, and assembles the marker stream into
// CommandEvent values.
//
// A Mux is built for one session: construct it with New and call Run at
// most once.
type Mux struct {
	pty runtime.PTY
	in  io.Reader
	out io.Writer

	events  chan CommandEvent
	pending *CommandEvent

	fd    int
	fdErr error

	restoreOnce sync.Once

	// makeRaw, restore, getSize, and now follow the same injectable func
	// field pattern osc.go already uses for its own clock, so a test can
	// override host terminal behaviour without a fake terminal type. See
	// New for their production defaults. now is not read by anything in
	// this file today; it is here for the same reason Raw is documented as
	// always empty above, a future correlation step is expected to want a
	// controllable clock on this type too.
	makeRaw func(fd int) (*term.State, error)
	restore func(fd int, state *term.State) error
	getSize func(fd int) (cols, rows int, err error)
	now     func() time.Time

	// reraise re-delivers a caught terminating signal once this process's
	// own handling of it is done. See watchTerminatingSignals.
	reraise func(os.Signal)
}

// New returns a Mux that drives p: forwarding in to the sandbox, and
// forwarding p's output, with this project's OSC markers stripped, to out.
//
// New resolves the host terminal file descriptor immediately, preferring
// in and falling back to out, by checking each against fdHaver. If neither
// satisfies it, New does not fail: it records the problem, and Run returns
// it, wrapped, the moment Run is called, rather than New panicking before a
// caller has any chance to handle the error.
func New(p runtime.PTY, in io.Reader, out io.Writer) *Mux {
	m := &Mux{
		pty:     p,
		in:      in,
		out:     out,
		events:  make(chan CommandEvent, eventBufferSize),
		makeRaw: term.MakeRaw,
		restore: term.Restore,
		getSize: term.GetSize,
		now:     time.Now,
		reraise: defaultReraise,
	}

	if f, ok := in.(fdHaver); ok {
		m.fd = int(f.Fd())
		return m
	}
	if f, ok := out.(fdHaver); ok {
		m.fd = int(f.Fd())
		return m
	}
	m.fdErr = errors.New("neither in nor out exposes a host file descriptor (Fd() uintptr); raw mode needs one")
	return m
}

// waitOutcome carries the result of one call to runtime.PTY.Wait across a
// channel from the goroutine that makes that call, including a recovered
// panic value.
//
// Wait runs in its own goroutine so Run's select can watch it alongside
// ctx.Done and the two copy goroutines, but a panic raised in a goroutine
// other than the one whose defer calls recover is never caught by that
// recover. Capturing the panic here, then panicking again from inside Run's
// own goroutine when this outcome is received, is what lets Run's top level
// deferred restore-and-repanic catch it, so the terminal still gets
// restored before the panic reaches Run's caller.
type waitOutcome struct {
	err      error
	panicVal any
}

// Run drives the multiplexer until the sandboxed shell exits, ctx is
// cancelled, or a copy direction reports an unrecoverable error, whichever
// happens first. It puts the host terminal into raw mode for the duration
// and restores it on every exit path, including a panic raised anywhere
// within this call, or forwarded here from the goroutine waiting on
// p.Wait, before that panic, or Run's own return, reaches the caller.
//
// Run must be called at most once per Mux.
func (m *Mux) Run(ctx context.Context) error {
	if m.fdErr != nil {
		return fmt.Errorf("pty: resolve host terminal: %w", m.fdErr)
	}
	fd := m.fd

	oldState, err := m.makeRaw(fd)
	if err != nil {
		return fmt.Errorf("pty: put host terminal into raw mode: %w", err)
	}

	defer func() {
		m.restoreOnce.Do(func() { _ = m.restore(fd, oldState) })
		if r := recover(); r != nil {
			panic(r)
		}
	}()

	if cols, rows, err := m.getSize(fd); err == nil {
		_ = m.resize(uint16(rows), uint16(cols))
	}

	stopSig := m.watchTerminatingSignals(fd, oldState, &m.restoreOnce)
	defer stopSig()

	stopResize := startResizeWatcher(m)
	defer stopResize()

	oscParser := NewParser(m.out, m.onOSCEvent)

	var stdinErr, outErr error
	stdinDone := make(chan struct{})
	outDone := make(chan struct{})
	go func() {
		_, stdinErr = io.Copy(m.pty, m.in)
		close(stdinDone)
	}()
	go func() {
		_, outErr = io.Copy(oscParser, m.pty)
		close(outDone)
	}()

	waitDone := make(chan waitOutcome, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				waitDone <- waitOutcome{panicVal: r}
			}
		}()
		waitDone <- waitOutcome{err: m.pty.Wait()}
	}()

	// drain closes the sandbox PTY handle, which unblocks whatever is
	// blocked in a Read on it, then waits for that read side copy
	// goroutine to actually finish. Every branch below calls it before
	// Run returns or re-panics, so Run never leaves that goroutine still
	// touching m.pty. The stdin forwarding goroutine is deliberately not
	// waited for here: it blocks on a Read of the host's own stdin, which
	// nothing in this function can unblock without closing stdin itself,
	// and closing the host's stdin out from under a caller is not this
	// function's decision to make. It exits on its own once stdin reaches
	// EOF or the caller's process exits.
	drain := func() {
		_ = m.pty.Close()
		<-outDone
	}

	var runErr error
	select {
	case out := <-waitDone:
		drain()
		if out.panicVal != nil {
			panic(out.panicVal)
		}
		runErr = out.err
	case <-ctx.Done():
		drain()
		runErr = ctx.Err()
	case <-stdinDone:
		drain()
		if stdinErr != nil && !errors.Is(stdinErr, io.EOF) {
			runErr = fmt.Errorf("pty: copy stdin to sandbox: %w", stdinErr)
		}
	case <-outDone:
		drain()
		if outErr != nil && !errors.Is(outErr, io.EOF) {
			runErr = fmt.Errorf("pty: copy sandbox output to host: %w", outErr)
		}
	}

	// Nothing but the read side copy goroutine above ever touches
	// m.pending or calls onOSCEvent, and drain has already proven that
	// goroutine has stopped, so flushing whatever it left pending is safe
	// here with no lock.
	if m.pending != nil {
		m.emit(*m.pending)
		m.pending = nil
	}
	_ = oscParser.Flush()
	close(m.events)

	return runErr
}

// Events returns the channel of assembled CommandEvent values. It is
// closed after Run returns, once the read side copy goroutine has stopped,
// so a range over it, or a receive that checks ok, terminates cleanly at
// the end of the session.
func (m *Mux) Events() <-chan CommandEvent {
	return m.events
}

// resize sets the sandbox pseudo terminal's window size to rows by cols.
// rows comes first, matching runtime.PTY.Resize's own parameter order
// exactly, so there is no risk of a transposed argument bug between this
// method and the interface it calls: every caller, Run's initial call, the
// SIGWINCH watcher, and every test, uses this same rows-then-cols order.
//
// A resize failure is reported to the caller but is never treated as fatal
// by anything in this package: one failed resize is not worth ending the
// learner's shell over.
func (m *Mux) resize(rows, cols uint16) error {
	return m.pty.Resize(rows, cols)
}

// onOSCEvent is the OSC parser's onEvent callback for this Mux. The
// parser's own doc comment guarantees onEvent is called synchronously, and
// only from the read side copy goroutine's call into parser.Write, so
// nothing else touches m.pending and no additional lock is needed here.
func (m *Mux) onOSCEvent(ev Event) {
	switch ev.Kind {
	case PreExec:
		if m.pending != nil {
			// The previous command's CwdReport marker never arrived, most
			// likely dropped on the producing side. Flush what there is,
			// with whatever Cwd it has, which is likely empty, rather than
			// holding it forever and losing the next command's data too.
			m.emit(*m.pending)
		}
		m.pending = &CommandEvent{StartedAt: ev.At}

	case CommandDone:
		if m.pending == nil {
			// A CommandDone with no matching PreExec is a dropped marker on
			// the producing side. Do not synthesize a command that never
			// had a recorded start.
			return
		}
		m.pending.ExitCode = ev.ExitCode
		m.pending.Duration = ev.At.Sub(m.pending.StartedAt)

	case CwdReport:
		if m.pending == nil {
			return
		}
		m.pending.Cwd = ev.Cwd
		m.emit(*m.pending)
		m.pending = nil

	case PromptStart, CommandStart:
		// Not needed by Mux.
	}
}

// emit delivers ev on Events without blocking. A learner playing normally
// never fills the buffer; a consumer that has stopped draining Events is a
// bug elsewhere, not a reason to block the read side copy goroutine, and
// therefore the sandbox output stream, forever. Only the fact of a drop is
// logged, never ev itself: a learner types a password into a sudo prompt
// inside the sandbox, and nothing in this package logs raw PTY content at
// any level, ever, on the chance it might contain one.
func (m *Mux) emit(ev CommandEvent) {
	select {
	case m.events <- ev:
	default:
		log.Printf("pty: dropping command event, consumer not draining Events()")
	}
}

// watchTerminatingSignals arms a safety net against SIGINT and SIGTERM for
// the duration of one Run call and returns a function that disarms it.
//
// Raw mode disables the host terminal's line discipline, and that line
// discipline is what normally turns the learner's own Ctrl-C keystroke into
// a delivered SIGINT. In raw mode, Ctrl-C is just the byte 0x03: it travels
// through the stdin-forwarding goroutine to the sandbox unmodified, exactly
// like every other byte, and the sandboxed shell decides what it means, not
// this process. So a genuine SIGINT or SIGTERM reaching this process while
// it holds the terminal raw did not come from the learner typing at the
// sandbox. It came from somewhere else entirely: a parent process, job
// control, an operator running kill, or the host shutting down. This
// handler exists only so that an outside signal does not leave the
// learner's terminal stuck in raw mode; it restores, then lets the signal
// finish doing whatever it would have done without Mux in the way.
func (m *Mux) watchTerminatingSignals(fd int, oldState *term.State, restoreOnce *sync.Once) (stop func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		select {
		case sig := <-ch:
			m.handleTerminatingSignal(sig, fd, oldState, restoreOnce)
		case <-done:
		}
	}()
	return func() {
		signal.Stop(ch)
		close(done)
	}
}

// handleTerminatingSignal is watchTerminatingSignals' response to a caught
// signal, factored out on its own so a test can drive it directly without
// going through signal.Notify or sending this process a real signal.
func (m *Mux) handleTerminatingSignal(sig os.Signal, fd int, oldState *term.State, restoreOnce *sync.Once) {
	restoreOnce.Do(func() { _ = m.restore(fd, oldState) })
	m.reraise(sig)
}

// defaultReraise ends this process after the terminal has already been
// restored, so that an external SIGINT or SIGTERM, never the learner's own
// keystroke, see watchTerminatingSignals, still ends the game rather than
// being silently absorbed.
//
// A byte-for-byte re-raise, resetting the signal disposition and sending
// the same signal to this process's own pid, is only expressible on
// unix-like platforms; os.Exit(1) is the portable choice that is correct on
// every platform this project targets, at the cost of the exit not being
// attributed to the original signal number by whatever is watching this
// process. Every test overrides Mux.reraise with a recorder, so no test
// actually terminates the test binary.
func defaultReraise(os.Signal) {
	os.Exit(1)
}
