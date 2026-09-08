package game

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/game/bus"
	"github.com/JoottunAtish/ShellForge/internal/verify"
)

// sessionCheckCheap adapts a *Session to CheckCheapSession so a test can
// drive LiveChecker.Run against the real checkCheap logic without standing
// up an Orchestrator. Production code never does this: gameLevel.StartLive
// passes an *Orchestrator, precisely so a live pass is serialized against a
// real Check and a Reset by the resetMu those share. Tests that want that
// serialization exercised use an Orchestrator directly instead; see
// TestLiveCheckNeverOverlapsARealCheck below.
type sessionCheckCheap struct{ *Session }

func (s sessionCheckCheap) CheckCheap(ctx context.Context) []verify.ObjectiveResult {
	return s.checkCheap(ctx)
}

// --- AC3: the transition rule ---

// TestUnknownToPassIsReportedOnce is the tick the feature exists for: an
// objective seen passing for the first time is reported, and reported only
// once. Calling transition directly, rather than driving Run and the
// channels, is deliberate: this is a statement about the rule, not about
// the throttle or the goroutine around it.
func TestUnknownToPassIsReportedOnce(t *testing.T) {
	l := NewLiveChecker(nil, time.Millisecond)
	objs := []verify.ObjectiveResult{{ID: "location", Status: verify.StatusPass}}

	first, _ := l.transition(objs)
	if len(first) != 1 || first[0].ID != "location" {
		t.Fatalf("first pass = %+v, want exactly one transition for location", first)
	}

	second, _ := l.transition(objs)
	if len(second) != 0 {
		t.Fatalf("second pass = %+v, want none: pass to pass must be quiet", second)
	}
}

// TestTransitionRuleIsQuietOnTheFirstFail is the full table point 7 of the
// design names, each case named so a failure says which one broke.
func TestTransitionRuleIsQuietOnTheFirstFail(t *testing.T) {
	tests := []struct {
		name       string
		known      bool
		prev, next verify.Status
		wantReport bool
	}{
		{"unknown to pass reports", false, "", verify.StatusPass, true},
		{"pass to fail reports", true, verify.StatusPass, verify.StatusFail, true},
		{"unknown to fail is silent", false, "", verify.StatusFail, false},
		{"fail to error is silent", true, verify.StatusFail, verify.StatusError, false},
		{"fail to timeout is silent", true, verify.StatusFail, verify.StatusTimeout, false},
		{"fail to pass reports", true, verify.StatusFail, verify.StatusPass, true},
		{"pass to pass is silent", true, verify.StatusPass, verify.StatusPass, false},
		{"pass to error is silent", true, verify.StatusPass, verify.StatusError, false},
		{"pass to timeout is silent", true, verify.StatusPass, verify.StatusTimeout, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := NewLiveChecker(nil, time.Millisecond)
			if tt.known {
				l.last["obj"] = tt.prev
			}

			got, _ := l.transition([]verify.ObjectiveResult{{ID: "obj", Status: tt.next}})
			reported := len(got) == 1 && got[0].ID == "obj"
			if reported != tt.wantReport {
				t.Errorf("known=%v prev=%q next=%q: reported=%v, want %v", tt.known, tt.prev, tt.next, reported, tt.wantReport)
			}
		})
	}
}

// --- AC4: the throttle ---

// fakeClock is a mutex-guarded time source for TestTenCommandsInsideThe...:
// a bare shared time.Time read from Run's goroutine and written from the
// test goroutine would be a real data race under -race, however carefully
// the two are interleaved, so this exists instead of one.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// TestTenCommandsInsideTheDebounceWindowRunTwoPasses drives the fake clock
// directly: LiveChecker.now and .after are unexported fields this test, in
// the same package, sets to fully control timing with no real sleeping.
//
// The leading-edge pass is held open deliberately (the verifier's onRun
// blocks until released), which is what makes the ten Notify calls that
// follow safe from racing Run's own consumption of the notify channel: Run
// cannot be reading it while it is blocked inside that first pass, so the
// burst is guaranteed to leave exactly the one coalesced notify pending that
// the second half of this test needs.
//
// Reading of the criterion: ten commands INSIDE ONE debounce window produce
// exactly two passes, one on the leading edge and one coalescing the rest.
// The load guarantee the criterion protects is one pass per debounce
// interval; a burst spread across more than one window legitimately needs
// more passes, because the later commands are not covered by the earlier
// one.
func TestTenCommandsInsideTheDebounceWindowRunTwoPasses(t *testing.T) {
	s, verifier, _ := newTestSession(t, Config{})

	l := NewLiveChecker(sessionCheckCheap{s}, 750*time.Millisecond)

	clock := &fakeClock{now: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}
	l.now = clock.Now
	// afterCalled announces that Run called l.after, which happens right
	// after Run reads the clock to compute its debounce remainder and right
	// before Run blocks waiting on the channel that call returns. Waiting for
	// this announcement, rather than inferring it from the first pass having
	// completed, is what makes the ordering below real instead of merely
	// likely: without it, the test could call clock.Advance before Run reads
	// the clock again, which would make Run compute an already-elapsed
	// `since`, skip the wait entirely, and pass immediately, leaving nothing
	// to ever receive on afterCh.
	afterCalled := make(chan time.Duration, 4)
	afterCh := make(chan time.Time)
	l.after = func(d time.Duration) <-chan time.Time {
		afterCalled <- d
		return afterCh
	}

	firstPassStarted := make(chan struct{})
	releaseFirstPass := make(chan struct{})
	passed := make(chan struct{}, 4)
	var calls int32
	verifier.onRun = func() {
		if atomic.AddInt32(&calls, 1) == 1 {
			close(firstPassStarted)
			<-releaseFirstPass
		}
		passed <- struct{}{}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		l.Run(ctx)
		close(done)
	}()

	l.Notify() // triggers the leading-edge pass, which onRun now holds open
	select {
	case <-firstPassStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("the leading-edge pass never started")
	}

	for i := 0; i < 10; i++ {
		l.Notify()
	}
	close(releaseFirstPass)

	select {
	case <-passed:
	case <-time.After(2 * time.Second):
		t.Fatal("the leading-edge pass never completed")
	}

	// Run has consumed the coalesced notify and entered its debounce wait.
	// Waiting for the call itself, rather than for the first pass having
	// completed, is what makes the ordering real: see afterCalled's own
	// comment above for why the two are not the same thing.
	var waited time.Duration
	select {
	case waited = <-afterCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("Run never reached its debounce wait for the coalesced notify")
	}
	// The clock has not moved since the leading-edge pass began, so Run
	// should be waiting out the whole debounce window, not some partial
	// remainder. This is the arithmetic a future edit to the throttle would
	// most easily get wrong, and the test above it never checked it.
	if waited != 750*time.Millisecond {
		t.Errorf("debounce wait = %s, want the whole 750ms window", waited)
	}

	clock.Advance(time.Second)
	afterCh <- clock.Now()

	select {
	case <-passed:
	case <-time.After(2 * time.Second):
		t.Fatal("the coalesced trailing pass never ran")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}

	// Safe to read now with no synchronization beyond <-done: Run's
	// goroutine has returned, so nothing else can still be writing it.
	if verifier.runCalls != 2 {
		t.Fatalf("runCalls = %d, want exactly 2 (never 1, never 3)", verifier.runCalls)
	}
}

// --- AC5: passes never overlap ---

// TestPassesNeverOverlap is design point 8 made into a test: a verifier
// that sets a flag on entry, fails the test if it was already set, sleeps a
// scheduling quantum, then clears it, catches a live checker that spawned a
// goroutine per notify instead of running single-flight.
func TestPassesNeverOverlap(t *testing.T) {
	s, verifier, _ := newTestSession(t, Config{})

	var running int32
	verifier.onRun = func() {
		if !atomic.CompareAndSwapInt32(&running, 0, 1) {
			t.Error("a pass started while another was already running")
			return
		}
		time.Sleep(time.Millisecond)
		atomic.StoreInt32(&running, 0)
	}

	l := NewLiveChecker(sessionCheckCheap{s}, time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		l.Run(ctx)
		close(done)
	}()

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				l.Notify()
			}
		}()
	}
	wg.Wait()

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

// --- AC6: notify and the bus handler never block the caller ---

// TestNotifyNeverBlocksWhileAPassIsRunning is design point 1(b): a verifier
// whose Run blocks lets this test hold a pass open and hammer Notify from
// the test goroutine, then confirm that burst collapsed into exactly one
// coalesced follow-up rather than one pass per Notify call.
func TestNotifyNeverBlocksWhileAPassIsRunning(t *testing.T) {
	s, verifier, _ := newTestSession(t, Config{})

	block := make(chan struct{})
	passStarted := make(chan struct{}, 10)
	var mu sync.Mutex
	passes := 0
	verifier.onRun = func() {
		mu.Lock()
		passes++
		mu.Unlock()
		passStarted <- struct{}{}
		<-block
	}

	l := NewLiveChecker(sessionCheckCheap{s}, time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		l.Run(ctx)
		close(done)
	}()

	l.Notify()
	select {
	case <-passStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("the first pass never started")
	}

	notifyDone := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			l.Notify()
		}
		close(notifyDone)
	}()

	select {
	case <-notifyDone:
	case <-time.After(2 * time.Second):
		t.Fatal("100 Notify calls did not return while a pass was running")
	}

	close(block) // release the held pass; every later onRun call returns at once too

	select {
	case <-passStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("the coalesced follow-up pass never ran")
	}

	// Give Run a moment to (wrongly) start a third pass, if it were going
	// to: nothing further was ever notified after the burst of 100, so
	// nothing legitimate would trigger one.
	time.Sleep(20 * time.Millisecond)

	mu.Lock()
	got := passes
	mu.Unlock()
	if got != 2 {
		t.Errorf("passes = %d, want exactly 2 (the initial pass plus one coalesced follow-up), not one per Notify call", got)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

// TestBusHandlerDoesNoWorkOnThePublishingGoroutine is design point 1(a):
// bus.Publish runs subscribers synchronously on the publishing goroutine,
// which is the goroutine that called JournalSink.Drain, so the handler
// Attach registers must do nothing but a non-blocking notify.
func TestBusHandlerDoesNoWorkOnThePublishingGoroutine(t *testing.T) {
	s, verifier, _ := newTestSession(t, Config{})
	l := NewLiveChecker(sessionCheckCheap{s}, time.Millisecond)

	b := bus.New()
	detach := l.Attach(b)
	defer detach()

	done := make(chan struct{})
	go func() {
		b.Publish(context.Background(), bus.CommandExecuted{LevelID: "nav-01"})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish did not return; the live-check handler must not do work on the publishing goroutine")
	}

	// Run was never started, so nothing else could have touched runCalls:
	// safe to read directly.
	if verifier.runCalls != 0 {
		t.Errorf("runCalls = %d, want 0: Run was never started, so no pass could have happened", verifier.runCalls)
	}
}
