package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/game"
	"github.com/JoottunAtish/ShellForge/internal/game/bus"
	"github.com/JoottunAtish/ShellForge/internal/journal"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/store"
	"github.com/JoottunAtish/ShellForge/internal/verify"
)

// TestGameResponderBriefIncludesTheChecklist is the regression test for the
// blocking review finding: `brief`, typed inside a live session, used to
// render only the briefing prose and drop the objective checklist entirely,
// even though the pre-attach screen shows both. The checklist is the only
// place a level's deliverable filenames and paths appear when the briefing
// prose itself does not name them, and `hint` sends a learner who asks for
// help back to `brief` for exactly that list.
//
// gameResponder.Reply's "brief" branch never touches r.session, so a nil
// session is fine here: this is the same seam TestEveryReplyLineEndsCRLF
// exercises for renderCheckReply, one level up.
func TestGameResponderBriefIncludesTheChecklist(t *testing.T) {
	level := briefingLevel()
	r := &gameResponder{level: level, color: false}

	out := r.Reply(context.Background(), "brief", "")

	for _, want := range []string{level.Objectives[0].Text, level.Objectives[1].Text, "(bonus)"} {
		if !strings.Contains(out, want) {
			t.Errorf("brief reply lost %q, so the checklist did not render:\n%q", want, out)
		}
	}

	// The briefing prose must still be there too: brief replaces nothing,
	// it adds the checklist underneath.
	if !strings.Contains(out, "08:15") {
		t.Errorf("brief reply lost the briefing prose:\n%q", out)
	}
}

// TestGameResponderBriefEndsCRLF belongs with TestEveryReplyLineEndsCRLF in
// render_check_test.go in spirit: gameResponder.Reply writes straight into a
// raw-mode terminal on four of its five branches, and only renderCheckReply
// and renderCheckError were covered by that table. A missing crlf() on any of
// the other branches is the same staircase bug and would ship the same way.
func TestGameResponderBriefEndsCRLF(t *testing.T) {
	level := briefingLevel()
	r := &gameResponder{level: level, color: false}

	out := r.Reply(context.Background(), "brief", "")

	for i := 0; i < len(out); i++ {
		if out[i] != '\n' {
			continue
		}
		if i == 0 || out[i-1] != '\r' {
			t.Fatalf("a newline at byte %d is not preceded by a carriage return; raw mode would render this as a staircase:\n%q", i, out)
		}
	}
	if !strings.HasSuffix(out, "\r\n") {
		t.Errorf("the reply does not end in CRLF; the shim prints it verbatim:\n%q", out)
	}
}

// TestGameResponderHintPointsAtBriefTruthfully pins the other half of the
// same review finding. The hint stub tells a learner to type `brief` to see
// the objectives again; now that brief actually shows them, this only
// documents the fix stays true rather than doing anything on its own.
func TestGameResponderHintPointsAtBriefTruthfully(t *testing.T) {
	level := briefingLevel()
	r := &gameResponder{level: level, color: false}

	hint := r.Reply(context.Background(), "hint", "")
	if !strings.Contains(hint, "brief") {
		t.Fatalf("the hint stub no longer points the learner at `brief`:\n%q", hint)
	}

	brief := r.Reply(context.Background(), "brief", "")
	if !strings.Contains(brief, level.Objectives[0].Text) {
		t.Errorf("hint points at `brief` for the objectives, but brief does not show them:\n%q", brief)
	}
}

// --- regressions found in review ---

// --- the live drain wire ---

// liveFakeSession answers Exec generically with a zero exit, which is all
// setup.Runner needs to materialize a level's world, and serves journal.tsv
// content through PullFile, which is what lets a real journal.Collector find
// commands with no sandbox at all.
type liveFakeSession struct {
	mu   sync.Mutex
	data []byte
}

func (f *liveFakeSession) Exec(_ context.Context, argv []string, _ runtime.ExecOpts) (runtime.ExecResult, error) {
	// setup.Runner resolves the level root with `readlink -m -- <path>` and
	// refuses unless the answer echoes the operand back, which is what a
	// real readlink -m does for a path with no symlink on it.
	if len(argv) > 0 && argv[0] == "readlink" {
		return runtime.ExecResult{ExitCode: 0, Stdout: []byte(argv[len(argv)-1] + "\n")}, nil
	}
	// file_exists (and every other fs check) resolves state with
	// `stat -c "%F|%a|%U|%G" -- path`; answering it as an existing regular
	// file is what lets a cheap check actually report StatusPass here.
	if len(argv) > 0 && argv[0] == "stat" {
		return runtime.ExecResult{ExitCode: 0, Stdout: []byte("regular file|644|learner|learner\n")}, nil
	}
	return runtime.ExecResult{ExitCode: 0}, nil
}

func (f *liveFakeSession) Attach(context.Context, runtime.AttachOpts) (runtime.PTY, error) {
	return nil, errors.New("liveFakeSession: Attach is not implemented")
}

func (f *liveFakeSession) PushFiles(context.Context, runtime.FileManifest) error { return nil }

func (f *liveFakeSession) PullFile(context.Context, string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]byte, len(f.data))
	copy(out, f.data)
	return out, nil
}

func (f *liveFakeSession) Close() error { return nil }

// waitUntil polls cond until it reports true or budget elapses, failing the
// test on a timeout. It exists because the live drain runs on its own
// goroutine and reports nothing back directly: what a caller can observe is
// the state it left behind, which takes an unknown, short amount of time to
// appear.
func waitUntil(t *testing.T, budget time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !cond() {
		t.Fatal("condition was not satisfied within the budget")
	}
}

// TestGameLevelCommandRanDrainsTheJournal is the AC1 test for the drain wire:
// a command event reaching CommandRan must make its way, through StartLive's
// own drain goroutine, into a real Drain call naming this level and this
// attempt.
func TestGameLevelCommandRanDrainsTheJournal(t *testing.T) {
	ctx := context.Background()

	sess := &liveFakeSession{data: []byte("1755000000.000000\t0\t/home/learner\tpwd\n")}

	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "progress.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	level := &content.Level{ID: "nav-01", Setup: content.Setup{Root: "/home/learner/quest"}}

	session, err := game.NewSession(game.Config{
		Level: level, Sess: sess, Verifier: verify.NewEngine(),
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	b := bus.New()
	var mu sync.Mutex
	var events []bus.CommandExecuted
	b.Subscribe("test", func(_ context.Context, ev bus.Event) {
		if ce, ok := ev.(bus.CommandExecuted); ok {
			mu.Lock()
			events = append(events, ce)
			mu.Unlock()
		}
	})

	orch, err := game.NewOrchestrator(game.OrchestratorConfig{
		Session: session, Bus: b, Progress: st, ProfileID: 1, PackID: "core-linux-basics",
	})
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}
	if err := orch.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = orch.Close(context.Background()) })

	collector, err := journal.NewCollector(sess, setupStateDir())
	if err != nil {
		t.Fatalf("journal.NewCollector: %v", err)
	}
	sink := game.NewJournalSink(collector, journal.New(st), b)

	g := &gameLevel{
		orch:       orch,
		session:    session,
		level:      level,
		sink:       sink,
		eventBus:   b,
		commandRan: make(chan struct{}, 1),
	}

	runCtx, cancel := context.WithCancel(ctx)
	ch, wait := g.StartLive(runCtx)
	if ch == nil {
		t.Error("StartLive returned a nil transition channel; it must return the live checker's own Transitions()")
	}
	if wait == nil {
		t.Fatal("StartLive returned a nil wait function")
	}
	// cancel before wait, in that order: wait blocks until StartLive's own
	// goroutines return, and they only return once ctx is done.
	defer func() {
		cancel()
		wait()
	}()

	g.CommandRan()

	waitUntil(t, 2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(events) > 0
	})

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 1 {
		t.Fatalf("got %d CommandExecuted events, want exactly 1", len(events))
	}
	if events[0].LevelID != "nav-01" {
		t.Errorf("LevelID = %q, want %q", events[0].LevelID, "nav-01")
	}
	if events[0].AttemptID != orch.AttemptID() {
		t.Errorf("AttemptID = %d, want %d (orch.AttemptID())", events[0].AttemptID, orch.AttemptID())
	}
}

// TestGameLevelStartLiveReportsATransitionWhenACommandRuns is the wire this
// commit adds end to end: a level with one cheap check, a command finishing,
// and a transition arriving on the channel StartLive returned, with no `check`
// ever typed. It is the same construction TestGameLevelCommandRanDrainsTheJournal
// uses, with one cheap objective added so there is something for the live
// checker to report.
func TestGameLevelStartLiveReportsATransitionWhenACommandRuns(t *testing.T) {
	ctx := context.Background()

	sess := &liveFakeSession{data: []byte("1755000000.000000\t0\t/home/learner\tpwd\n")}

	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "progress.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	level := &content.Level{
		ID:    "nav-01",
		Setup: content.Setup{Root: "/home/learner/quest"},
		Objectives: []content.Objective{
			{ID: "location", Text: "quest/answer.txt exists"},
		},
		Checks: []content.CheckSpec{
			{ID: "location", Type: "file_exists", OnFail: "answer.txt is missing",
				Params: map[string]any{"path": "/home/learner/quest/answer.txt"}},
		},
	}

	session, err := game.NewSession(game.Config{
		Level: level, Sess: sess, Verifier: verify.NewEngine(),
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	b := bus.New()
	orch, err := game.NewOrchestrator(game.OrchestratorConfig{
		Session: session, Bus: b, Progress: st, ProfileID: 1, PackID: "core-linux-basics",
	})
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}
	if err := orch.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = orch.Close(context.Background()) })

	collector, err := journal.NewCollector(sess, setupStateDir())
	if err != nil {
		t.Fatalf("journal.NewCollector: %v", err)
	}
	sink := game.NewJournalSink(collector, journal.New(st), b)

	g := &gameLevel{
		orch:       orch,
		session:    session,
		level:      level,
		sink:       sink,
		eventBus:   b,
		commandRan: make(chan struct{}, 1),
	}

	runCtx, cancel := context.WithCancel(ctx)
	transitions, wait := g.StartLive(runCtx)
	if transitions == nil {
		t.Fatal("StartLive returned a nil transition channel")
	}
	// cancel before wait, in that order: wait blocks until StartLive's own
	// goroutines return, and they only return once ctx is done.
	defer func() {
		cancel()
		wait()
	}()

	g.CommandRan()

	select {
	case objs := <-transitions:
		if len(objs) != 1 || objs[0].ID != "location" || objs[0].Status != verify.StatusPass {
			t.Errorf("transition = %+v, want the location objective reporting StatusPass", objs)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no transition arrived after a command ran")
	}
}

// JournalSink.Drain's contract is "before a check and once at teardown".
// Only the check half was wired, so every command after the learner's last
// check was lost: from the events table, from commands_used, and from the
// achievements that count commands. A learner who never typed check at all
// recorded nothing whatsoever.
//
// Asserted against the source, because observing it needs a sandbox: what
// matters is that Teardown drains BEFORE it closes, since Close is what
// reads the command count for the last time and the Orchestrator stops
// counting the moment it is closed.
func TestTeardownDrainsTheJournalBeforeClosingTheAttempt(t *testing.T) {
	src := readSource(t, "level_adapters.go")

	body := src[strings.Index(src, "func (g *gameLevel) Teardown("):]
	body = body[:strings.Index(body, "\n}")]

	drain := strings.Index(body, "drainJournal")
	close := strings.Index(body, "orch.Close")

	if drain < 0 {
		t.Fatal("gameLevel.Teardown does not drain the journal, so every command after the last check is lost")
	}
	if close < 0 {
		t.Fatal("gameLevel.Teardown does not close the attempt")
	}
	if drain > close {
		t.Error("Teardown closes the attempt before draining, so the last commands are counted by nothing")
	}
}

// TestLevelJournalAnswersScopeLevel is the regression test for the wiring bug
// that made every command_matched bonus in the shipped pack unreachable.
// buildLevel used to hand game.NewSession a bare journal.New(st), and
// Journal.Commands answers scope.Level with nothing at all until SetLevel
// has named a level, so a learner who solved nav-01 with `pwd >
// quest/answer.txt` earned the objective and missed the bonus that asks
// whether they used `pwd`.
//
// The assertion is deliberately made through verify.JournalReader, the
// interface a check actually holds, rather than through the concrete type:
// what broke was what a check could see, not what the journal could store.
func TestLevelJournalAnswersScopeLevel(t *testing.T) {
	ctx := context.Background()

	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "progress.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	var reader verify.JournalReader = levelJournal(ctx, st, "nav-01")

	j, ok := reader.(*journal.Journal)
	if !ok {
		t.Fatalf("levelJournal returned %T, want *journal.Journal", reader)
	}
	if err := j.Append(ctx, journal.Entry{
		Seq: 1, TS: time.Now().UTC(), LevelID: "nav-01",
		Cwd: "/home/learner", Raw: "pwd > quest/answer.txt",
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got := reader.Commands(verify.Scope{Kind: verify.ScopeLevel})
	if len(got) != 1 || got[0] != "pwd > quest/answer.txt" {
		t.Fatalf("scope level commands = %q, want [%q]", got, "pwd > quest/answer.txt")
	}
}

// TestLevelJournalExcludesAnEarlierAttempt pins the other half of the
// boundary: a command from before this level was assembled must not award
// this attempt's bonus.
func TestLevelJournalExcludesAnEarlierAttempt(t *testing.T) {
	ctx := context.Background()

	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "progress.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	earlier := journal.New(st)
	if err := earlier.Append(ctx, journal.Entry{
		Seq: 1, TS: time.Now().UTC(), LevelID: "nav-01",
		Cwd: "/home/learner", Raw: "pwd",
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	j := levelJournal(ctx, st, "nav-01")
	if got := j.Commands(verify.Scope{Kind: verify.ScopeLevel}); len(got) != 0 {
		t.Errorf("scope level commands on a fresh attempt = %q, want none", got)
	}
}

// --------------------------------------------------------------------------
// `next`, typed at the prompt inside a level
// --------------------------------------------------------------------------

// TestNextVerbAnswersFromInsideTheSandbox is the regression test for the
// fourth defect a learner hit: having passed a level, they typed `next` and
// then `shellforge next` and got "command not found" from both, with nothing
// in the pass banner to tell them otherwise.
//
// The verb resolves the real curriculum through a real store, so the answer
// names the level that actually comes after the one recorded as passed
// rather than the first level in the pack.
func TestNextVerbAnswersFromInsideTheSandbox(t *testing.T) {
	ctx := context.Background()

	pack, err := content.Embedded()
	if err != nil {
		t.Fatalf("content.Embedded: %v", err)
	}

	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "progress.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	profile, err := st.EnsureProfile(ctx, "learner")
	if err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}

	order, err := pack.Order()
	if err != nil {
		t.Fatalf("pack.Order: %v", err)
	}

	first := order[0]
	firstLevel, ok := pack.Level(first)
	if !ok {
		t.Fatalf("the pack does not hold its own first level %q", first)
	}
	if err := st.SetLevelStatus(ctx, profile.ID, pack.ID, firstLevel.ID, firstLevel.Version, store.StatusPassed); err != nil {
		t.Fatalf("SetLevelStatus: %v", err)
	}

	r := &gameResponder{
		level: firstLevel,
		pass:  &passContext{pack: pack, store: st, profileID: profile.ID, unlocks: &unlockCollector{}},
	}

	got := r.Reply(ctx, "next", "")

	if strings.Contains(got, "unknown request") {
		t.Fatalf("`next` is still an unknown verb: %q", got)
	}
	second := order[1]
	if !strings.Contains(got, second) {
		t.Errorf("`next` did not name the level after %q (want %q): %q", first, second, got)
	}
	// No sandbox behind this responder, so `next` cannot end the shell for
	// the learner and falls back to saying what to type. The automatic path
	// is TestNextArmsTheAdvanceWhenThereIsSomewhereToGo, below.
	if !strings.Contains(got, "exit") {
		t.Errorf("`next` did not tell the learner to leave the sandbox first: %q", got)
	}
	// The shim prints this verbatim into a terminal held in raw mode, where
	// a bare LF moves down without returning the carriage.
	for i := 0; i < len(got); i++ {
		if got[i] == '\n' && (i == 0 || got[i-1] != '\r') {
			t.Fatalf("byte %d is an LF with no CR before it: %q", i, got)
		}
	}
}

// TestNextVerbWithNoProgressContextStillAnswers pins the degraded path. A
// responder with no store behind it, which is every test that drives one
// without a progress database, must still tell the learner how to carry on
// rather than returning an empty reply or panicking on a nil pack.
func TestNextVerbWithNoProgressContextStillAnswers(t *testing.T) {
	r := &gameResponder{level: &content.Level{ID: "nav-01", Title: "First Contact"}}

	got := r.Reply(context.Background(), "next", "")
	if !strings.Contains(got, "exit") || !strings.Contains(got, "shellforge play") {
		t.Errorf("`next` with no progress context did not say how to carry on: %q", got)
	}
}

// advanceFixture is a responder with everything `next` needs to carry the
// learner onward: a real pack, a real store, an orchestrator that has been
// through a real check, and a fake sandbox session to record the touch
// against.
type advanceFixture struct {
	responder *gameResponder
	session   *fakeSession
	flag      *atomic.Bool
	order     []string
}

// newAdvanceFixture builds one. passed decides whether the level under it
// has been passed in this session, which is the one thing `next` will not
// carry a learner past.
func newAdvanceFixture(t *testing.T, passed bool) advanceFixture {
	t.Helper()
	ctx := context.Background()

	pack, err := content.Embedded()
	if err != nil {
		t.Fatalf("content.Embedded: %v", err)
	}
	order, err := pack.Order()
	if err != nil {
		t.Fatalf("pack.Order: %v", err)
	}

	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "progress.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	profile, err := st.EnsureProfile(ctx, "learner")
	if err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}

	level, ok := pack.Level(order[0])
	if !ok {
		t.Fatalf("the pack does not hold its own first level %q", order[0])
	}
	if err := st.SetLevelStatus(ctx, profile.ID, pack.ID, level.ID, level.Version, store.StatusPassed); err != nil {
		t.Fatalf("SetLevelStatus: %v", err)
	}

	// Setup resolves the level root with readlink and refuses anything that
	// does not echo the operand back, so a bare fakeSession cannot even
	// start a level. See sandboxStub.
	sess := &fakeSession{exec: sandboxStub}
	orch := orchestratorThatPassed(t, ctx, level, sess, st, passed)

	var flag atomic.Bool
	return advanceFixture{
		responder: &gameResponder{
			level:    level,
			orch:     orch,
			sess:     sess,
			stateDir: testStateDir,
			advance:  &flag,
			pass: &passContext{
				pack:      pack,
				store:     st,
				profileID: profile.ID,
				unlocks:   &unlockCollector{},
				advance:   true,
			},
		},
		session: sess,
		flag:    &flag,
		order:   order,
	}
}

const testStateDir = "/home/learner/.shellforge"

// touchedAdvanceSentinel reports whether `next` dropped the file the
// learner's shell watches for. Matched on the exact argv rather than on the
// command name: setup writes its own SETUP_OK sentinel with touch, so
// "something ran touch" says nothing.
func (f advanceFixture) touchedAdvanceSentinel() bool {
	want := strings.Join([]string{"touch", "--", testStateDir + "/" + advanceSentinel}, " ")
	for _, argv := range f.session.argvs {
		if strings.Join(argv, " ") == want {
			return true
		}
	}
	return false
}

// orchestratorThatPassed returns a started Orchestrator whose Passed reports
// want.
//
// Passing is done by running a real check against a session that answers
// every probe the way a solved level would, because there is no other way
// in: Passed is deliberately not settable from outside internal/game, on the
// grounds that nothing but a real check may decide a level was passed.
func orchestratorThatPassed(t *testing.T, ctx context.Context, level *content.Level, sess *fakeSession, st *store.Store, want bool) *game.Orchestrator {
	t.Helper()

	session, err := game.NewSession(game.Config{
		Level: level, Sess: sess, Verifier: verify.NewEngine(),
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	orch, err := game.NewOrchestrator(game.OrchestratorConfig{
		Session: session, Bus: bus.New(), Progress: st, ProfileID: 1, PackID: "core-linux-basics",
	})
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}
	if err := orch.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = orch.Close(context.Background()) })

	if !want {
		sess.argvs = nil
		return orch
	}

	sess.exec = solvedStub
	if _, err := orch.Check(ctx); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !orch.Passed() {
		t.Fatalf("the fixture did not pass %q, so it cannot exercise the path that needs a pass", level.ID)
	}

	// Back to the plain stub, and the argv log cleared, so what the tests
	// below assert on is what `next` did and not what the fixture did to
	// get here.
	sess.exec = sandboxStub
	sess.argvs = nil
	return orch
}

// sandboxStub answers the two probes anything running against a sandbox
// makes before it can do its own work: setup resolves the level root with
// `readlink -m --` and refuses unless the answer echoes the operand back,
// and every filesystem check resolves state with `stat -c`.
func sandboxStub(argv []string, _ runtime.ExecOpts) (runtime.ExecResult, error) {
	switch {
	case len(argv) == 0:
		return runtime.ExecResult{}, nil
	case argv[0] == "readlink":
		return runtime.ExecResult{Stdout: []byte(argv[len(argv)-1] + "\n")}, nil
	case argv[0] == "stat":
		return runtime.ExecResult{Stdout: []byte("regular file|644|learner|learner\n")}, nil
	}
	return runtime.ExecResult{}, nil
}

// solvedStub is sandboxStub plus an answer for the content probes, so a real
// check over the real first level of the pack reports every objective
// passed.
func solvedStub(argv []string, opts runtime.ExecOpts) (runtime.ExecResult, error) {
	if len(argv) > 0 && (argv[0] == "readlink" || argv[0] == "stat") {
		return sandboxStub(argv, opts)
	}
	return runtime.ExecResult{Stdout: []byte("/home/learner\n")}, nil
}

// TestNextArmsTheAdvanceWhenThereIsSomewhereToGo is the regression test for
// what a learner asked for once the first four defects were fixed: `next`
// told them what to type instead of doing it.
//
// Two things have to happen. The sentinel the learner's shell watches for is
// dropped, and the host records that this was asked for so it does not ask
// again. The sentinel goes first, before the reply is written, and that
// order is what makes it race free rather than usually right: the shell
// looks for the file only after this reply has been printed.
func TestNextArmsTheAdvanceWhenThereIsSomewhereToGo(t *testing.T) {
	f := newAdvanceFixture(t, true)

	got := f.responder.Reply(context.Background(), "next", "")

	if !f.flag.Load() {
		t.Error("`next` did not record that the learner asked to carry on, so play would ask them again")
	}
	if !strings.Contains(got, "Loading") {
		t.Errorf("`next` did not say which level is loading: %q", got)
	}
	if !strings.Contains(got, f.order[1]) {
		t.Errorf("`next` did not name the level it is loading (want %q): %q", f.order[1], got)
	}
	if strings.Contains(got, "shellforge play") {
		t.Errorf("`next` still told the learner to run a command it was about to make unnecessary: %q", got)
	}

	if !f.touchedAdvanceSentinel() {
		t.Errorf("the advance sentinel was never dropped: argv calls were %v", f.session.argvs)
	}
}

// TestNextRefusesToEndAnUnsolvedLevel is the rule that keeps `next` from
// costing a learner their work. Somewhere to go is not the same as being
// ready to go there, and a learner halfway through an unsolved level who is
// thrown out of it has lost a world they never agreed to lose.
func TestNextRefusesToEndAnUnsolvedLevel(t *testing.T) {
	f := newAdvanceFixture(t, false)

	got := f.responder.Reply(context.Background(), "next", "")

	if f.flag.Load() {
		t.Error("`next` armed the advance on a level that was never passed")
	}
	if f.touchedAdvanceSentinel() {
		t.Error("`next` dropped the sentinel on a level that was never passed, so the shell would end under a learner mid-task")
	}
	if !strings.Contains(got, "exit") {
		t.Errorf("`next` did not say what to do instead: %q", got)
	}
}

// TestNextFallsBackWhenTheSentinelCannotBeWritten pins the degradation, and
// pins it in the safe direction. If the sentinel did not land, the shell is
// not going to end, so recording the advance anyway would leave play
// carrying on from a level whose shell is still up. Falling back to the
// instructions is both correct and true: they work whether or not the
// automatic path did.
func TestNextFallsBackWhenTheSentinelCannotBeWritten(t *testing.T) {
	f := newAdvanceFixture(t, true)
	f.session.exec = func(argv []string, _ runtime.ExecOpts) (runtime.ExecResult, error) {
		if argv[0] == "touch" {
			return runtime.ExecResult{ExitCode: 1}, nil
		}
		return runtime.ExecResult{}, nil
	}

	got := f.responder.Reply(context.Background(), "next", "")

	if f.flag.Load() {
		t.Error("`next` recorded an advance whose sentinel never landed, so play would carry on from a level still holding a shell")
	}
	if !strings.Contains(got, "exit") {
		t.Errorf("`next` did not fall back to telling the learner what to type: %q", got)
	}
}
