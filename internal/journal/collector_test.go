package journal

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

// --- fakeSession: a runtime.Session whose only real method is PullFile. -----
//
// Collector reads one file out of the sandbox and does nothing else, so only
// PullFile has a body. The other four panic rather than returning a zero
// value, so a Collector that ever reaches for a second channel fails loudly
// in the test that let it, instead of passing quietly.

type fakeSession struct {
	mu    sync.Mutex
	data  []byte
	err   error
	pulls int
	paths []string
}

func (f *fakeSession) PullFile(_ context.Context, p string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pulls++
	f.paths = append(f.paths, p)
	if f.err != nil {
		return nil, f.err
	}
	out := make([]byte, len(f.data))
	copy(out, f.data)
	return out, nil
}

func (f *fakeSession) Exec(context.Context, []string, runtime.ExecOpts) (runtime.ExecResult, error) {
	panic("fakeSession: Collector must never call Exec")
}

func (f *fakeSession) Attach(context.Context, runtime.AttachOpts) (runtime.PTY, error) {
	panic("fakeSession: Collector must never call Attach")
}

func (f *fakeSession) PushFiles(context.Context, runtime.FileManifest) error {
	panic("fakeSession: Collector must never call PushFiles")
}

func (f *fakeSession) Close() error {
	panic("fakeSession: Collector must never call Close")
}

func (f *fakeSession) setContent(b []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data = b
}

func (f *fakeSession) appendContent(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data = append(f.data, s...)
}

func (f *fakeSession) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *fakeSession) pullCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pulls
}

// snapshot returns a copy of the file's current content, so a test can size
// it without racing appendContent.
func (f *fakeSession) snapshot() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]byte, len(f.data))
	copy(out, f.data)
	return out
}

func (f *fakeSession) pulledPaths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.paths))
	copy(out, f.paths)
	return out
}

// tsvLine renders one record exactly as images/rc/instrument.bash writes it:
// EPOCHREALTIME, exit code, PWD, then the command line, tab separated, with a
// trailing newline.
func tsvLine(epoch string, exit int, cwd, raw string) string {
	return fmt.Sprintf("%s\t%d\t%s\t%s\n", epoch, exit, cwd, raw)
}

// newTestCollector returns a Collector over a fakeSession preloaded with
// content, using the state directory the sandbox image actually sets.
func newTestCollector(t *testing.T, content string) (*Collector, *fakeSession) {
	t.Helper()
	f := &fakeSession{data: []byte(content)}
	c, err := NewCollector(f, "/home/learner/.shellforge")
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}
	return c, f
}

func TestSinceReturnsEveryRecordStampedWithTheLevelID(t *testing.T) {
	content := tsvLine("1755000000.500000", 0, "/home/learner", "ls -la") +
		tsvLine("1755000001.250000", 2, "/home/learner", "grep -r 'error' logs/") +
		tsvLine("1755000002.000000", 0, "/home/learner/logs", "cd logs")

	c, _ := newTestCollector(t, content)

	got, err := c.Since(context.Background(), "nav-01")
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("Since returned %d entries, want 3", len(got))
	}

	wantRaw := []string{"ls -la", "grep -r 'error' logs/", "cd logs"}
	wantExit := []int{0, 2, 0}
	wantCwd := []string{"/home/learner", "/home/learner", "/home/learner/logs"}
	for i, e := range got {
		if e.Raw != wantRaw[i] {
			t.Errorf("entry %d Raw = %q, want %q", i, e.Raw, wantRaw[i])
		}
		if e.Exit != wantExit[i] {
			t.Errorf("entry %d Exit = %d, want %d", i, e.Exit, wantExit[i])
		}
		if e.Cwd != wantCwd[i] {
			t.Errorf("entry %d Cwd = %q, want %q", i, e.Cwd, wantCwd[i])
		}
		if e.LevelID != "nav-01" {
			t.Errorf("entry %d LevelID = %q, want nav-01", i, e.LevelID)
		}
		if e.Seq != int64(i+1) {
			t.Errorf("entry %d Seq = %d, want %d", i, e.Seq, i+1)
		}
	}

	wantTS := time.Unix(1755000000, 500000*1000).UTC()
	if !got[0].TS.Equal(wantTS) {
		t.Errorf("entry 0 TS = %s, want %s", got[0].TS, wantTS)
	}
}

func TestSinceIsIncrementalAcrossCallsWithNoDuplicatesAndNoGaps(t *testing.T) {
	c, f := newTestCollector(t,
		tsvLine("1755000000.000000", 0, "/home/learner", "pwd")+
			tsvLine("1755000001.000000", 0, "/home/learner", "ls"),
	)
	ctx := context.Background()

	first, err := c.Since(ctx, "nav-01")
	if err != nil {
		t.Fatalf("first Since: %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("first Since returned %d entries, want 2", len(first))
	}

	// Nothing new since: the same file must yield nothing a second time.
	second, err := c.Since(ctx, "nav-01")
	if err != nil {
		t.Fatalf("second Since: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("second Since returned %d entries, want 0", len(second))
	}

	f.appendContent(
		tsvLine("1755000002.000000", 0, "/home/learner", "cat notes.txt") +
			tsvLine("1755000003.000000", 1, "/home/learner", "cat missing.txt"),
	)

	third, err := c.Since(ctx, "nav-01")
	if err != nil {
		t.Fatalf("third Since: %v", err)
	}
	if len(third) != 2 {
		t.Fatalf("third Since returned %d entries, want 2", len(third))
	}

	var raws []string
	for _, e := range append(append([]Entry{}, first...), third...) {
		raws = append(raws, e.Raw)
	}
	want := []string{"pwd", "ls", "cat notes.txt", "cat missing.txt"}
	if strings.Join(raws, "|") != strings.Join(want, "|") {
		t.Errorf("commands across three calls = %v, want %v", raws, want)
	}

	for i, e := range append(append([]Entry{}, first...), third...) {
		if e.Seq != int64(i+1) {
			t.Errorf("entry %d Seq = %d, want %d: Seq must be monotonic across calls", i, e.Seq, i+1)
		}
	}
}

func TestSinceSkipsATruncatedFinalLineAndReturnsItOnceComplete(t *testing.T) {
	c, f := newTestCollector(t,
		tsvLine("1755000000.000000", 0, "/home/learner", "pwd")+
			"1755000001.000000\t0\t/home/learner\tls -l", // no terminator: mid-write
	)
	ctx := context.Background()

	got, err := c.Since(ctx, "nav-01")
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Since returned %d entries, want 1 (the truncated final line must not be returned)", len(got))
	}
	if got[0].Raw != "pwd" {
		t.Errorf("Raw = %q, want pwd", got[0].Raw)
	}

	// The learner's shell finishes the write.
	f.appendContent("\n")

	got, err = c.Since(ctx, "nav-01")
	if err != nil {
		t.Fatalf("second Since: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("second Since returned %d entries, want 1", len(got))
	}
	if got[0].Raw != "ls -l" {
		t.Errorf("Raw = %q, want ls -l", got[0].Raw)
	}
	if got[0].Seq != 2 {
		t.Errorf("Seq = %d, want 2", got[0].Seq)
	}
}

func TestSinceSkipsAMalformedMiddleLineAndKeepsTheSurroundingRecords(t *testing.T) {
	content := tsvLine("1755000000.000000", 0, "/home/learner", "pwd") +
		"this is not a journal record at all\n" +
		"1755000001.000000\tnot-an-exit-code\t/home/learner\tls\n" +
		tsvLine("1755000002.000000", 0, "/home/learner", "whoami")

	c, _ := newTestCollector(t, content)

	got, err := c.Since(context.Background(), "nav-01")
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Since returned %d entries, want 2", len(got))
	}
	if got[0].Raw != "pwd" || got[1].Raw != "whoami" {
		t.Errorf("commands = [%q %q], want [pwd whoami]", got[0].Raw, got[1].Raw)
	}
}

func TestSinceSkipsAnOverLongLineAndKeepsTheSurroundingRecords(t *testing.T) {
	huge := strings.Repeat("x", maxTSVLine+1)
	content := tsvLine("1755000000.000000", 0, "/home/learner", "pwd") +
		tsvLine("1755000001.000000", 0, "/home/learner", huge) +
		tsvLine("1755000002.000000", 0, "/home/learner", "whoami")

	c, _ := newTestCollector(t, content)

	got, err := c.Since(context.Background(), "nav-01")
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Since returned %d entries, want 2", len(got))
	}
	if got[0].Raw != "pwd" || got[1].Raw != "whoami" {
		t.Errorf("commands = [%q %q], want [pwd whoami]", got[0].Raw, got[1].Raw)
	}
}

func TestSinceReportsNoCommandsAndNoErrorWhenTheJournalDoesNotExist(t *testing.T) {
	c, f := newTestCollector(t, "")
	f.setErr(fmt.Errorf("open %s: %w", "/home/learner/.shellforge/journal.tsv", fs.ErrNotExist))

	got, err := c.Since(context.Background(), "nav-01")
	if err != nil {
		t.Fatalf("Since on an absent journal returned %v, want no error", err)
	}
	if len(got) != 0 {
		t.Fatalf("Since returned %d entries, want 0", len(got))
	}
	if f.pullCount() != 1 {
		t.Errorf("PullFile ran %d times, want 1: Since must still have tried to read the journal", f.pullCount())
	}
}

func TestSinceReportsAReadErrorWithNoCommandTextInIt(t *testing.T) {
	c, f := newTestCollector(t, "")
	f.setErr(errors.New("docker cp (pull) /home/learner/.shellforge/journal.tsv exited 1"))

	got, err := c.Since(context.Background(), "nav-01")
	if err == nil {
		t.Fatal("Since on an unreadable journal returned no error, want one")
	}
	if len(got) != 0 {
		t.Fatalf("Since returned %d entries alongside an error, want 0", len(got))
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("error text carries command text: %s", err)
	}
}

func TestSinceRecoversOnceTheJournalBecomesReadableAgain(t *testing.T) {
	c, f := newTestCollector(t, tsvLine("1755000000.000000", 0, "/home/learner", "pwd"))
	ctx := context.Background()

	f.setErr(errors.New("transient backend failure"))
	if _, err := c.Since(ctx, "nav-01"); err == nil {
		t.Fatal("Since returned no error while the journal was unreadable")
	}

	f.setErr(nil)
	got, err := c.Since(ctx, "nav-01")
	if err != nil {
		t.Fatalf("Since after recovery: %v", err)
	}
	if len(got) != 1 || got[0].Raw != "pwd" {
		t.Fatalf("Since after recovery returned %v, want the one pwd record", got)
	}
}

func TestSinceReadsJournalTSVUnderTheStateDirectoryWithSlashSeparators(t *testing.T) {
	c, f := newTestCollector(t, "")

	if _, err := c.Since(context.Background(), "nav-01"); err != nil {
		t.Fatalf("Since: %v", err)
	}

	paths := f.pulledPaths()
	if len(paths) != 1 {
		t.Fatalf("PullFile called %d times, want exactly 1", len(paths))
	}
	if paths[0] != "/home/learner/.shellforge/journal.tsv" {
		t.Errorf("pulled %q, want /home/learner/.shellforge/journal.tsv", paths[0])
	}
}

func TestSincePullsTheJournalExactlyOncePerCall(t *testing.T) {
	c, f := newTestCollector(t, tsvLine("1755000000.000000", 0, "/home/learner", "pwd"))
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		if _, err := c.Since(ctx, "nav-01"); err != nil {
			t.Fatalf("Since call %d: %v", i, err)
		}
		if got := f.pullCount(); got != i {
			t.Errorf("after %d calls, PullFile ran %d times, want %d", i, got, i)
		}
	}
}

func TestSinceLeavesUsedTabAndUsedHistoryFalse(t *testing.T) {
	c, _ := newTestCollector(t, tsvLine("1755000000.000000", 0, "/home/learner", "ls -l"))

	got, err := c.Since(context.Background(), "nav-01")
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Since returned %d entries, want 1", len(got))
	}
	if got[0].UsedTab {
		t.Error("UsedTab = true; the TSV has no tab-tap column, so a collected entry must leave it false")
	}
	if got[0].UsedHistory {
		t.Error("UsedHistory = true; the TSV has no history column, so a collected entry must leave it false")
	}
	if got[0].DurationMS != 0 {
		t.Errorf("DurationMS = %d, want 0; the TSV has no duration field", got[0].DurationMS)
	}
}

// manyRecords builds a journal.tsv holding n well formed records.
func manyRecords(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(tsvLine(fmt.Sprintf("17550000%02d.000000", i%100), 0, "/home/learner", fmt.Sprintf("echo %d", i)))
	}
	return b.String()
}

func TestSinceBoundsHowManyRecordsItReturnsInOneCall(t *testing.T) {
	const extra = 7
	c, _ := newTestCollector(t, manyRecords(2*maxRecordsPerSince+extra))
	ctx := context.Background()

	want := []int{maxRecordsPerSince, maxRecordsPerSince, extra, 0}
	var seen []string
	for i, wantN := range want {
		got, err := c.Since(ctx, "nav-01")
		if err != nil {
			t.Fatalf("Since call %d: %v", i+1, err)
		}
		if len(got) != wantN {
			t.Fatalf("Since call %d returned %d entries, want %d", i+1, len(got), wantN)
		}
		for _, e := range got {
			seen = append(seen, e.Raw)
		}
	}

	if len(seen) != 2*maxRecordsPerSince+extra {
		t.Fatalf("collected %d commands in total, want %d", len(seen), 2*maxRecordsPerSince+extra)
	}
	for i, raw := range seen {
		if want := fmt.Sprintf("echo %d", i); raw != want {
			t.Fatalf("command %d = %q, want %q: the bound must not drop or reorder a record", i, raw, want)
			break
		}
	}
}

// TestSinceWorkPerCallDoesNotGrowWithTheFileSize is the memory half of the
// bound. One Since call over a journal ten times larger must not return, and
// therefore must not have parsed, ten times as many records: the per-call
// cost is fixed by maxRecordsPerSince, not by how much a learner appended
// with something like `yes >> journal.tsv`.
func TestSinceWorkPerCallDoesNotGrowWithTheFileSize(t *testing.T) {
	ctx := context.Background()

	small, _ := newTestCollector(t, manyRecords(3*maxRecordsPerSince))
	large, _ := newTestCollector(t, manyRecords(30*maxRecordsPerSince))

	gotSmall, err := small.Since(ctx, "nav-01")
	if err != nil {
		t.Fatalf("Since over the smaller journal: %v", err)
	}
	gotLarge, err := large.Since(ctx, "nav-01")
	if err != nil {
		t.Fatalf("Since over the larger journal: %v", err)
	}

	if len(gotSmall) != maxRecordsPerSince {
		t.Errorf("smaller journal yielded %d entries in one call, want %d", len(gotSmall), maxRecordsPerSince)
	}
	if len(gotLarge) != len(gotSmall) {
		t.Errorf("a journal ten times larger yielded %d entries in one call and the smaller one yielded %d; one call's work must not grow with the file",
			len(gotLarge), len(gotSmall))
	}
}

func TestSinceReturnsNothingFromAJournalOfNothingButGarbage(t *testing.T) {
	// What `yes >> journal.tsv` produces, followed by one real record, so
	// this asserts the garbage is skipped rather than that nothing at all
	// came back.
	c, f := newTestCollector(t, strings.Repeat("y\n", 100000)+
		tsvLine("1755000000.000000", 0, "/home/learner", "pwd"))

	var got []Entry
	for i := 0; i < 200; i++ {
		batch, err := c.Since(context.Background(), "nav-01")
		if err != nil {
			t.Fatalf("Since call %d: %v", i+1, err)
		}
		got = append(got, batch...)
	}
	if len(got) != 1 {
		t.Fatalf("Since returned %d entries from a file of garbage plus one record, want 1", len(got))
	}
	if got[0].Raw != "pwd" {
		t.Errorf("Raw = %q, want pwd", got[0].Raw)
	}
	if f.pullCount() != 200 {
		t.Errorf("PullFile ran %d times, want 200", f.pullCount())
	}
}

func TestSinceReturnsNothingWhenTheJournalShrinksUnderIt(t *testing.T) {
	c, f := newTestCollector(t,
		tsvLine("1755000000.000000", 0, "/home/learner", "pwd")+
			tsvLine("1755000001.000000", 0, "/home/learner", "ls"),
	)
	ctx := context.Background()

	if got, err := c.Since(ctx, "nav-01"); err != nil || len(got) != 2 {
		t.Fatalf("first Since = (%d entries, %v), want (2, nil)", len(got), err)
	}

	// A learner can truncate their own journal. Nothing may re-emit a
	// command already recorded, and nothing may panic slicing past the end.
	f.setContent([]byte(tsvLine("1755000002.000000", 0, "/home/learner", "pwd")))

	got, err := c.Since(ctx, "nav-01")
	if err != nil {
		t.Fatalf("Since after truncation: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Since after truncation returned %d entries, want 0", len(got))
	}
}

// assertLongerThan fails when the regrown journal did not reach past the byte
// count an earlier call had already consumed. A test that means to exercise a
// stale offset landing INSIDE the new content proves nothing if the new
// content stops short of it.
func assertLongerThan(t *testing.T, got, floor int) {
	t.Helper()
	if got <= floor {
		t.Fatalf("the regrown journal is %d bytes and the first call consumed %d; it must be longer, or the stale offset lands past the end of the file and this test exercises the wrong case", got, floor)
	}
}

// TestSinceResyncsFromTheStartWhenTheJournalRegrowsAfterAShrink is the other
// half of the truncation contract, and the half that was missing: asserting
// that the call after a shrink returns nothing says nothing about the calls
// after that one.
//
// A stale offset survives a shrink as a byte count into content that no
// longer exists. Once the file grows back past it, reading from it lands in
// the middle of a record: the fragment before the next '\n' is handed to
// ReadTSV as if it were a whole line, so the command it belongs to is lost
// and, given four tab separated fields in the wrong places, a garbled row
// takes its place. The regrown content here is deliberately longer than the
// content the first call consumed, so the stale offset is inside it rather
// than past its end, which is the case that corrupts rather than the case
// that merely stalls.
func TestSinceResyncsFromTheStartWhenTheJournalRegrowsAfterAShrink(t *testing.T) {
	c, f := newTestCollector(t,
		tsvLine("1755000000.000000", 0, "/home/learner", "grep -rn 'ERROR' /var/log/app/")+
			tsvLine("1755000001.000000", 0, "/home/learner", "awk '{print $3}' access.log | sort -u")+
			tsvLine("1755000002.000000", 0, "/home/learner", "find . -name '*.conf' -mtime -7"),
	)
	ctx := context.Background()
	consumed := len(f.snapshot())

	if got, err := c.Since(ctx, "nav-01"); err != nil || len(got) != 3 {
		t.Fatalf("first Since = (%d entries, %v), want (3, nil)", len(got), err)
	}

	// The learner truncates the journal and starts it again from one short
	// record. The call that notices returns nothing: the remainder must not
	// be re-emitted the instant the file shrinks.
	f.setContent([]byte(tsvLine("1755000003.000000", 0, "/home/learner", "whoami")))
	if got, err := c.Since(ctx, "nav-01"); err != nil || len(got) != 0 {
		t.Fatalf("Since on the shrink = (%d entries, %v), want (0, nil)", len(got), err)
	}

	// The shell keeps writing. Three well formed records take the file back
	// past the length the first call had already consumed, so the stale
	// offset now points inside the new content rather than past the end of
	// it. assertLongerThan below pins that, because it is the whole point of
	// the case and a later edit to these command strings could quietly undo
	// it.
	f.appendContent(
		tsvLine("1755000004.000000", 0, "/home/learner", "sed -n '1,20p' /etc/services") +
			tsvLine("1755000005.000000", 1, "/home/learner", "tar -czf backup.tar.gz /home/learner/notes") +
			tsvLine("1755000006.000000", 0, "/home/learner", "chmod 0644 /home/learner/notes/reading-list.md"),
	)
	assertLongerThan(t, len(f.snapshot()), consumed)

	var got []Entry
	for i := 0; i < 3; i++ {
		batch, err := c.Since(ctx, "nav-01")
		if err != nil {
			t.Fatalf("Since call %d after the regrowth: %v", i+1, err)
		}
		got = append(got, batch...)
	}

	want := []string{
		"whoami",
		"sed -n '1,20p' /etc/services",
		"tar -czf backup.tar.gz /home/learner/notes",
		"chmod 0644 /home/learner/notes/reading-list.md",
	}
	var raws []string
	for _, e := range got {
		raws = append(raws, e.Raw)
	}
	if strings.Join(raws, "|") != strings.Join(want, "|") {
		t.Fatalf("after the regrowth Since collected %v, want %v: a stale offset reads from the middle of a record, which drops the commands around it and can admit a garbled one",
			raws, want)
	}
	for i, e := range got {
		if e.Cwd != "/home/learner" {
			t.Errorf("entry %d Cwd = %q, want /home/learner: a record parsed from a mid-line offset carries the wrong fields", i, e.Cwd)
		}
		if e.TS.IsZero() {
			t.Errorf("entry %d TS is the zero time, so its first field was not a timestamp", i)
		}
	}
}

func TestNewCollectorRefusesAStateDirectoryOutsideTheLearnerHome(t *testing.T) {
	// Every one of these fails a different arm of platform.UnsafeLevelRoot,
	// and none of them is somewhere the sandbox state directory can live.
	cases := map[string]string{
		"empty":            "",
		"relative":         ".shellforge",
		"dot dot segment":  "/home/learner/../etc/shellforge",
		"host path":        "/etc/shellforge",
		"the root itself":  "/",
		"the learner home": "/home/learner",
	}

	for name, dir := range cases {
		t.Run(name, func(t *testing.T) {
			c, err := NewCollector(&fakeSession{}, dir)
			if err == nil {
				t.Fatalf("NewCollector(%q) returned no error, want a refusal", dir)
			}
			if c != nil {
				t.Errorf("NewCollector(%q) returned a Collector alongside its refusal, want nil", dir)
			}
			if !strings.Contains(err.Error(), "state directory") {
				t.Errorf("NewCollector(%q) error = %q, want it to name what was refused", dir, err)
			}
		})
	}
}

func TestNewCollectorAcceptsTheStateDirectoryTheSandboxImageSets(t *testing.T) {
	f := &fakeSession{}
	c, err := NewCollector(f, "/home/learner/.shellforge")
	if err != nil {
		t.Fatalf("NewCollector on the real state directory: %v", err)
	}
	if _, err := c.Since(context.Background(), "nav-01"); err != nil {
		t.Fatalf("Since: %v", err)
	}
	if paths := f.pulledPaths(); len(paths) != 1 || paths[0] != "/home/learner/.shellforge/journal.tsv" {
		t.Errorf("pulled %v, want [/home/learner/.shellforge/journal.tsv]", paths)
	}
}

func TestSinceIsSafeUnderConcurrentCalls(t *testing.T) {
	c, _ := newTestCollector(t, manyRecords(50))
	ctx := context.Background()

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		total int
	)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := c.Since(ctx, "nav-01")
			if err != nil {
				t.Errorf("Since: %v", err)
				return
			}
			mu.Lock()
			total += len(got)
			mu.Unlock()
		}()
	}
	wg.Wait()

	if total != 50 {
		t.Errorf("eight concurrent calls together returned %d entries, want exactly 50", total)
	}
}
