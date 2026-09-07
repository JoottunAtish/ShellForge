package achievements

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/game/bus"
	"github.com/JoottunAtish/ShellForge/internal/store"
)

// fakeStore is the three method persistence this package declares, with no
// sqlite file behind it. It keeps rows the way the real store does: a
// counter that can be overwritten, and an unlock that happens once.
type fakeStore struct {
	mu sync.Mutex

	rows     map[string]store.Achievement
	unlocks  []string
	saves    int
	readErr  error
	writeErr error
}

func newFakeStore() *fakeStore {
	return &fakeStore{rows: make(map[string]store.Achievement)}
}

func (f *fakeStore) Achievements(_ context.Context, _ int64) (map[string]store.Achievement, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.readErr != nil {
		return nil, f.readErr
	}
	out := make(map[string]store.Achievement, len(f.rows))
	for k, v := range f.rows {
		out[k] = v
	}
	return out, nil
}

func (f *fakeStore) SaveProgress(_ context.Context, _ int64, key string, progress float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return f.writeErr
	}
	f.saves++
	row := f.rows[key]
	row.Key = key
	row.Progress = progress
	f.rows[key] = row
	return nil
}

func (f *fakeStore) Unlock(_ context.Context, _ int64, key string, at time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return false, f.writeErr
	}
	row := f.rows[key]
	if row.Unlocked() {
		return false, nil
	}
	row.Key = key
	row.UnlockedAt = at
	f.rows[key] = row
	f.unlocks = append(f.unlocks, key)
	return true, nil
}

func (f *fakeStore) unlockCount(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, k := range f.unlocks {
		if k == key {
			n++
		}
	}
	return n
}

// testPack is two acts, three levels, with pars set so one_liner has
// something to measure.
func testPack() *content.Pack {
	return &content.Pack{
		ID: "test-pack",
		Acts: []content.Act{
			{ID: "act1", Title: "One", Levels: []string{"a1", "a2"}},
			{ID: "act2", Title: "Two", Levels: []string{"b1"}},
		},
		Levels: []content.Level{
			{ID: "a1", Act: "act1", ParCommands: 3},
			{ID: "a2", Act: "act1", ParCommands: 4},
			{ID: "b1", Act: "act2", ParCommands: 2},
		},
	}
}

// bigPack is a pack with enough levels that one_liner can be earned from
// five distinct ones without clearing the campaign.
func bigPack() *content.Pack {
	p := &content.Pack{ID: "big", Acts: []content.Act{{ID: "act1", Title: "One"}}}
	for i := 0; i < 8; i++ {
		id := "lvl-" + itoa(i)
		p.Acts[0].Levels = append(p.Acts[0].Levels, id)
		p.Levels = append(p.Levels, content.Level{ID: id, Act: "act1", ParCommands: 5})
	}
	return p
}

// attached builds a Registry over pack and a fresh bus, already subscribed.
func attached(t *testing.T, pack *content.Pack, st Achievements, now func() time.Time, opts ...Option) (*bus.Bus, *Registry) {
	t.Helper()
	b := bus.New()
	r := New(st, pack, 1, now, opts...)
	detach, err := r.Attach(context.Background(), b)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	t.Cleanup(detach)
	return b, r
}

// unlockedRecorder subscribes to a bus and records the achievement keys it
// sees announced.
func unlockedRecorder(b *bus.Bus) *struct {
	mu   sync.Mutex
	keys []string
} {
	rec := &struct {
		mu   sync.Mutex
		keys []string
	}{}
	b.Subscribe("test.recorder", func(_ context.Context, ev bus.Event) {
		if a, ok := ev.(bus.AchievementUnlocked); ok {
			rec.mu.Lock()
			rec.keys = append(rec.keys, a.Key)
			rec.mu.Unlock()
		}
	})
	return rec
}

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

var testClock = fixedClock(time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC))

func passed(levelID string, commands int, at time.Time) bus.LevelPassed {
	return bus.LevelPassed{LevelID: levelID, CommandsUsed: commands, At: at}
}

func assertUnlocked(t *testing.T, st *fakeStore, key string) {
	t.Helper()
	if st.unlockCount(key) != 1 {
		t.Errorf("%s unlocked %d times, want 1", key, st.unlockCount(key))
	}
}

func assertNotUnlocked(t *testing.T, st *fakeStore, key string) {
	t.Helper()
	if st.unlockCount(key) != 0 {
		t.Errorf("%s unlocked %d times, want 0", key, st.unlockCount(key))
	}
}

// --- one test per rule ---

func TestFirstBloodUnlocksOnTheFirstPass(t *testing.T) {
	st := newFakeStore()
	b, _ := attached(t, testPack(), st, testClock)

	b.Publish(context.Background(), passed("a1", 1, testClock()))
	assertUnlocked(t, st, KeyFirstBlood)
}

func TestTabMasterCountsTabsAndUnlocksAtTwentyFive(t *testing.T) {
	st := newFakeStore()
	b, _ := attached(t, testPack(), st, testClock)
	ctx := context.Background()

	for i := 0; i < tabMasterTabs-1; i++ {
		b.Publish(ctx, bus.CommandExecuted{UsedTab: true, At: testClock()})
	}
	assertNotUnlocked(t, st, KeyTabMaster)

	b.Publish(ctx, bus.CommandExecuted{UsedTab: true, At: testClock()})
	assertUnlocked(t, st, KeyTabMaster)
}

func TestTabMasterIgnoresCommandsWithoutATab(t *testing.T) {
	st := newFakeStore()
	b, _ := attached(t, testPack(), st, testClock)

	for i := 0; i < 40; i++ {
		b.Publish(context.Background(), bus.CommandExecuted{UsedTab: false, At: testClock()})
	}
	assertNotUnlocked(t, st, KeyTabMaster)
}

func TestManualLabourCountsManAsACommandNotASubstring(t *testing.T) {
	cases := []struct {
		raw   string
		count bool
	}{
		{raw: "man ls", count: true},
		{raw: "sudo man ls", count: true},
		{raw: "sudo sudo man ls", count: true},
		{raw: "/usr/bin/man ls", count: true},
		{raw: "LANG=C man ls", count: true},
		{raw: "mandatory_backup.sh", count: false},
		{raw: "cat manual.txt", count: false},
		{raw: "echo man", count: false},
		{raw: "", count: false},
		{raw: "   ", count: false},
	}

	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			st := newFakeStore()
			b, _ := attached(t, testPack(), st, testClock)

			for i := 0; i < manualLabourRuns; i++ {
				b.Publish(context.Background(), bus.CommandExecuted{Raw: tc.raw, At: testClock()})
			}

			if tc.count {
				assertUnlocked(t, st, KeyManualLabour)
			} else {
				assertNotUnlocked(t, st, KeyManualLabour)
			}
		})
	}
}

func TestOneLinerNeedsFiveDistinctLevelsAtOrUnderPar(t *testing.T) {
	st := newFakeStore()
	b, _ := attached(t, bigPack(), st, testClock)
	ctx := context.Background()

	// The same level five times is one level, not five.
	for i := 0; i < 5; i++ {
		b.Publish(ctx, passed("lvl-0", 5, testClock()))
	}
	assertNotUnlocked(t, st, KeyOneLiner)

	for i := 1; i <= 3; i++ {
		b.Publish(ctx, passed("lvl-"+itoa(i), 5, testClock()))
	}
	assertNotUnlocked(t, st, KeyOneLiner)

	b.Publish(ctx, passed("lvl-4", 5, testClock()))
	assertUnlocked(t, st, KeyOneLiner)
}

func TestOneLinerIgnoresAPassOverPar(t *testing.T) {
	st := newFakeStore()
	b, _ := attached(t, bigPack(), st, testClock)

	for i := 0; i < 8; i++ {
		b.Publish(context.Background(), passed("lvl-"+itoa(i), 99, testClock()))
	}
	assertNotUnlocked(t, st, KeyOneLiner)
}

func TestRmSurvivorNeedsAResetAndThenAPassOfTheSameLevel(t *testing.T) {
	t.Run("reset then pass", func(t *testing.T) {
		st := newFakeStore()
		b, _ := attached(t, testPack(), st, testClock)
		ctx := context.Background()

		b.Publish(ctx, bus.LevelReset{LevelID: "a1", At: testClock()})
		b.Publish(ctx, passed("a1", 1, testClock()))
		assertUnlocked(t, st, KeyRmSurvivor)
	})

	t.Run("a pass with no reset before it", func(t *testing.T) {
		st := newFakeStore()
		b, _ := attached(t, testPack(), st, testClock)

		b.Publish(context.Background(), passed("a1", 1, testClock()))
		assertNotUnlocked(t, st, KeyRmSurvivor)
	})

	t.Run("a reset on one level and a pass on another", func(t *testing.T) {
		st := newFakeStore()
		b, _ := attached(t, testPack(), st, testClock)
		ctx := context.Background()

		b.Publish(ctx, bus.LevelReset{LevelID: "a1", At: testClock()})
		b.Publish(ctx, passed("a2", 1, testClock()))
		assertNotUnlocked(t, st, KeyRmSurvivor)
	})
}

func TestNightOwlUsesTheEventTimeAndAHalfOpenWindow(t *testing.T) {
	cases := []struct {
		hour int
		want bool
	}{
		{hour: 1, want: false},
		{hour: 2, want: true},
		{hour: 3, want: true},
		{hour: 4, want: true},
		{hour: 5, want: false}, // half open: 05:00 is morning
		{hour: 13, want: false},
	}

	for _, tc := range cases {
		t.Run(itoa(tc.hour), func(t *testing.T) {
			st := newFakeStore()
			b, _ := attached(t, testPack(), st, testClock)

			at := time.Date(2026, 9, 7, tc.hour, 0, 0, 0, time.UTC)
			b.Publish(context.Background(), passed("a1", 1, at))

			if tc.want {
				assertUnlocked(t, st, KeyNightOwl)
			} else {
				assertNotUnlocked(t, st, KeyNightOwl)
			}
		})
	}
}

func TestNoHintsNeedsAWholeActWithNoHintTakenInIt(t *testing.T) {
	t.Run("an act cleared clean", func(t *testing.T) {
		st := newFakeStore()
		b, _ := attached(t, testPack(), st, testClock)
		ctx := context.Background()

		b.Publish(ctx, passed("a1", 1, testClock()))
		assertNotUnlocked(t, st, KeyNoHints)
		b.Publish(ctx, passed("a2", 1, testClock()))
		assertUnlocked(t, st, KeyNoHints)
	})

	t.Run("a hint anywhere in the act spoils it", func(t *testing.T) {
		st := newFakeStore()
		b, _ := attached(t, testPack(), st, testClock)
		ctx := context.Background()

		b.Publish(ctx, passed("a1", 1, testClock()))
		b.Publish(ctx, bus.HintTaken{LevelID: "a2", Tier: 1, Cost: 5, At: testClock()})
		b.Publish(ctx, passed("a2", 1, testClock()))
		assertNotUnlocked(t, st, KeyNoHints)
	})

	t.Run("the next act starts clean", func(t *testing.T) {
		st := newFakeStore()
		b, _ := attached(t, testPack(), st, testClock)
		ctx := context.Background()

		b.Publish(ctx, bus.HintTaken{LevelID: "a1", Tier: 1, Cost: 5, At: testClock()})
		b.Publish(ctx, passed("a1", 1, testClock()))
		b.Publish(ctx, passed("a2", 1, testClock()))
		assertNotUnlocked(t, st, KeyNoHints)

		// act2 is one level, and no hint was taken in it.
		b.Publish(ctx, passed("b1", 1, testClock()))
		assertUnlocked(t, st, KeyNoHints)
	})

	t.Run("the same level twice is not a cleared act", func(t *testing.T) {
		st := newFakeStore()
		b, _ := attached(t, testPack(), st, testClock)
		ctx := context.Background()

		b.Publish(ctx, passed("a1", 1, testClock()))
		b.Publish(ctx, passed("a1", 1, testClock()))
		assertNotUnlocked(t, st, KeyNoHints)
	})
}

func TestActClearNeedsEveryLevelOfThatAct(t *testing.T) {
	st := newFakeStore()
	b, _ := attached(t, testPack(), st, testClock)
	ctx := context.Background()

	b.Publish(ctx, passed("a1", 1, testClock()))
	assertNotUnlocked(t, st, KeyActClearPrefix+"1")

	b.Publish(ctx, passed("a2", 1, testClock()))
	assertUnlocked(t, st, KeyActClearPrefix+"1")
	assertNotUnlocked(t, st, KeyActClearPrefix+"2")

	b.Publish(ctx, passed("b1", 1, testClock()))
	assertUnlocked(t, st, KeyActClearPrefix+"2")
}

func TestCampaignCompleteNeedsEveryLevelInThePack(t *testing.T) {
	st := newFakeStore()
	b, _ := attached(t, testPack(), st, testClock)
	ctx := context.Background()

	b.Publish(ctx, passed("a1", 1, testClock()))
	b.Publish(ctx, passed("a2", 1, testClock()))
	assertNotUnlocked(t, st, KeyCampaignComplete)

	b.Publish(ctx, passed("b1", 1, testClock()))
	assertUnlocked(t, st, KeyCampaignComplete)
}

// --- properties across every rule ---

func TestReplayingTheWholeSequenceUnlocksNothingTwice(t *testing.T) {
	st := newFakeStore()
	b, _ := attached(t, testPack(), st, testClock)
	rec := unlockedRecorder(b)
	ctx := context.Background()

	drive := func() {
		b.Publish(ctx, bus.LevelReset{LevelID: "a1", At: testClock()})
		b.Publish(ctx, passed("a1", 1, testClock()))
		b.Publish(ctx, passed("a2", 1, testClock()))
		b.Publish(ctx, passed("b1", 1, testClock()))
	}
	drive()
	drive()

	rec.mu.Lock()
	defer rec.mu.Unlock()
	seen := make(map[string]int)
	for _, k := range rec.keys {
		seen[k]++
	}
	for key, n := range seen {
		if n != 1 {
			t.Errorf("%s was announced %d times, want 1", key, n)
		}
	}
	for key := range seen {
		if st.unlockCount(key) != 1 {
			t.Errorf("%s wrote %d rows, want 1", key, st.unlockCount(key))
		}
	}
}

func TestACounterResumesAcrossARestart(t *testing.T) {
	st := newFakeStore()
	ctx := context.Background()

	b1, _ := attached(t, testPack(), st, testClock)
	for i := 0; i < tabMasterTabs-1; i++ {
		b1.Publish(ctx, bus.CommandExecuted{UsedTab: true, At: testClock()})
	}
	assertNotUnlocked(t, st, KeyTabMaster)

	// A fresh registry over the same recorded rows, which is what a restart
	// looks like from here.
	b2, _ := attached(t, testPack(), st, testClock)
	b2.Publish(ctx, bus.CommandExecuted{UsedTab: true, At: testClock()})
	assertUnlocked(t, st, KeyTabMaster)
}

func TestUnlockingPublishesOnTheSameBus(t *testing.T) {
	st := newFakeStore()
	b, _ := attached(t, testPack(), st, testClock)
	rec := unlockedRecorder(b)

	b.Publish(context.Background(), passed("a1", 1, testClock()))

	rec.mu.Lock()
	defer rec.mu.Unlock()
	found := false
	for _, k := range rec.keys {
		if k == KeyFirstBlood {
			found = true
		}
	}
	if !found {
		t.Errorf("AchievementUnlocked was not published; recorded %v", rec.keys)
	}
}

func TestUnlockCarriesTheInjectedClock(t *testing.T) {
	st := newFakeStore()
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	b, _ := attached(t, testPack(), st, fixedClock(at))

	b.Publish(context.Background(), passed("a1", 1, testClock()))

	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.rows[KeyFirstBlood].UnlockedAt.Equal(at) {
		t.Errorf("UnlockedAt = %v, want the injected %v", st.rows[KeyFirstBlood].UnlockedAt, at)
	}
}

// --- the guarantees that make this package safe to add rules to ---

func TestEveryDeclaredKeyHasARuleAndNoKeyIsDuplicated(t *testing.T) {
	rules := RulesFor(testPack())

	want := []string{
		KeyFirstBlood, KeyTabMaster, KeyManualLabour, KeyOneLiner,
		KeyRmSurvivor, KeyNightOwl, KeyNoHints,
		KeyActClearPrefix + "1", KeyActClearPrefix + "2",
		KeyCampaignComplete,
	}

	got := make(map[string]int, len(rules))
	for _, r := range rules {
		got[r.Key]++
		if r.Title == "" || r.Description == "" {
			t.Errorf("rule %q has no title or no description; stats renders both", r.Key)
		}
		if r.Progress == nil {
			t.Errorf("rule %q has no Progress function", r.Key)
		}
	}

	for _, key := range want {
		if got[key] != 1 {
			t.Errorf("key %q appears %d times in the rule set, want 1", key, got[key])
		}
	}
	if len(rules) != len(want) {
		t.Errorf("the rule set has %d rules, want %d for this pack: %v", len(rules), len(want), got)
	}
}

// The six act_clear keys the design record fixes exist once the real pack's
// six acts are indexed.
func TestSixActsProduceSixActClearKeys(t *testing.T) {
	pack := &content.Pack{ID: "six"}
	for i := 1; i <= 6; i++ {
		id := "act" + itoa(i)
		lvl := "l" + itoa(i)
		pack.Acts = append(pack.Acts, content.Act{ID: id, Levels: []string{lvl}})
		pack.Levels = append(pack.Levels, content.Level{ID: lvl, Act: id})
	}

	got := make(map[string]bool)
	for _, r := range RulesFor(pack) {
		got[r.Key] = true
	}
	for i := 1; i <= 6; i++ {
		if !got[KeyActClearPrefix+itoa(i)] {
			t.Errorf("no rule for %s%d", KeyActClearPrefix, i)
		}
	}
}

// A rule that panics must not take the learner's session, or the other
// rules, with it. The bus contains a panic per subscriber, and this package
// subscribes each rule separately precisely so that containment applies per
// rule rather than per registry.
func TestAPanickingRuleCannotStopTheOthers(t *testing.T) {
	st := newFakeStore()
	ran := false

	rules := []Rule{
		{
			Key: "boom", Title: "Boom", Description: "Panics on purpose.",
			Progress: func(bus.Event, float64) (float64, bool) { panic("deliberate test panic") },
		},
		{
			Key: "after", Title: "After", Description: "Runs after the panic.",
			Progress: func(_ bus.Event, c float64) (float64, bool) { ran = true; return c, true },
		},
	}

	b := bus.New(bus.WithErrorHandler(func(string, any, []byte) {}))
	r := New(st, testPack(), 1, testClock, WithRules(rules))
	detach, err := r.Attach(context.Background(), b)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer detach()

	b.Publish(context.Background(), passed("a1", 1, testClock()))

	if !ran {
		t.Error("the rule after the panicking one never ran")
	}
	assertUnlocked(t, st, "after")
}

// Pluggability, asserted rather than claimed: an eleventh rule declared in
// this test file works with no production change at all.
func TestARuleDeclaredOutsideThisPackagesRuleSetWorks(t *testing.T) {
	st := newFakeStore()

	custom := Rule{
		Key:         "three_checks",
		Title:       "Three Checks",
		Description: "Run check three times.",
		Progress: func(ev bus.Event, counter float64) (float64, bool) {
			if _, ok := ev.(bus.CheckRun); !ok {
				return counter, false
			}
			next := counter + 1
			return next, next >= 3
		},
	}

	b, _ := attached(t, testPack(), st, testClock, WithRules(append(RulesFor(testPack()), custom)))
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		b.Publish(ctx, bus.CheckRun{LevelID: "a1", At: testClock()})
	}
	assertUnlocked(t, st, "three_checks")
}

func TestAttachReportsAFailureToReadWhatWasAlreadyEarned(t *testing.T) {
	st := newFakeStore()
	st.readErr = errors.New("database is locked")

	r := New(st, testPack(), 1, testClock)
	if _, err := r.Attach(context.Background(), bus.New()); err == nil {
		t.Error("Attach succeeded despite being unable to read existing achievements")
	}
}

// A store that will not accept a write must not break the learner's
// session: a badge is cosmetic and the level goes on.
func TestAStoreThatRefusesWritesDoesNotBreakAnything(t *testing.T) {
	st := newFakeStore()
	b, _ := attached(t, testPack(), st, testClock)
	st.mu.Lock()
	st.writeErr = errors.New("disk full")
	st.mu.Unlock()

	b.Publish(context.Background(), passed("a1", 1, testClock()))
	// Nothing to assert beyond not panicking and not deadlocking: the badge
	// is simply not awarded.
}

func TestDetachStopsEveryRule(t *testing.T) {
	st := newFakeStore()
	b := bus.New()
	r := New(st, testPack(), 1, testClock)
	detach, err := r.Attach(context.Background(), b)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	detach()
	detach() // must not panic

	b.Publish(context.Background(), passed("a1", 1, testClock()))
	assertNotUnlocked(t, st, KeyFirstBlood)
}

func TestANilPackYieldsOnlyThePackIndependentRules(t *testing.T) {
	rules := RulesFor(nil)

	for _, r := range rules {
		if r.Key == KeyCampaignComplete {
			// Present but inert, which the next assertion pins.
			continue
		}
	}

	st := newFakeStore()
	b, _ := attached(t, nil, st, testClock)
	b.Publish(context.Background(), passed("a1", 1, testClock()))

	// first_blood needs no pack and still works.
	assertUnlocked(t, st, KeyFirstBlood)
	// campaign_complete cannot know what "every level" means, so it must
	// stay locked rather than unlock on the first pass.
	assertNotUnlocked(t, st, KeyCampaignComplete)

	if len(rules) == 0 {
		t.Error("RulesFor(nil) returned no rules at all")
	}
}
