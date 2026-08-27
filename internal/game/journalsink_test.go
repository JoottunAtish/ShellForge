package game

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/game/bus"
	"github.com/JoottunAtish/ShellForge/internal/journal"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/store"
)

// --- journalFakeSession: a runtime.Session that only serves journal.tsv. ----
//
// session_test.go's fakeSession answers Exec, which is what a game.Session
// needs; a JournalSink needs the other end, a PullFile that returns canned
// TSV bytes, so this is a separate fake rather than another mode bolted onto
// that one.

type journalFakeSession struct {
	mu   sync.Mutex
	data []byte
	err  error
}

func (f *journalFakeSession) PullFile(context.Context, string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	out := make([]byte, len(f.data))
	copy(out, f.data)
	return out, nil
}

func (f *journalFakeSession) Exec(context.Context, []string, runtime.ExecOpts) (runtime.ExecResult, error) {
	panic("journalFakeSession: a JournalSink must never call Exec")
}

func (f *journalFakeSession) Attach(context.Context, runtime.AttachOpts) (runtime.PTY, error) {
	panic("journalFakeSession: a JournalSink must never call Attach")
}

func (f *journalFakeSession) PushFiles(context.Context, runtime.FileManifest) error {
	panic("journalFakeSession: a JournalSink must never call PushFiles")
}

func (f *journalFakeSession) Close() error {
	panic("journalFakeSession: a JournalSink must never call Close")
}

func (f *journalFakeSession) appendContent(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data = append(f.data, s...)
}

func (f *journalFakeSession) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// sinkTSVLine renders one record exactly as images/rc/instrument.bash writes
// it: EPOCHREALTIME, exit code, PWD, then the command line, tab separated.
func sinkTSVLine(epoch string, exit int, cwd, raw string) string {
	return fmt.Sprintf("%s\t%d\t%s\t%s\n", epoch, exit, cwd, raw)
}

// newTestSink wires a JournalSink over a fake sandbox session, a real
// Journal backed by a fresh store, and a recording bus.
func newTestSink(t *testing.T, content string) (*JournalSink, *journalFakeSession, *journal.Journal, *recordingBus, *store.Store) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "progress.db")
	s, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	sess := &journalFakeSession{data: []byte(content)}
	j := journal.New(s)
	b, rec := newRecordingBus()

	return NewJournalSink(journal.NewCollector(sess, "/home/learner/.shellforge"), j, b), sess, j, rec, s
}

func commandEvents(rec *recordingBus) []bus.CommandExecuted {
	var out []bus.CommandExecuted
	for _, ev := range rec.snapshot() {
		if ce, ok := ev.(bus.CommandExecuted); ok {
			out = append(out, ce)
		}
	}
	return out
}

func fiveCommands() string {
	return sinkTSVLine("1755000000.000000", 0, "/home/learner", "pwd") +
		sinkTSVLine("1755000001.000000", 0, "/home/learner", "ls -la") +
		sinkTSVLine("1755000002.000000", 1, "/home/learner", "cat missing.txt") +
		sinkTSVLine("1755000003.000000", 0, "/home/learner/logs", "cd logs") +
		sinkTSVLine("1755000004.000000", 0, "/home/learner/logs", "grep -c ERROR app-1.log")
}

func TestDrainAppendsEveryCommandAndPublishesOneEventEach(t *testing.T) {
	sink, _, j, rec, _ := newTestSink(t, fiveCommands())
	ctx := context.Background()

	if err := sink.Drain(ctx, "pipe-05", 77); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	rows, err := j.Level(ctx, "pipe-05")
	if err != nil {
		t.Fatalf("Level: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("events table holds %d rows for pipe-05, want 5", len(rows))
	}

	wantRaw := []string{"pwd", "ls -la", "cat missing.txt", "cd logs", "grep -c ERROR app-1.log"}
	wantExit := []int{0, 0, 1, 0, 0}
	wantCwd := []string{"/home/learner", "/home/learner", "/home/learner", "/home/learner/logs", "/home/learner/logs"}
	for i, e := range rows {
		if e.Raw != wantRaw[i] {
			t.Errorf("row %d Raw = %q, want %q", i, e.Raw, wantRaw[i])
		}
		if e.Exit != wantExit[i] {
			t.Errorf("row %d Exit = %d, want %d", i, e.Exit, wantExit[i])
		}
		if e.Cwd != wantCwd[i] {
			t.Errorf("row %d Cwd = %q, want %q", i, e.Cwd, wantCwd[i])
		}
		if e.LevelID != "pipe-05" {
			t.Errorf("row %d LevelID = %q, want pipe-05", i, e.LevelID)
		}
	}

	events := commandEvents(rec)
	if len(events) != 5 {
		t.Fatalf("bus carried %d CommandExecuted events, want 5", len(events))
	}
	for i, ev := range events {
		if ev.Raw != wantRaw[i] {
			t.Errorf("event %d Raw = %q, want %q: events must arrive in journal order", i, ev.Raw, wantRaw[i])
		}
		if ev.AttemptID != 77 {
			t.Errorf("event %d AttemptID = %d, want 77", i, ev.AttemptID)
		}
		if ev.LevelID != "pipe-05" {
			t.Errorf("event %d LevelID = %q, want pipe-05", i, ev.LevelID)
		}
		if ev.ExitCode != wantExit[i] {
			t.Errorf("event %d ExitCode = %d, want %d", i, ev.ExitCode, wantExit[i])
		}
		if ev.Cwd != wantCwd[i] {
			t.Errorf("event %d Cwd = %q, want %q", i, ev.Cwd, wantCwd[i])
		}
		if ev.Seq != rows[i].Seq {
			t.Errorf("event %d Seq = %d, want %d (the seq actually recorded)", i, ev.Seq, rows[i].Seq)
		}
		if ev.At.IsZero() {
			t.Errorf("event %d At is the zero time, want the command's own timestamp", i)
		}
	}
}

func TestDrainIsIncrementalAcrossThreeCallsWithNoDuplicatesAndNoGaps(t *testing.T) {
	sink, sess, j, rec, _ := newTestSink(t, sinkTSVLine("1755000000.000000", 0, "/home/learner", "pwd"))
	ctx := context.Background()

	if err := sink.Drain(ctx, "nav-01", 1); err != nil {
		t.Fatalf("first Drain: %v", err)
	}
	sess.appendContent(
		sinkTSVLine("1755000001.000000", 0, "/home/learner", "ls") +
			sinkTSVLine("1755000002.000000", 0, "/home/learner", "cd docs"),
	)
	if err := sink.Drain(ctx, "nav-01", 1); err != nil {
		t.Fatalf("second Drain: %v", err)
	}
	// A third Drain with nothing new must append nothing and publish nothing.
	if err := sink.Drain(ctx, "nav-01", 1); err != nil {
		t.Fatalf("third Drain: %v", err)
	}

	rows, err := j.Level(ctx, "nav-01")
	if err != nil {
		t.Fatalf("Level: %v", err)
	}
	var got []string
	for _, e := range rows {
		got = append(got, e.Raw)
	}
	want := []string{"pwd", "ls", "cd docs"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("events table holds %v, want %v exactly once each", got, want)
	}

	if n := len(commandEvents(rec)); n != 3 {
		t.Errorf("bus carried %d CommandExecuted events across three Drains, want 3", n)
	}
}

func TestDrainDegradesToZeroCommandsWhenTheJournalCannotBeRead(t *testing.T) {
	sink, sess, j, rec, _ := newTestSink(t, "")
	sess.setErr(errors.New("docker cp (pull) /home/learner/.shellforge/journal.tsv exited 1"))
	ctx := context.Background()

	if err := sink.Drain(ctx, "nav-01", 3); err != nil {
		t.Fatalf("Drain over an unreadable journal returned %v; it must never fail the level or the check", err)
	}

	rows, err := j.Level(ctx, "nav-01")
	if err != nil {
		t.Fatalf("Level: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("events table holds %d rows, want 0", len(rows))
	}
	if n := len(commandEvents(rec)); n != 0 {
		t.Errorf("bus carried %d events, want 0", n)
	}
}

func TestDrainDegradesToZeroCommandsWhenThereIsNoJournalYet(t *testing.T) {
	sink, _, j, rec, _ := newTestSink(t, "")
	ctx := context.Background()

	if err := sink.Drain(ctx, "nav-01", 3); err != nil {
		t.Fatalf("Drain over an empty journal returned %v, want no error", err)
	}
	rows, err := j.Level(ctx, "nav-01")
	if err != nil {
		t.Fatalf("Level: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("events table holds %d rows, want 0", len(rows))
	}
	if n := len(commandEvents(rec)); n != 0 {
		t.Errorf("bus carried %d events, want 0", n)
	}
}

func TestDrainSkipsAMalformedRecordAndKeepsTheSurroundingOnes(t *testing.T) {
	content := sinkTSVLine("1755000000.000000", 0, "/home/learner", "pwd") +
		strings.Repeat("y\n", 500) +
		sinkTSVLine("1755000001.000000", 0, "/home/learner", "whoami") +
		"1755000002.000000\t0\t/home/learner\tls -l" // truncated, mid-write
	sink, _, j, rec, _ := newTestSink(t, content)
	ctx := context.Background()

	if err := sink.Drain(ctx, "nav-01", 5); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	rows, err := j.Level(ctx, "nav-01")
	if err != nil {
		t.Fatalf("Level: %v", err)
	}
	var got []string
	for _, e := range rows {
		got = append(got, e.Raw)
	}
	if strings.Join(got, "|") != "pwd|whoami" {
		t.Errorf("events table holds %v, want [pwd whoami]", got)
	}
	if n := len(commandEvents(rec)); n != 2 {
		t.Errorf("bus carried %d events, want 2", n)
	}
}

func TestDrainKeepsCommandTextOutOfTheErrorItReturns(t *testing.T) {
	sink, _, _, rec, s := newTestSink(t, sinkTSVLine("1755000000.000000", 0, "/home/learner", "curl -u admin:hunter2 https://example.com"))

	// Closing the store is the one reliable way to make Append fail against
	// a real Journal, which is what NewJournalSink takes.
	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	err := sink.Drain(context.Background(), "nav-01", 9)
	if err == nil {
		t.Fatal("Drain returned no error when the store could not be written")
	}
	if strings.Contains(err.Error(), "hunter2") || strings.Contains(err.Error(), "curl -u") {
		t.Errorf("Drain's error carries command text: %s", err)
	}
	if n := len(commandEvents(rec)); n != 0 {
		t.Errorf("bus carried %d events for an entry that was never appended, want 0", n)
	}
}

// TestJournalSinkNeverFormatsAnEntry is the source level half of the privacy
// rule, in the style of internal/journal/privacy_test.go. A journal.Entry
// reached through an unexported field cannot redact itself (see Entry's own
// doc comment), so this file must never hand an Entry, or its Raw, to
// anything that formats.
func TestJournalSinkNeverFormatsAnEntry(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "journalsink.go", nil, 0)
	if err != nil {
		t.Fatalf("parse journalsink.go: %v", err)
	}

	safe := map[string]bool{"Append": true, "Publish": true, "len": true}

	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := ""
		switch fn := call.Fun.(type) {
		case *ast.SelectorExpr:
			name = fn.Sel.Name
			if x, ok := fn.X.(*ast.Ident); ok && x.Name == "fmt" && name != "Errorf" {
				t.Errorf("journalsink.go calls fmt.%s; it may only call fmt.Errorf", name)
			}
		case *ast.Ident:
			name = fn.Name
		}
		if safe[name] {
			return true
		}
		for _, arg := range call.Args {
			sel, ok := arg.(*ast.SelectorExpr)
			if ok && sel.Sel.Name == "Raw" {
				t.Errorf("journalsink.go passes .Raw to %s, outside the append and publish paths", name)
			}
			if ok && (sel.Sel.Name == "Entry" || sel.Sel.Name == "entry") {
				t.Errorf("journalsink.go passes an Entry to %s", name)
			}
		}
		return true
	})

	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if path == "log" || path == "log/slog" {
			t.Errorf("journalsink.go imports %q; a journal Entry must never reach a log", path)
		}
	}
}
