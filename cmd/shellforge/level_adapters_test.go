package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
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
		commandRan: make(chan struct{}, 1),
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if ch := g.StartLive(runCtx); ch != nil {
		t.Errorf("StartLive returned a non-nil transition channel; this commit only wires the drain")
	}

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
