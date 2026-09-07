package achievements

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/game/bus"
	"github.com/JoottunAtish/ShellForge/internal/store"
)

// Rule is one achievement.
//
// A rule is a pure decision over an event plus the counter it has already
// accumulated. It reads no database, publishes nothing, and never blocks:
// persistence and publication are the Registry's job, which is what keeps
// rules.go a list of small functions anybody can add to.
type Rule struct {
	// Key is the achievement's stable identifier, and the primary key it
	// is stored under.
	Key string

	// Title is the badge's display name.
	Title string

	// Description is shown when it unlocks and in shellforge stats.
	Description string

	// Progress advances the counter and reports whether the achievement is
	// now earned. counter is whatever this rule stored last, starting at
	// zero. An event a rule does not care about must return the counter
	// unchanged and false.
	Progress func(ev bus.Event, counter float64) (next float64, unlocked bool)
}

// Achievements is the persistence this package needs, declared here in the
// consumer rather than taken as a *store.Store, so a registry can be tested
// against a three method fake with no sqlite file. *store.Store satisfies
// it.
type Achievements interface {
	// Achievements returns every achievement row recorded for a profile.
	Achievements(ctx context.Context, profileID int64) (map[string]store.Achievement, error)

	// SaveProgress records a rule's counter.
	SaveProgress(ctx context.Context, profileID int64, key string, progress float64) error

	// Unlock marks an achievement earned, reporting whether this call is
	// the one that earned it.
	Unlock(ctx context.Context, profileID int64, key string, at time.Time) (bool, error)
}

// Registry subscribes every rule to a bus and persists what they decide.
type Registry struct {
	store     Achievements
	profileID int64
	now       func() time.Time
	rules     []Rule

	mu       sync.Mutex
	counters map[string]float64
	unlocked map[string]bool
}

// Option configures a Registry.
type Option func(*Registry)

// WithRules replaces the rule set.
//
// It exists for two callers: a test that wants one rule in isolation, and
// the pluggability assertion that registers a rule declared in a test file
// and shows it works with no production change.
func WithRules(rules []Rule) Option {
	return func(r *Registry) { r.rules = rules }
}

// New returns a Registry over pack's rules, ready to Attach.
//
// pack is a parameter rather than absent, which is a deliberate divergence
// from issue #127's interface sketch: five of the rules are questions about
// the pack ("every level in this act", "at or under this level's par") and
// there is no honest way to answer them without it. A nil pack is legal and
// yields only the rules that do not need one, so a caller with no pack
// loaded gets fewer achievements rather than wrong ones.
//
// now defaults to time.Now and is the clock the unlock timestamp is read
// from.
func New(st Achievements, pack *content.Pack, profileID int64, now func() time.Time, opts ...Option) *Registry {
	if now == nil {
		now = time.Now
	}
	r := &Registry{
		store:     st,
		profileID: profileID,
		now:       now,
		rules:     RulesFor(pack),
		counters:  make(map[string]float64),
		unlocked:  make(map[string]bool),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Rules returns the rule set this Registry will apply, in order.
func (r *Registry) Rules() []Rule { return r.rules }

// Attach loads what this profile has already earned and subscribes every
// rule to b, returning the function that unsubscribes them all.
//
// Each rule is its own bus subscriber, named "achievements.<key>". That is
// not incidental: the bus contains a panic per subscriber, so one rule
// panicking leaves every other rule still running and the learner's session
// untouched. One shared subscriber looping over the rules would lose every
// rule after the one that panicked.
//
// The returned detach is safe to call more than once and is safe to call
// from inside a handler.
func (r *Registry) Attach(ctx context.Context, b *bus.Bus) (detach func(), err error) {
	if b == nil {
		return nil, fmt.Errorf("achievements: attach to a nil bus")
	}

	recorded, err := r.store.Achievements(ctx, r.profileID)
	if err != nil {
		return nil, fmt.Errorf("read what this profile has already earned: %w", err)
	}

	r.mu.Lock()
	for key, a := range recorded {
		r.counters[key] = a.Progress
		r.unlocked[key] = a.Unlocked()
	}
	r.mu.Unlock()

	unsubs := make([]func(), 0, len(r.rules))
	for _, rule := range r.rules {
		rule := rule
		unsubs = append(unsubs, b.Subscribe("achievements."+rule.Key, func(ctx context.Context, ev bus.Event) {
			r.apply(ctx, b, rule, ev)
		}))
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			for _, unsub := range unsubs {
				unsub()
			}
		})
	}, nil
}

// Unlocked reports the keys this registry has seen earned, for a caller
// that wants to render them without going back to the database.
func (r *Registry) Unlocked() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	// In rule order rather than map order, so a banner listing two
	// achievements lists them the same way twice.
	out := make([]string, 0, len(r.unlocked))
	for _, rule := range r.rules {
		if r.unlocked[rule.Key] {
			out = append(out, rule.Key)
		}
	}
	return out
}

// apply runs one rule against one event and persists whatever changed.
//
// A store that will not accept the write is swallowed rather than
// surfaced. A badge is cosmetic, this runs inside the publishing goroutine
// of whatever just happened to the learner, and failing a level because an
// achievement counter could not be saved would be absurd. The write that
// matters, the learner's progress, is the orchestrator's and reports its
// own failures.
func (r *Registry) apply(ctx context.Context, b *bus.Bus, rule Rule, ev bus.Event) {
	if rule.Progress == nil {
		return
	}

	r.mu.Lock()
	if r.unlocked[rule.Key] {
		r.mu.Unlock()
		return
	}
	counter := r.counters[rule.Key]
	r.mu.Unlock()

	next, unlocked := rule.Progress(ev, counter)

	if next != counter {
		r.mu.Lock()
		r.counters[rule.Key] = next
		r.mu.Unlock()
		_ = r.store.SaveProgress(ctx, r.profileID, rule.Key, next)
	}

	if !unlocked {
		return
	}

	// The store decides whether this is the first time, so a restart that
	// re-earns something announces nothing. Its bool is what stops a second
	// AchievementUnlocked and a second row.
	at := r.now()
	first, err := r.store.Unlock(ctx, r.profileID, rule.Key, at)
	if err != nil {
		return
	}

	r.mu.Lock()
	r.unlocked[rule.Key] = true
	r.mu.Unlock()

	if !first {
		return
	}
	b.Publish(ctx, bus.AchievementUnlocked{Key: rule.Key, At: at})
}
