package pty

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/term"
)

// --- fakePTY: an in-memory runtime.PTY for white box tests. -----------------
//
// The read side is a real io.Pipe so a test controls exactly what bytes
// arrive and in how many Read calls, which is what the marker-split tests
// need. The write side is a plain byte slice: Mux only ever writes to it, it
// never reads back what it wrote.

type fakePTY struct {
	mu        sync.Mutex
	writes    []byte
	resizes   [][2]uint16
	resizeErr error
	closed    bool
	closeErr  error

	readBeforeResize bool

	pr *io.PipeReader
	pw *io.PipeWriter

	waitOnce  sync.Once
	waitCh    chan struct{}
	waitErr   error
	waitPanic any
}

func newFakePTY() *fakePTY {
	pr, pw := io.Pipe()
	return &fakePTY{pr: pr, pw: pw, waitCh: make(chan struct{})}
}

func (f *fakePTY) Read(p []byte) (int, error) {
	f.mu.Lock()
	if len(f.resizes) == 0 {
		f.readBeforeResize = true
	}
	f.mu.Unlock()
	return f.pr.Read(p)
}

func (f *fakePTY) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, errors.New("fakePTY: write after close")
	}
	f.writes = append(f.writes, p...)
	return len(p), nil
}

func (f *fakePTY) Close() error {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return nil
	}
	f.closed = true
	err := f.closeErr
	f.mu.Unlock()
	_ = f.pr.Close()
	// A real PTY hangup, the master side closing, usually ends the
	// foreground process too. Mimic that so Wait unblocks whenever Close
	// does, or the internal goroutine waiting on it would leak forever in
	// any test that only ever closes, never explicitly triggers, Wait.
	f.triggerWait(nil)
	return err
}

func (f *fakePTY) Resize(rows, cols uint16) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resizes = append(f.resizes, [2]uint16{rows, cols})
	return f.resizeErr
}

func (f *fakePTY) Wait() error {
	<-f.waitCh
	f.mu.Lock()
	p := f.waitPanic
	err := f.waitErr
	f.mu.Unlock()
	if p != nil {
		panic(p)
	}
	return err
}

// triggerWait makes a blocked or future Wait call return err. Safe to call
// more than once; only the first call's err sticks.
func (f *fakePTY) triggerWait(err error) {
	f.mu.Lock()
	f.waitErr = err
	f.mu.Unlock()
	f.waitOnce.Do(func() { close(f.waitCh) })
}

// triggerWaitPanic makes a blocked or future Wait call panic with v.
func (f *fakePTY) triggerWaitPanic(v any) {
	f.mu.Lock()
	f.waitPanic = v
	f.mu.Unlock()
	f.waitOnce.Do(func() { close(f.waitCh) })
}

func (f *fakePTY) setResizeErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resizeErr = err
}

func (f *fakePTY) writesSnapshot() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]byte, len(f.writes))
	copy(out, f.writes)
	return out
}

func (f *fakePTY) resizesSnapshot() [][2]uint16 {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][2]uint16, len(f.resizes))
	copy(out, f.resizes)
	return out
}

// feedOutput writes s to the read side, as if the sandboxed shell had
// produced it. It is a separate helper, rather than a bare pw.Write, only so
// every call site reads the same way.
func (f *fakePTY) feedOutput(t *testing.T, s string) {
	t.Helper()
	if _, err := f.pw.Write([]byte(s)); err != nil {
		t.Fatalf("feed fake PTY output: %v", err)
	}
}

// panicReader is an io.Reader whose Read always panics with v. Used to prove
// a panic inside the stdin copy goroutine is caught and re-raised from
// Run's own goroutine, with the terminal restored first, rather than
// crashing the process with the terminal still raw.
type panicReader struct{ v any }

func (p panicReader) Read([]byte) (int, error) { panic(p.v) }

// panicWriter is an io.Writer whose Write always panics with v. Same
// purpose as panicReader, for the output copy goroutine, which writes
// through the OSC parser into Mux's out.
type panicWriter struct{ v any }

func (p panicWriter) Write([]byte) (int, error) { panic(p.v) }

// --- safeBuffer: a mutex guarded byte buffer used as Mux's out. ------------

type safeBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

func (b *safeBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.buf)
}

// --- shared test wiring ------------------------------------------------

// newTestMux returns a Mux wired to a fakePTY, with the three injectable
// terminal func fields already overridden to safe no-op defaults good
// enough for most tests, and the fd resolution error New would otherwise
// produce, since neither the pipe reader nor the safeBuffer is an *os.File,
// cleared. A test that cares about a particular field overrides it again
// after this call.
func newTestMux(t *testing.T) (*fakePTY, *Mux, *io.PipeWriter, *safeBuffer) {
	t.Helper()

	p := newFakePTY()
	inR, inW := io.Pipe()
	out := &safeBuffer{}

	m := New(p, inR, out)
	m.fdErr = nil
	m.fd = 1
	m.makeRaw = func(int) (*term.State, error) { return &term.State{}, nil }
	m.restore = func(int, *term.State) error { return nil }
	m.getSize = func(int) (cols, rows int, err error) { return 80, 24, nil }

	t.Cleanup(func() {
		_ = inW.Close()
		_ = p.Close()
	})

	return p, m, inW, out
}

// runWithTimeout runs mux.Run(ctx) to completion and fails the test if it
// does not return within the timeout, rather than hanging the whole suite
// on a deadlocked implementation.
func runWithTimeout(t *testing.T, mux *Mux, ctx context.Context) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- mux.Run(ctx) }()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s")
		return nil
	}
}

// runAsync starts mux.Run(ctx) in the background and returns the channel it
// will report on, for a test that needs to keep driving the session while
// Run is still in progress.
func runAsync(mux *Mux, ctx context.Context) <-chan error {
	done := make(chan error, 1)
	go func() { done <- mux.Run(ctx) }()
	return done
}

// waitUntil polls cond, at a short fixed interval, until it reports true or
// timeout elapses, in which case it fails the test. Used only where the
// thing being waited for crosses a goroutine boundary this package does not
// otherwise expose a synchronization point for.
func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("condition not met within %s", timeout)
		}
		time.Sleep(time.Millisecond)
	}
}

// --- OSC wire fixtures, matching osc_test.go's own literal style. ----------

func preExecSeq() string { return "\x1b]133;C\x07" }

func commandDoneSeq(exit int) string { return fmt.Sprintf("\x1b]133;D;%d\x07", exit) }

func cwdReportSeq(path string) string { return "\x1b]7;file://quest" + path + "\x07" }

// --- 1: terminal restore across every exit path -----------------------

type restoreRecorder struct {
	mu         sync.Mutex
	makeRawN   int
	restoreN   int
	restoreArg *term.State
}

// wireRecorder overrides makeRaw and restore on mux to count calls into rec
// and returns the sentinel *term.State makeRaw will report, so a test can
// assert restore was handed back the exact same pointer.
func wireRecorder(mux *Mux, rec *restoreRecorder) *term.State {
	state := &term.State{}
	mux.makeRaw = func(int) (*term.State, error) {
		rec.mu.Lock()
		rec.makeRawN++
		rec.mu.Unlock()
		return state, nil
	}
	mux.restore = func(_ int, s *term.State) error {
		rec.mu.Lock()
		rec.restoreN++
		rec.restoreArg = s
		rec.mu.Unlock()
		return nil
	}
	return state
}

func TestRun_RestoresTerminal(t *testing.T) {
	t.Run("clean_shell_exit", func(t *testing.T) {
		p, mux, _, _ := newTestMux(t)
		rec := &restoreRecorder{}
		state := wireRecorder(mux, rec)

		p.triggerWait(nil)
		if err := runWithTimeout(t, mux, context.Background()); err != nil {
			t.Errorf("Run() = %v, want nil", err)
		}

		rec.mu.Lock()
		defer rec.mu.Unlock()
		if rec.makeRawN != 1 {
			t.Errorf("makeRaw called %d times, want 1", rec.makeRawN)
		}
		if rec.restoreN != 1 {
			t.Errorf("restore called %d times, want 1", rec.restoreN)
		}
		if rec.restoreArg != state {
			t.Errorf("restore called with %p, want the state makeRaw returned (%p)", rec.restoreArg, state)
		}
	})

	t.Run("error_from_copy_goroutine", func(t *testing.T) {
		p, mux, _, _ := newTestMux(t)
		rec := &restoreRecorder{}
		state := wireRecorder(mux, rec)

		boom := errors.New("boom")
		_ = p.pw.CloseWithError(boom)

		err := runWithTimeout(t, mux, context.Background())
		if err == nil {
			t.Error("Run() = nil, want an error from the failed copy")
		}

		rec.mu.Lock()
		defer rec.mu.Unlock()
		if rec.makeRawN != 1 {
			t.Errorf("makeRaw called %d times, want 1", rec.makeRawN)
		}
		if rec.restoreN != 1 {
			t.Errorf("restore called %d times, want 1", rec.restoreN)
		}
		if rec.restoreArg != state {
			t.Errorf("restore called with %p, want the state makeRaw returned (%p)", rec.restoreArg, state)
		}
	})

	t.Run("context_cancelled", func(t *testing.T) {
		p, mux, _, _ := newTestMux(t)
		rec := &restoreRecorder{}
		state := wireRecorder(mux, rec)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := runWithTimeout(t, mux, ctx)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run() = %v, want context.Canceled", err)
		}
		_ = p

		rec.mu.Lock()
		defer rec.mu.Unlock()
		if rec.makeRawN != 1 {
			t.Errorf("makeRaw called %d times, want 1", rec.makeRawN)
		}
		if rec.restoreN != 1 {
			t.Errorf("restore called %d times, want 1", rec.restoreN)
		}
		if rec.restoreArg != state {
			t.Errorf("restore called with %p, want the state makeRaw returned (%p)", rec.restoreArg, state)
		}
	})

	t.Run("panic_inside_run", func(t *testing.T) {
		p, mux, _, _ := newTestMux(t)
		rec := &restoreRecorder{}
		state := wireRecorder(mux, rec)

		p.triggerWaitPanic("boom")

		var gotPanic any
		func() {
			defer func() { gotPanic = recover() }()
			_ = mux.Run(context.Background())
		}()

		if gotPanic != "boom" {
			t.Errorf("panic value = %v, want %q", gotPanic, "boom")
		}

		rec.mu.Lock()
		defer rec.mu.Unlock()
		if rec.makeRawN != 1 {
			t.Errorf("makeRaw called %d times, want 1", rec.makeRawN)
		}
		if rec.restoreN != 1 {
			t.Errorf("restore called %d times, want 1", rec.restoreN)
		}
		if rec.restoreArg != state {
			t.Errorf("restore called with %p, want the state makeRaw returned (%p)", rec.restoreArg, state)
		}
	})

	t.Run("panic_in_stdin_copy_goroutine", func(t *testing.T) {
		p, mux, _, _ := newTestMux(t)
		rec := &restoreRecorder{}
		state := wireRecorder(mux, rec)
		mux.in = panicReader{v: "stdin boom"}
		_ = p

		gotPanic := runAndCatchPanic(t, mux, context.Background())
		if gotPanic != "stdin boom" {
			t.Errorf("panic value = %v, want %q", gotPanic, "stdin boom")
		}

		rec.mu.Lock()
		defer rec.mu.Unlock()
		if rec.restoreN != 1 {
			t.Errorf("restore called %d times, want 1", rec.restoreN)
		}
		if rec.restoreArg != state {
			t.Errorf("restore called with %p, want the state makeRaw returned (%p)", rec.restoreArg, state)
		}
	})

	t.Run("panic_in_output_copy_goroutine", func(t *testing.T) {
		p, mux, _, _ := newTestMux(t)
		rec := &restoreRecorder{}
		state := wireRecorder(mux, rec)
		mux.out = panicWriter{v: "output boom"}

		panicCh := make(chan any, 1)
		go func() {
			defer func() { panicCh <- recover() }()
			_ = mux.Run(context.Background())
		}()

		// feedOutput blocks on the pipe's matching Read, which only happens
		// once Run's setup has started the output copy goroutine, so this
		// needs no separate synchronization of its own.
		p.feedOutput(t, "hello")

		var gotPanic any
		select {
		case gotPanic = <-panicCh:
		case <-time.After(5 * time.Second):
			t.Fatal("Run did not panic within 5s")
		}
		if gotPanic != "output boom" {
			t.Errorf("panic value = %v, want %q", gotPanic, "output boom")
		}

		rec.mu.Lock()
		defer rec.mu.Unlock()
		if rec.restoreN != 1 {
			t.Errorf("restore called %d times, want 1", rec.restoreN)
		}
		if rec.restoreArg != state {
			t.Errorf("restore called with %p, want the state makeRaw returned (%p)", rec.restoreArg, state)
		}
	})
}

// runAndCatchPanic runs mux.Run(ctx) and returns the value of any panic
// that escapes it, or nil if Run returned normally. Used by the panic
// coverage subtests above where the panicking goroutine is not the test's
// own, so time.After is needed to fail cleanly rather than hang forever if
// the panic is somehow lost.
func runAndCatchPanic(t *testing.T, mux *Mux, ctx context.Context) any {
	t.Helper()
	panicCh := make(chan any, 1)
	go func() {
		defer func() { panicCh <- recover() }()
		_ = mux.Run(ctx)
	}()
	select {
	case v := <-panicCh:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not panic within 5s")
		return nil
	}
}

// --- 2: the terminating signal safety net, exercised via the seam -------
//
// A real SIGINT or SIGTERM is not raised here: it is unsafe to raise
// against the test process itself, and syscall.SIGTERM has nothing to
// raise on Windows. Writing directly to mux.signalled, the same channel
// watchTerminatingSignals forwards a caught OS signal onto, proves Run's
// own response is correct without depending on OS signal delivery, which
// is confirmed correct by inspection of watchTerminatingSignals' few lines
// of registration code instead.
func TestMux_HandlesTerminatingSignal(t *testing.T) {
	tests := []struct {
		name string
		sig  os.Signal
	}{
		{"SIGINT", syscall.SIGINT},
		{"SIGTERM", syscall.SIGTERM},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, mux, _, _ := newTestMux(t)
			rec := &restoreRecorder{}
			state := wireRecorder(mux, rec)
			_ = p

			done := runAsync(mux, context.Background())
			mux.signalled <- tt.sig

			err := <-done
			if !errors.Is(err, ErrSignalled) {
				t.Errorf("Run() = %v, want an error wrapping ErrSignalled", err)
			}

			rec.mu.Lock()
			defer rec.mu.Unlock()
			if rec.restoreN != 1 {
				t.Errorf("restore called %d times, want 1", rec.restoreN)
			}
			if rec.restoreArg != state {
				t.Errorf("restore called with %p, want the state makeRaw returned (%p)", rec.restoreArg, state)
			}
		})
	}
}

// --- 2b: New's host fd resolution -----------------------------------------

// neitherFdNorTerminal is a plain io.ReadWriter that satisfies neither
// fdHaver nor anything term.IsTerminal would recognize, used to drive New
// down its failure path.
type neitherFdNorTerminal struct{}

func (neitherFdNorTerminal) Read([]byte) (int, error)    { return 0, io.EOF }
func (neitherFdNorTerminal) Write(p []byte) (int, error) { return len(p), nil }

func TestNew_NeitherInNorOutExposesFd(t *testing.T) {
	p := newFakePTY()
	t.Cleanup(func() { _ = p.Close() })

	mux := New(p, neitherFdNorTerminal{}, neitherFdNorTerminal{})

	err := mux.Run(context.Background())
	if err == nil {
		t.Fatal("Run() = nil, want an error, since neither in nor out exposes a host fd")
	}
	if !strings.Contains(err.Error(), "resolve host terminal") {
		t.Errorf("Run() = %q, want it to mention resolving the host terminal", err.Error())
	}
}

// --- 3: the initial resize, ordered ahead of any forwarded output -------

func TestMux_ResizeAppliedOnInitialRun(t *testing.T) {
	p, mux, _, out := newTestMux(t)
	mux.getSize = func(int) (cols, rows int, err error) { return 100, 30, nil }

	done := runAsync(mux, context.Background())

	// Give Run's synchronous setup, which happens before either copy
	// goroutine starts, a chance to run before any output is produced.
	// The real ordering guarantee comes from the implementation calling
	// resize before starting the output copy goroutine at all, checked
	// below via readBeforeResize; this is only to avoid feeding output
	// before Run has even been scheduled once.
	waitUntil(t, 2*time.Second, func() bool {
		return len(p.resizesSnapshot()) > 0
	})

	p.feedOutput(t, "hello")
	waitUntil(t, 2*time.Second, func() bool { return out.Len() > 0 })

	p.mu.Lock()
	sawReadBeforeResize := p.readBeforeResize
	p.mu.Unlock()
	if sawReadBeforeResize {
		t.Error("fake PTY's Read was called before the initial resize, want resize first")
	}

	resizes := p.resizesSnapshot()
	if len(resizes) == 0 || resizes[0] != [2]uint16{30, 100} {
		t.Errorf("first resize = %v, want [rows=30 cols=100]", resizes)
	}

	p.triggerWait(nil)
	if err := <-done; err != nil {
		t.Errorf("Run() = %v, want nil", err)
	}
}

// --- 4: resize argument order --------------------------------------------

func TestMux_Resize(t *testing.T) {
	p, mux, _, _ := newTestMux(t)

	if err := mux.resize(52, 211); err != nil {
		t.Fatalf("resize() = %v, want nil", err)
	}

	resizes := p.resizesSnapshot()
	if len(resizes) != 1 || resizes[0] != [2]uint16{52, 211} {
		t.Errorf("fake PTY recorded %v, want exactly [rows=52 cols=211]", resizes)
	}
}

// --- 5: a resize failure is reported but never fatal ---------------------

func TestMux_Resize_PropagatesButIsNonFatal(t *testing.T) {
	p, mux, _, _ := newTestMux(t)

	boom := errors.New("boom")
	p.setResizeErr(boom)

	if err := mux.resize(10, 20); !errors.Is(err, boom) {
		t.Errorf("resize() = %v, want %v", err, boom)
	}

	done := runAsync(mux, context.Background())
	p.triggerWait(nil)

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run() = %v after a non-fatal resize failure, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after clean shell exit")
	}
}

// --- 6 and 7: stdin is forwarded byte for byte, Ctrl-C included ---------

func TestMux_StdinForwardedVerbatim(t *testing.T) {
	p, mux, inW, _ := newTestMux(t)

	done := runAsync(mux, context.Background())
	t.Cleanup(func() {
		p.triggerWait(nil)
		<-done
	})

	payload := []byte{'a', 0x1b, 0x03, '[', 'X', 0x1b, '[', '2', 'J', 'z'}
	if _, err := inW.Write(payload); err != nil {
		t.Fatalf("write to stdin: %v", err)
	}

	waitUntil(t, 2*time.Second, func() bool { return len(p.writesSnapshot()) >= len(payload) })

	got := p.writesSnapshot()
	if string(got) != string(payload) {
		t.Errorf("fake PTY write side = %q, want %q unmodified", got, payload)
	}
}

func TestMux_CtrlCForwardedNotIntercepted(t *testing.T) {
	p, mux, inW, _ := newTestMux(t)

	done := runAsync(mux, context.Background())
	t.Cleanup(func() {
		p.triggerWait(nil)
		<-done
	})

	if _, err := inW.Write([]byte{0x03}); err != nil {
		t.Fatalf("write ctrl-c to stdin: %v", err)
	}

	waitUntil(t, 2*time.Second, func() bool { return len(p.writesSnapshot()) >= 1 })

	got := p.writesSnapshot()
	if len(got) != 1 || got[0] != 0x03 {
		t.Errorf("fake PTY write side = %v, want [0x03]", got)
	}

	select {
	case err := <-done:
		t.Fatalf("Run returned (%v) after Ctrl-C alone; it must not treat 0x03 as a reason to stop", err)
	default:
	}
}

// --- 8: markers are stripped, everything else passes through untouched -

func TestMux_Passthrough_NoMarkerBytesOnStdout(t *testing.T) {
	p, mux, _, out := newTestMux(t)

	done := runAsync(mux, context.Background())
	t.Cleanup(func() {
		p.triggerWait(nil)
		<-done
	})

	// The OSC 7 marker is split mid-payload, at a byte offset that lands
	// inside the path text rather than on any structural boundary, across
	// two separate feedOutput calls, which is two separate Read returns on
	// the fake PTY's read side.
	full := cwdReportSeq("/tmp")
	splitAt := len("\x1b]7;file://ques")
	first, second := full[:splitAt], full[splitAt:]

	p.feedOutput(t, "hello "+preExecSeq()+"world "+first)
	p.feedOutput(t, second+" done")

	wantPlain := "hello world  done"
	waitUntil(t, 2*time.Second, func() bool { return out.Len() >= len(wantPlain) })

	if got := out.String(); got != wantPlain {
		t.Errorf("stdout = %q, want %q (no marker bytes)", got, wantPlain)
	}
}

// --- 9 through 12: event assembly ----------------------------------------

func recvEvent(t *testing.T, mux *Mux) CommandEvent {
	t.Helper()
	select {
	case ev := <-mux.Events():
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a CommandEvent")
		return CommandEvent{}
	}
}

func TestMux_EventAssembly_SingleCommand(t *testing.T) {
	p, mux, _, _ := newTestMux(t)
	done := runAsync(mux, context.Background())
	t.Cleanup(func() {
		p.triggerWait(nil)
		<-done
	})

	p.feedOutput(t, preExecSeq())
	// A short, deliberate gap between PreExec and CommandDone: both are
	// timestamped by the OSC parser's own real clock, and on a coarse
	// system clock two calls to time.Now back to back can otherwise land
	// on the same tick, making Duration flakily zero rather than
	// dependably positive.
	time.Sleep(5 * time.Millisecond)
	p.feedOutput(t, commandDoneSeq(0))
	p.feedOutput(t, cwdReportSeq("/home/learner"))

	ev := recvEvent(t, mux)
	if ev.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", ev.ExitCode)
	}
	if ev.Cwd != "/home/learner" {
		t.Errorf("Cwd = %q, want /home/learner", ev.Cwd)
	}
	if ev.Duration <= 0 {
		t.Errorf("Duration = %v, want > 0", ev.Duration)
	}
	if ev.Raw != "" {
		t.Errorf("Raw = %q, want empty", ev.Raw)
	}
}

func TestMux_EventAssembly_NonZeroExit(t *testing.T) {
	p, mux, _, _ := newTestMux(t)
	done := runAsync(mux, context.Background())
	t.Cleanup(func() {
		p.triggerWait(nil)
		<-done
	})

	p.feedOutput(t, preExecSeq())
	p.feedOutput(t, commandDoneSeq(17))
	p.feedOutput(t, cwdReportSeq("/home/learner"))

	ev := recvEvent(t, mux)
	if ev.ExitCode != 17 {
		t.Errorf("ExitCode = %d, want 17", ev.ExitCode)
	}
}

func TestMux_EventAssembly_CwdChangeAcrossCommands(t *testing.T) {
	p, mux, _, _ := newTestMux(t)
	done := runAsync(mux, context.Background())
	t.Cleanup(func() {
		p.triggerWait(nil)
		<-done
	})

	p.feedOutput(t, preExecSeq())
	p.feedOutput(t, commandDoneSeq(0))
	p.feedOutput(t, cwdReportSeq("/home/learner"))
	ev1 := recvEvent(t, mux)

	p.feedOutput(t, preExecSeq())
	p.feedOutput(t, commandDoneSeq(0))
	p.feedOutput(t, cwdReportSeq("/tmp"))
	ev2 := recvEvent(t, mux)

	if ev1.Cwd != "/home/learner" {
		t.Errorf("event one Cwd = %q, want /home/learner", ev1.Cwd)
	}
	if ev2.Cwd != "/tmp" {
		t.Errorf("event two Cwd = %q, want /tmp", ev2.Cwd)
	}
}

func TestMux_EventAssembly_MissingCwdReport(t *testing.T) {
	p, mux, _, _ := newTestMux(t)
	done := runAsync(mux, context.Background())
	t.Cleanup(func() {
		p.triggerWait(nil)
		<-done
	})

	// First command: PreExec, CommandDone, then immediately a second
	// PreExec with no CwdReport in between. The stale pending event must
	// still be flushed, with an empty Cwd, rather than held forever.
	p.feedOutput(t, preExecSeq())
	p.feedOutput(t, commandDoneSeq(5))
	p.feedOutput(t, preExecSeq())

	ev1 := recvEvent(t, mux)
	if ev1.ExitCode != 5 {
		t.Errorf("event one ExitCode = %d, want 5", ev1.ExitCode)
	}
	if ev1.Cwd != "" {
		t.Errorf("event one Cwd = %q, want empty", ev1.Cwd)
	}

	// The second command, already under way from the PreExec above,
	// assembles correctly and independently of the first.
	p.feedOutput(t, commandDoneSeq(9))
	p.feedOutput(t, cwdReportSeq("/var"))

	ev2 := recvEvent(t, mux)
	if ev2.ExitCode != 9 {
		t.Errorf("event two ExitCode = %d, want 9", ev2.ExitCode)
	}
	if ev2.Cwd != "/var" {
		t.Errorf("event two Cwd = %q, want /var", ev2.Cwd)
	}
}

// --- 13: Events is closed once Run returns --------------------------------

func TestMux_EventsClosedWhenRunReturns(t *testing.T) {
	p, mux, _, _ := newTestMux(t)

	p.triggerWait(nil)
	if err := runWithTimeout(t, mux, context.Background()); err != nil {
		t.Fatalf("Run() = %v, want nil", err)
	}

	select {
	case _, ok := <-mux.Events():
		if ok {
			t.Error("Events() delivered a value after Run returned, want the channel closed and empty")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Events() did not report closed within 2s")
	}
}

// --- 14: a stuck consumer never blocks the PTY copy goroutine -----------

func TestMux_DropsEventWhenConsumerNotDraining(t *testing.T) {
	p, mux, _, _ := newTestMux(t)
	done := runAsync(mux, context.Background())
	t.Cleanup(func() {
		p.triggerWait(nil)
		<-done
	})

	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		// Fill the buffer to capacity, then push one more, all without
		// anyone draining Events().
		for i := 0; i < eventBufferSize+1; i++ {
			p.feedOutput(t, preExecSeq())
			p.feedOutput(t, commandDoneSeq(0))
			p.feedOutput(t, cwdReportSeq("/home/learner"))
		}
		// One more round trip through the pipe, carrying a harmless plain
		// byte with no marker significance, is what actually proves the
		// last real write above has been fully processed, not just read.
		// The fake PTY's read side is an io.Pipe, and a Write on it only
		// blocks until the matching Read returns; it says nothing about
		// whether the copy goroutine has gone on to call onOSCEvent for
		// that chunk yet. That goroutine is a single sequential loop of
		// Read then Write, so its next Read, the one this final write
		// satisfies, cannot start until the previous iteration's Write,
		// which is what runs every onOSCEvent call for the cycle just
		// fed, has returned. Without this, draining below can race the
		// last cycle's own CwdReport event still being assembled, freeing
		// a slot just in time for it to land instead of drop, and turning
		// an exact capacity assertion flaky.
		p.feedOutput(t, "x")
	}()

	select {
	case <-writeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("feeding events deadlocked; emit must never block the PTY copy goroutine")
	}

	// Drain what did make it through: exactly the buffer's capacity, the
	// extra one having gone through the drop path instead.
	got := 0
drain:
	for {
		select {
		case <-mux.Events():
			got++
		case <-time.After(200 * time.Millisecond):
			break drain
		}
	}
	if got != eventBufferSize {
		t.Errorf("received %d events, want exactly %d (the buffer's capacity)", got, eventBufferSize)
	}
}

// --- 15: tab tapping, and the forwarding it must not disturb --------------

// feedCompletedCommand feeds the three markers that close out one command
// and returns the CommandEvent Mux assembles from them.
func feedCompletedCommand(t *testing.T, p *fakePTY, mux *Mux, exit int, cwd string) CommandEvent {
	t.Helper()
	p.feedOutput(t, preExecSeq())
	p.feedOutput(t, commandDoneSeq(exit))
	p.feedOutput(t, cwdReportSeq(cwd))
	return recvEvent(t, mux)
}

// typeStdin writes payload to the host stdin side and waits until every byte
// of it has reached the fake PTY's write side. That wait is the
// synchronization point the tab flag needs: the stdin copy goroutine reads a
// chunk (observing its bytes) before it writes that same chunk on, so a byte
// visible on the write side has already been through the tap.
func typeStdin(t *testing.T, p *fakePTY, inW *io.PipeWriter, payload []byte, before int) {
	t.Helper()
	if _, err := inW.Write(payload); err != nil {
		t.Fatalf("write to stdin: %v", err)
	}
	waitUntil(t, 2*time.Second, func() bool { return len(p.writesSnapshot()) >= before+len(payload) })
}

func TestMux_TabTap_UsedTabReflectsTheRecordedStdinStream(t *testing.T) {
	p, mux, inW, _ := newTestMux(t)
	done := runAsync(mux, context.Background())
	t.Cleanup(func() {
		p.triggerWait(nil)
		<-done
	})

	// A recorded stdin byte stream, one entry per command the learner
	// submits: what they typed, and whether a Tab (0x09) is in it.
	stream := []struct {
		name    string
		payload []byte
		wantTab bool
	}{
		{"completed with tab", []byte("cd Doc\tuments\r"), true},
		{"typed out in full", []byte("ls -la\r"), false},
		{"ctrl-c then a plain command", []byte{0x03, 'p', 'w', 'd', '\r'}, false},
		{"tab again after a plain command", []byte("cat rep\t\r"), true},
	}

	var typed []byte
	for _, s := range stream {
		typeStdin(t, p, inW, s.payload, len(typed))
		typed = append(typed, s.payload...)

		ev := feedCompletedCommand(t, p, mux, 0, "/home/learner")
		if ev.UsedTab != s.wantTab {
			t.Errorf("%s: UsedTab = %v, want %v", s.name, ev.UsedTab, s.wantTab)
		}
		if ev.Raw != "" {
			t.Errorf("%s: Raw = %q, want empty: Mux stays a byte pump and never fills Raw", s.name, ev.Raw)
		}
	}

	// The whole recorded stream must have reached the sandbox unchanged:
	// nothing added, nothing dropped, nothing reordered, Ctrl-C included.
	if got := p.writesSnapshot(); string(got) != string(typed) {
		t.Errorf("fake PTY write side = %q, want %q byte for byte", got, typed)
	}
}

func TestMux_TabTap_FlagIsClearedForTheNextCommand(t *testing.T) {
	p, mux, inW, _ := newTestMux(t)
	done := runAsync(mux, context.Background())
	t.Cleanup(func() {
		p.triggerWait(nil)
		<-done
	})

	typeStdin(t, p, inW, []byte("ec\tho hi\r"), 0)
	if ev := feedCompletedCommand(t, p, mux, 0, "/home/learner"); !ev.UsedTab {
		t.Fatal("first command UsedTab = false, want true")
	}

	// No keystroke at all before the second command's markers: a tab from
	// the previous window must not carry over.
	if ev := feedCompletedCommand(t, p, mux, 0, "/home/learner"); ev.UsedTab {
		t.Error("second command UsedTab = true, want false: the flag must be cleared per command")
	}
}

func TestMux_TabTap_TabInsideAFullScreenApplicationIsStillForwardedVerbatim(t *testing.T) {
	p, mux, inW, _ := newTestMux(t)
	done := runAsync(mux, context.Background())
	t.Cleanup(func() {
		p.triggerWait(nil)
		<-done
	})

	// A vim-shaped burst: escape sequences, a literal tab being inserted
	// into a file, and a Ctrl-C, in one write.
	payload := []byte{0x1b, '[', 'A', 'i', 0x09, 'x', 0x09, 0x1b, 0x03, ':', 'w', 'q', '\r'}
	typeStdin(t, p, inW, payload, 0)

	if got := p.writesSnapshot(); string(got) != string(payload) {
		t.Errorf("fake PTY write side = %v, want %v byte for byte", got, payload)
	}
}
