package pty

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
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

	// makeRaw, restore, and getSize follow the same injectable func field
	// pattern osc.go already uses for its own clock, so a test can override
	// host terminal behaviour without a fake terminal type. See New for
	// their production defaults.
	makeRaw func(fd int) (*term.State, error)
	restore func(fd int, state *term.State) error
	getSize func(fd int) (cols, rows int, err error)

	// signalled carries a caught external terminating signal from the
	// watcher goroutine to Run's own select, so Run unwinds through its
	// ordinary exit path rather than the process dying underneath it. See
	// watchTerminatingSignals and ErrSignalled.
	signalled chan os.Signal
}

// ErrSignalled reports that Run stopped because this process received an
// external terminating signal, rather than because the sandboxed shell
// exited on its own.
//
// Run restores the host terminal, closes the sandbox PTY handle, waits for
// its own goroutines, and closes Events before returning an error wrapping
// this, so a caller can close the runtime.Session it owns and then decide
// how to end the process. Mux deliberately does not call os.Exit itself: at
// L2 it does not own the process lifetime, and exiting from in here would
// strand whatever container or distribution the caller opened. Callers that
// want the conventional signal exit status should re-raise after their own
// cleanup has run.
var ErrSignalled = errors.New("terminated by signal")

// New returns a Mux that drives p: forwarding in to the sandbox, and
// forwarding p's output, with this project's OSC markers stripped, to out.
//
// New resolves the host terminal file descriptor immediately. It prefers
// whichever of in or out is an fdHaver AND a real terminal, checked with
// term.IsTerminal, so a session with a redirected, non-terminal in (a
// script piping input into an otherwise interactive shell) still resolves
// out's real terminal rather than failing outright over the one side that
// happens not to be a tty. If neither is a real terminal, it falls back to
// whichever is at least an fdHaver, preferring in, matching this package's
// original, simpler rule. If neither satisfies fdHaver at all, New does
// not fail: it records the problem, and Run returns it, wrapped, the
// moment Run is called, rather than New panicking before a caller has any
// chance to handle the error.
func New(p runtime.PTY, in io.Reader, out io.Writer) *Mux {
	m := &Mux{
		pty:       p,
		in:        in,
		out:       out,
		events:    make(chan CommandEvent, eventBufferSize),
		makeRaw:   term.MakeRaw,
		restore:   term.Restore,
		getSize:   term.GetSize,
		signalled: make(chan os.Signal, 1),
	}

	inFd, inHasFd := fdOf(in)
	outFd, outHasFd := fdOf(out)

	switch {
	case inHasFd && term.IsTerminal(inFd):
		m.fd = inFd
	case outHasFd && term.IsTerminal(outFd):
		m.fd = outFd
	case inHasFd:
		m.fd = inFd
	case outHasFd:
		m.fd = outFd
	default:
		m.fdErr = errors.New("neither in nor out exposes a host file descriptor (Fd() uintptr); raw mode needs one")
	}
	return m
}

// fdOf reports v's OS file descriptor and whether it has one at all, by
// checking it against fdHaver. *os.File satisfies fdHaver, which is what
// os.Stdin and os.Stdout both are.
func fdOf(v any) (fd int, ok bool) {
	f, ok := v.(fdHaver)
	if !ok {
		return 0, false
	}
	return int(f.Fd()), true
}

// outcome carries the result of one goroutine Run started, including a
// recovered panic value, back across a channel to Run's own select.
//
// A panic raised in a goroutine other than the one whose deferred call
// invokes recover is never caught by that recover: Go terminates the whole
// process on an unrecovered panic in any goroutine, main or not. runRecovered
// is what stands between Run's three worker goroutines, the stdin copy, the
// output copy, and the wait on the sandboxed process, and that fate. Each
// captures its own panic here and sends it back as data; Run's own select
// then panics again from its own goroutine, which is what lets Run's top
// level defer restore the host terminal before the panic reaches Run's
// caller. Without this, a panic in either copy goroutine would kill the
// process with the terminal still in raw mode, unrecoverable from outside.
type outcome struct {
	err      error
	panicVal any
}

// runRecovered runs fn on its own goroutine and reports its result, or a
// panic recovered from it, on the returned channel, then closes it.
//
// Closing after the one send matters: Run's select only ever consumes the
// branch that actually fired, and drain, below, deliberately reads from
// outDone a second time on every OTHER branch to wait for the output copy
// goroutine to finish. A plain unclosed buffered channel would make that
// second read block forever once its single value is gone; reading from a
// closed channel with nothing buffered returns immediately instead, which
// is all drain needs, since it only cares that the goroutine is done, not
// what it returned.
func runRecovered(fn func() error) <-chan outcome {
	done := make(chan outcome, 1)
	go func() {
		out := outcome{}
		defer func() {
			done <- out
			close(done)
		}()
		defer func() {
			if r := recover(); r != nil {
				out = outcome{panicVal: r}
			}
		}()
		out.err = fn()
	}()
	return done
}

// Run drives the multiplexer until the sandboxed shell exits, ctx is
// cancelled, an external SIGINT or SIGTERM arrives, or a copy direction
// reports an unrecoverable error, whichever happens first. It puts the host
// terminal into raw mode for the duration and restores it on every exit
// path, including a panic raised in Run itself or in any of its three
// worker goroutines (stdin copy, output copy, or the wait on the sandboxed
// process), forwarded here via runRecovered before that panic, or Run's own
// return, reaches the caller.
//
// A caught SIGINT or SIGTERM does not exit the process: Run restores the
// terminal, drains its goroutines, closes Events, and returns an error
// wrapping ErrSignalled, so a caller can close whatever runtime.Session or
// container it owns before deciding how the process itself ends.
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
		_ = m.resize(clampToUint16(rows), clampToUint16(cols))
	}

	stopSig := m.watchTerminatingSignals()
	defer stopSig()

	stopResize := startResizeWatcher(m)
	defer stopResize()

	oscParser := NewParser(m.out, m.onOSCEvent)

	stdinDone := runRecovered(func() error {
		_, err := io.Copy(m.pty, m.in)
		return err
	})
	outDone := runRecovered(func() error {
		_, err := io.Copy(oscParser, m.pty)
		return err
	})
	waitDone := runRecovered(m.pty.Wait)

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

	// repanic re-raises a panic recovered on a goroutine other than this
	// one. recover only ever catches a panic on the same goroutine as the
	// deferred call to it, so the stdin copy, output copy, and Wait
	// goroutines each recover their own panic and forward the value here;
	// panicking again from Run's own goroutine is what lets Run's top
	// level defer restore the terminal before the panic reaches Run's
	// caller.
	repanic := func(out outcome) {
		if out.panicVal != nil {
			panic(out.panicVal)
		}
	}

	var runErr error
	select {
	case out := <-waitDone:
		drain()
		repanic(out)
		runErr = out.err
	case out := <-stdinDone:
		drain()
		repanic(out)
		if out.err != nil && !errors.Is(out.err, io.EOF) {
			runErr = fmt.Errorf("pty: copy stdin to sandbox: %w", out.err)
		}
	case out := <-outDone:
		drain()
		repanic(out)
		if out.err != nil && !errors.Is(out.err, io.EOF) {
			runErr = fmt.Errorf("pty: copy sandbox output to host: %w", out.err)
		}
	case sig := <-m.signalled:
		drain()
		runErr = fmt.Errorf("pty: %w: %v", ErrSignalled, sig)
	case <-ctx.Done():
		drain()
		runErr = ctx.Err()
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

// clampToUint16 bounds n to the range of a uint16.
//
// term.GetSize returns cols and rows as int; runtime.PTY.Resize takes
// uint16. A terminal size from an OS syscall is never remotely close to
// either bound in practice, but a bare uint16(n) conversion is exactly the
// kind of silent wraparound gosec's G115 exists to catch, so this makes the
// bound explicit rather than suppressing the finding.
func clampToUint16(n int) uint16 {
	if n < 0 {
		return 0
	}
	if n > math.MaxUint16 {
		return math.MaxUint16
	}
	return uint16(n)
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
		// A bare \n does not carriage-return on a host terminal that Run
		// has put into raw mode, so log.Default's default writer would
		// render this as a staircase-corrupted line on the learner's own
		// screen if the buffer ever actually fills. \r\n avoids that.
		log.Print("pty: dropping command event, consumer not draining Events()\r\n")
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
// control, an operator running kill, or the host shutting down.
//
// This watcher does not restore the terminal or exit the process itself. It
// only forwards the caught signal to m.signalled, which Run's own select is
// already watching alongside ctx.Done and the two copy goroutines, so the
// signal unwinds through Run's ordinary exit path: restore, drain, close
// Events, return ErrSignalled. A handler that called os.Exit directly from
// here would skip all of that and strand whatever container or session the
// caller opened; Mux is L2 and does not own the process lifetime.
func (m *Mux) watchTerminatingSignals() (stop func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		select {
		case sig := <-ch:
			select {
			case m.signalled <- sig:
			default:
			}
		case <-done:
		}
	}()
	return func() {
		signal.Stop(ch)
		close(done)
	}
}
