package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"math/rand"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

// The Day 1 demo level, hardcoded in Go.
//
// Levels become YAML data on Day 2 and this file is expected to be DELETED
// then, not extended. It exists so the Day 1 gate can be demonstrated before
// the content engine exists, and it is deliberately shaped like the YAML that
// replaces it so that migration is mechanical rather than a rewrite.
//
// The level mirrors pipe-05 from docs/LEVEL-FORMAT.md section 6, so the
// numbers are already known good: three rotated log files, a total count of
// lines matching ERROR, and that count written into report.txt.

const (
	// levelRootPrefix is the only prefix a level root may live under. It
	// matches FileEntry.Path's contract and the docker backend's own
	// sandboxRoot, and it is the load-bearing half of every refusal in
	// validateLevelRoot.
	levelRootPrefix = "/home/learner/"

	// demoRoot is the demo level's world. It is a compile-time constant in
	// this ticket, which removes most of the risk from the teardown path,
	// but validateLevelRoot still guards it because Day 2 makes this value
	// data-driven and the guard has to predate that.
	demoRoot = "/home/learner/quest"

	// demoStateDir is SF_STATE for the demo session: where the shell
	// instrumentation writes its journal and snapshots, and where the
	// control channel FIFOs live. It matches the default in
	// images/rc/instrument.bash and images/bin/_sf-request, and the
	// directory the Containerfile creates and chowns to learner.
	demoStateDir = "/opt/shellforge/state"

	// demoUser is the sandbox user the learner plays as.
	demoUser = "learner"

	// demoOwner is the owner:group applied to every file the demo level
	// materializes.
	demoOwner = "learner:learner"

	// demoErrorCount is how many lines across the three generated log files
	// match ERROR case-insensitively, and therefore the answer the learner
	// must write into report.txt.
	//
	// It is a budget the generator spends, not a number somebody counted
	// once: demoLogFiles below distributes exactly this many ERROR lines
	// across the three files, so the answer is a property of the generator
	// rather than an observation about it. TestFixtureMatchesTheAnswer
	// verifies it independently by counting matches the way the solution's
	// own grep does, which is what makes the asset provably consistent with
	// its answer.
	demoErrorCount = 147

	// demoSeed seeds the log generator. Setup must be deterministic and
	// offline: the same seed produces byte-identical logs on every machine,
	// with no network call and no embedded asset file.
	demoSeed = 20260314

	// checkTimeout bounds the one Exec the check makes.
	//
	// Without it, cat is not guaranteed to return, and the check runs on
	// a path the learner owns. A report.txt that is a named pipe with no
	// writer blocks the read forever, which wedges the control channel for
	// the rest of the session: the learner's own `check` hangs and no later
	// one is ever served. A symlink to an endless device such as /dev/zero is
	// worse, because the runtime accumulates stdout in memory with no cap.
	// Neither is a plausible accident. Neither should cost a learner their
	// session either, and the failure it produces has to be a bounded one.
	//
	// Ten seconds is far longer than reading one small file needs, and far
	// shorter than a learner will sit in front of a dead prompt.
	checkTimeout = 10 * time.Second
)

// DemoLevel is the Day 1 hardcoded level. It is replaced by YAML on Day 2 and
// should be deleted then, not extended.
type DemoLevel struct {
	// ID is the level id the CLI dispatches on.
	ID string

	// Briefing is the markdown-ish text shown before the prompt appears.
	Briefing string

	// Root is the level's world inside the sandbox, under
	// /home/learner/. Teardown removes it, so every path in this package
	// that reaches a destructive argv is validated against it first.
	Root string

	// Files are the level's assets, materialized by Setup.
	Files []runtime.FileEntry

	// Solution is the command sequence that satisfies the level. A golden
	// test runs it between a failing Check and a passing one.
	Solution string
}

// demoLogFile describes one generated log file and its share of the ERROR
// budget. The three shares sum to demoErrorCount, which is asserted by
// TestErrorBudgetSumsToTheAnswer rather than left to arithmetic in a comment.
type demoLogFile struct {
	name       string
	service    string
	lines      int
	errorLines int
}

var demoLogFiles = []demoLogFile{
	{name: "app-1.log", service: "api", lines: 420, errorLines: 58},
	{name: "app-2.log", service: "worker", lines: 380, errorLines: 41},
	{name: "billing.log", service: "billing", lines: 260, errorLines: 48},
}

// demoNoiseLine is one non-ERROR log line: a severity and the message that
// goes with it.
//
// The two are paired rather than chosen independently so the fixture reads
// like a real log. Picking a level and a message separately produces lines
// like "WARN health probe ok", which is nonsense a learner reading the file
// would notice.
type demoNoiseLine struct {
	level string
	text  string
}

// demoNoiseLines are the non-ERROR log lines.
//
// Not one of them, level or text, may contain the substring "error" in any
// case. The solution's grep is case-insensitive, so a single stray "Error" or
// "errors" here would silently add a matching line and make the level's own
// answer wrong. TestNoiseNeverMatchesTheAnswer pins that.
var demoNoiseLines = []demoNoiseLine{
	{"INFO", "request completed in 42ms"},
	{"DEBUG", "cache hit for key user:1204"},
	{"INFO", "connection pool resized to 16"},
	{"WARN", "retry scheduled in 500ms"},
	{"DEBUG", "heartbeat acknowledged"},
	{"INFO", "config reloaded from disk"},
	{"DEBUG", "queue depth 12"},
	{"INFO", "session token refreshed"},
	{"INFO", "batch flushed, 240 rows"},
	{"DEBUG", "health probe ok"},
	{"WARN", "upstream latency 880ms"},
	{"TRACE", "worker claimed job 4471"},
	{"WARN", "request took 2.1s, above the 1s budget"},
	{"INFO", "shutdown signal handled cleanly"},
}

// demoErrorMessages are the messages on ERROR lines. The codes match
// pipe-05's own codes.txt objective so the fixture stays recognisable when
// this level becomes YAML.
var demoErrorMessages = []string{
	"upstream timeout on /v1/charges (E503)",
	"payment declined by processor (E401)",
	"database connection lost (E500)",
	"card tokenization failed (E401)",
	"ledger write rejected (E500)",
	"settlement batch unavailable (E503)",
}

// demoBriefing is what the learner reads before the prompt appears.
var demoBriefing = strings.Join([]string{
	"## 02:41 - Billing service",
	"",
	"Errors are pouring in. Nobody knows how many.",
	"Today's rotated logs are in `~/quest/logs/`.",
	"",
	"Find out how bad it is.",
	"",
	"Objective: write the total number of ERROR lines into `~/quest/report.txt`.",
	"",
	"Type `check` when you think you have it. Type `exit` to leave.",
}, "\n")

// demoSolution is the command that satisfies the level, and the command a
// golden test runs between a failing Check and a passing one.
const demoSolution = "grep -ri ERROR ~/quest/logs/ | wc -l > ~/quest/report.txt"

// Demo returns the single hardcoded level.
func Demo() DemoLevel {
	files := make([]runtime.FileEntry, 0, len(demoLogFiles))
	for i, f := range demoLogFiles {
		files = append(files, runtime.FileEntry{
			// A distinct seed per file, so two files with the same line
			// and error budget do not come out byte-identical.
			Path:    path.Join(demoRoot, "logs", f.name),
			Content: demoLogContent(f, demoSeed+int64(i)),
			Mode:    fs.FileMode(0o644),
			Owner:   demoOwner,
		})
	}
	return DemoLevel{
		ID:       "demo",
		Briefing: demoBriefing,
		Root:     demoRoot,
		Files:    files,
		Solution: demoSolution,
	}
}

// ReportPath is the file the learner must write the answer into.
func (l DemoLevel) ReportPath() string {
	return path.Join(l.Root, "report.txt")
}

// Answer is the value ReportPath must hold, compared after trimming
// surrounding whitespace.
func (l DemoLevel) Answer() string {
	return strconv.Itoa(demoErrorCount)
}

// StateDir is SF_STATE for this level's session.
func (l DemoLevel) StateDir() string {
	return demoStateDir
}

// User is the sandbox user the level is played as.
func (l DemoLevel) User() string {
	return demoUser
}

// demoLogContent generates one log file deterministically.
//
// The ERROR line count is fixed by construction rather than by chance: the
// budget is laid down as a fixed number of true values and then shuffled, so
// the count survives any future change to how the shuffle picks positions.
// The generator's randomness only decides where the ERROR lines land and
// which message each line carries, never how many there are.
func demoLogContent(f demoLogFile, seed int64) []byte {
	// #nosec G404 -- this is a deterministic content generator, not a
	// security decision. A cryptographic source would defeat the point:
	// setup must produce byte-identical assets on every machine.
	rng := rand.New(rand.NewSource(seed))

	isError := make([]bool, f.lines)
	for i := 0; i < f.errorLines; i++ {
		isError[i] = true
	}
	rng.Shuffle(f.lines, func(i, j int) {
		isError[i], isError[j] = isError[j], isError[i]
	})

	// A fixed wall-clock start, so no test and no learner ever sees a
	// timestamp that depends on when setup ran.
	stamp := time.Date(2026, time.March, 14, 2, 0, 0, 0, time.UTC)

	var buf bytes.Buffer
	for i := 0; i < f.lines; i++ {
		stamp = stamp.Add(time.Duration(1+rng.Intn(20)) * time.Second)

		noise := demoNoiseLines[rng.Intn(len(demoNoiseLines))]
		level, message := noise.level, noise.text
		if isError[i] {
			level = "ERROR"
			message = demoErrorMessages[rng.Intn(len(demoErrorMessages))]
		}

		fmt.Fprintf(&buf, "%s %s %s %s\n", stamp.Format("2006-01-02T15:04:05Z"), f.service, level, message)
	}
	return buf.Bytes()
}

// validateLevelRoot refuses, rather than adjusts, any path that is not safe
// to hand to a recursive delete inside the sandbox.
//
// This is the guard the destructive-safety skill requires, and it runs before
// any path reaches a destructive argv. It refuses in this order:
//
//   - an empty path, or one that cleans to "." or "/"
//   - a path containing a ".." segment, checked BEFORE cleaning, because
//     cleaning is exactly what would hide it
//   - a path that is not absolute after cleaning
//   - a path that is not strictly under /home/learner/
//   - /home/learner itself, which is the learner's whole home directory and
//     never a level root
//
// It is lexical only. A symlink escaping the root is a filesystem property
// that no host-side string check can see, so Setup and Teardown resolve the
// path inside the sandbox as well and re-check the result. See
// resolveLevelRoot.
func validateLevelRoot(root string) error {
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("refusing to use %q as a level root: it is empty", root)
	}
	// Before cleaning, not after. path.Clean("/home/learner/../etc")
	// returns "/home/etc", which passes a naive prefix check while pointing
	// somewhere it must never point.
	for _, segment := range strings.Split(root, "/") {
		if segment == ".." {
			return fmt.Errorf("refusing to use %q as a level root: it contains a .. segment", root)
		}
	}
	clean := path.Clean(root)
	if !path.IsAbs(clean) {
		return fmt.Errorf("refusing to use %q as a level root: it is not absolute", root)
	}
	if clean == "/" || clean == "." {
		return fmt.Errorf("refusing to use %q as a level root: it resolves to %q", root, clean)
	}
	if !strings.HasPrefix(clean, levelRootPrefix) {
		return fmt.Errorf("refusing to use %q as a level root: level roots must live under %s", root, levelRootPrefix)
	}
	return nil
}

// resolveLevelRoot validates root lexically, then resolves it INSIDE the
// sandbox and refuses if resolution moved it at all.
//
// A learner can create a symlink during a level, so the root that was safe
// when the level loaded can point somewhere else by the time teardown runs.
// `readlink -m` canonicalizes without requiring the path to exist, which is
// what makes this work on a first run where the root has not been created
// yet as well as on a reset where it has.
//
// Any difference between the cleaned path and the resolved path is refused
// outright rather than followed. A level root that is a symlink, or that sits
// below one, is not a case this project needs to support, and refusing is the
// fail-closed answer the destructive-safety skill requires.
func resolveLevelRoot(ctx context.Context, s runtime.Session, root string) (string, error) {
	if err := validateLevelRoot(root); err != nil {
		return "", err
	}
	clean := path.Clean(root)

	res, err := s.Exec(ctx, []string{"readlink", "-m", "--", clean}, runtime.ExecOpts{User: demoUser})
	if err != nil {
		return "", fmt.Errorf("resolve the level root %q inside the sandbox: %w", clean, err)
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("resolve the level root %q inside the sandbox: readlink exited %d", clean, res.ExitCode)
	}
	resolved := strings.TrimSpace(string(res.Stdout))
	if resolved != clean {
		return "", fmt.Errorf("refusing to use %q as a level root: inside the sandbox it resolves to %q, so something on that path is a symlink", clean, resolved)
	}
	// Re-validate the resolved value. It is equal to clean here by the
	// check above, so this cannot fail today; it stays because the check
	// above is the kind of thing a future change relaxes, and this is the
	// assertion that would catch it.
	if err := validateLevelRoot(resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

// Teardown removes the level's world, inside the sandbox.
//
// This is the ONLY function in this package that deletes anything, and it
// validates its own target rather than trusting a caller to have done it.
// The removal runs via Session.Exec inside the sandbox: a host-side
// os.RemoveAll on a level root is never correct.
//
// It is idempotent. Removing a root that is not there succeeds, which is what
// lets Setup call it unconditionally.
//
// The removal runs as the learner, NOT as root, and that is not a stylistic
// choice. The sandbox container drops every Linux capability and adds back only
// CHOWN and FOWNER, so root inside it does not hold CAP_DAC_OVERRIDE and does
// not bypass ordinary permission checks. Unlinking a file needs write
// permission on its directory, and the level root is learner-owned 0755 by the
// time anything is in it, so a root `rm -rf` fails with Permission denied on
// every entry. Measured, not theorised: that is exactly how the golden test
// failed before this comment existed. The learner owning their own world is the
// design, so the learner is who removes it.
func (l DemoLevel) Teardown(ctx context.Context, s runtime.Session) error {
	root, err := resolveLevelRoot(ctx, s, l.Root)
	if err != nil {
		return err
	}

	// Best effort repair, for one specific broken state: a Setup that died
	// between PushFiles and its chown leaves root-owned directories the
	// learner cannot delete. root can still chown them because CAP_CHOWN is
	// one of the two capabilities the container keeps. The exit code is
	// deliberately ignored, because the overwhelmingly common case is that
	// the root does not exist at all and chown rightly fails.
	if _, err := s.Exec(ctx, []string{"chown", "-R", demoOwner, "--", root}, runtime.ExecOpts{User: "root"}); err != nil {
		return fmt.Errorf("prepare the level root %q for removal: %w", root, err)
	}

	res, err := s.Exec(ctx, []string{"rm", "-rf", "--", root}, runtime.ExecOpts{User: demoUser})
	if err != nil {
		return fmt.Errorf("remove the level root %q: %w", root, err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("remove the level root %q: rm exited %d: %s", root, res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	return nil
}

// Setup materializes the level into the session, teardown-first so it is
// idempotent.
func (l DemoLevel) Setup(ctx context.Context, s runtime.Session) error {
	root, err := resolveLevelRoot(ctx, s, l.Root)
	if err != nil {
		return err
	}
	if err := l.Teardown(ctx, s); err != nil {
		return err
	}
	if err := s.PushFiles(ctx, runtime.FileManifest{Files: l.Files}); err != nil {
		return fmt.Errorf("materialize the demo level assets: %w", err)
	}

	// Hand the world to the learner.
	//
	// This step is not decoration. PushFiles creates the ancestor
	// directories of every entry as uid 0 and only chmods them, so after
	// the push /home/learner/quest and /home/learner/quest/logs are
	// root-owned 0755 even though the log files themselves are
	// learner-owned. Without this chown the learner cannot create
	// report.txt in their own level root and the level is unsolvable: the
	// solution's redirect fails with Permission denied. pipe-05 in
	// docs/LEVEL-FORMAT.md carries the same step as its setup.script
	// ("chown -R learner:learner ."), so this is the hardcoded equivalent
	// of authored behaviour rather than a workaround.
	res, err := s.Exec(ctx, []string{"chown", "-R", demoOwner, "--", root}, runtime.ExecOpts{User: "root"})
	if err != nil {
		return fmt.Errorf("give the level root to %s: %w", demoUser, err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("give the level root to %s: chown exited %d: %s", demoUser, res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	return nil
}

// The check's failure messages.
//
// There are three because they are three different problems, and telling them
// apart is the difference between a learner fixing what is actually wrong and
// a learner redoing the part that was already right.
//
// None of them asks whether the search was case-insensitive, and that is
// deliberate. Nothing in this fixture matches ERROR in any case except the
// literal level label, so `grep -r ERROR` and `grep -ri ERROR` return the same
// number and case sensitivity cannot be the reason a learner's count is wrong.
// Asking anyway spends the one message a stuck learner reads on a dead end.
// TestCaseSensitivityDoesNotChangeTheAnswer pins the fact this wording rests
// on, so a fixture that later grows lowercase error text fails a test instead
// of quietly making the advice true again.
const (
	onFailMissing = "There is no ~/quest/report.txt yet. Put your total in that file, as a single number on its own."
	onFailEmpty   = "~/quest/report.txt exists but is empty, so nothing reached it. Check that your command prints a number on its own before you send it to the file."
)

// onFailWrong reports a report.txt that exists and holds the wrong thing.
//
// It quotes what is in there, because a wrong number and a wrong format fail
// this check identically and need opposite fixes. A learner who counted with
// `wc -l` against a file rather than a pipe has "147 somefile" in report.txt:
// the count is already right, and telling them the number is off sends them
// away to recount something they had finished.
func onFailWrong(got string) string {
	return fmt.Sprintf("~/quest/report.txt holds %s, which is not the total on its own. "+
		"Three things worth checking: that the search reaches all three files under logs/ rather than one of them, "+
		"that you are counting matching lines rather than words, bytes, or every line in the files, "+
		"and that nothing but the number ends up in the file.", quoteReportContent(got))
}

// quoteReportContent renders report.txt's content for a failure message,
// truncated on a rune boundary.
//
// The learner owns this string and can redirect an entire log file into it, so
// it is never printed whole: the message has to stay readable on the terminal
// it is about to be written to.
func quoteReportContent(got string) string {
	const max = 48
	if runes := []rune(got); len(runes) > max {
		return strconv.Quote(string(runes[:max])) + " (truncated)"
	}
	return strconv.Quote(got)
}

// Check runs the single assertion and returns a human-readable result.
//
// It reads real sandbox state through Session.Exec and never consults the
// journal, per internal/journal's own doc comment: a learner can forge a
// byte-identical OSC 133 marker, so the journal proves nothing about what
// ran. It also never writes: cat is the whole of it, so running Check twice
// leaves the filesystem identical.
func (l DemoLevel) Check(ctx context.Context, s runtime.Session) (passed bool, message string, err error) {
	res, execErr := s.Exec(ctx, []string{"cat", "--", l.ReportPath()}, runtime.ExecOpts{
		User: demoUser,
		// Bounded, because the path being read is one the learner controls.
		// See checkTimeout.
		Timeout: checkTimeout,
	})
	if execErr != nil {
		return false, "", fmt.Errorf("read %s: %w", l.ReportPath(), execErr)
	}
	if res.TimedOut {
		return false, "", fmt.Errorf("read %s: gave up after %s. If report.txt is a named pipe, or a link to something with no end such as /dev/zero, remove it with `rm ~/quest/report.txt` and write an ordinary file instead", l.ReportPath(), checkTimeout)
	}
	// A non-zero exit is the file being absent or unreadable, which is a
	// normal check failure and not an error. That distinction is the reason
	// this reads through Exec rather than PullFile: PullFile reports both
	// cases the same way.
	if res.ExitCode != 0 {
		return false, onFailMissing, nil
	}

	got := strings.TrimSpace(string(res.Stdout))
	if got == "" {
		return false, onFailEmpty, nil
	}
	if got != l.Answer() {
		return false, onFailWrong(got), nil
	}
	return true, fmt.Sprintf("report.txt holds the total ERROR count (%s)", l.Answer()), nil
}
