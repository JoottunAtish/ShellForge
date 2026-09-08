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

// --- AC3: the transition rule ---

// TestUnknownToPassIsReportedOnce is the tick the feature exists for: an
// objective seen passing for the first time is reported, and reported only
// once. Calling transition directly, rather than driving Run and the
// channels, is deliberate: this is a statement about the rule, not about
// the throttle or the goroutine around it.
func TestUnknownToPassIsReportedOnce(t *testing.T) {
	l := NewLiveChecker(nil, time.Millisecond)
	objs := []verify.ObjectiveResult{{ID: "location", Status: verify.StatusPass}}

	first := l.transition(objs)
	if len(first) != 1 || first[0].ID != "location" {
		t.Fatalf("first pass = %+v, want exactly one transition for location", first)
	}

	second := l.transition(objs)
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := NewLiveChecker(nil, time.Millisecond)
			if tt.known {
				l.last["obj"] = tt.prev
			}

			got := l.transition([]verify.ObjectiveResult{{ID: "obj", Status: tt.next}})
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

	l := NewLiveChecker(s, 750*time.Millisecond)

	clock := &fakeClock{now: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}
	l.now = clock.Now
	afterCh := make(chan time.Time)
	l.after = func(time.Duration) <-chan time.Time { return afterCh }

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

	// Run has looped back and found the one coalesced notify pending. The
	// clock has not moved, so it must be waiting out the debounce window
	// rather than passing again immediately; advancing it and releasing
	// the wait is what lets the second pass run.
	clock.Advance(time.Second)
	select {
	case afterCh <- clock.Now():
	case <-time.After(2 * time.Second):
		t.Fatal("Run never reached its debounce wait for the coalesced notify")
	}

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

	l := NewLiveChecker(s, time.Millisecond)

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

	l := NewLiveChecker(s, time.Millisecond)

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
	l := NewLiveChecker(s, time.Millisecond)

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
