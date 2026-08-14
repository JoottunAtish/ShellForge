package main

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/game"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/verify"
)

// The golden test contract from docs/LEVEL-FORMAT.md section 7, as production
// code so that `author test` and the Go test share one definition of it.
//
// A second definition would drift, and the two consumers would then disagree
// about whether a level was correct, which is the worst possible thing for a
// contract whose whole job is to say so.
//
// This lives in cmd/shellforge rather than internal/game because it needs a real
// sandbox, and internal/archtest's runtimeImplAllowed set deliberately excludes
// internal/game from importing a runtime implementation. Putting it there and
// widening that set would be exactly the leak the set exists to prevent.

// Golden stages, in the order they run. The stage name goes in every report, so
// a CI log alone is enough to diagnose a failure.
const (
	stageSetup     = "setup"
	stagePreCheck  = "pre_check"
	stageSolution  = "solution"
	stagePostCheck = "post_check"
	stagePurity    = "purity"
	stageTeardown  = "teardown"
)

// solutionTimeout bounds a level's own solution.
//
// Generous, because a solution is allowed to be a small script and a first run
// may touch a cold page cache. Bounded at all, because a solution with a typo
// that waits on stdin would otherwise hang `make golden` forever.
const solutionTimeout = 2 * time.Minute

// goldenHarness runs the contract against one sandbox session.
type goldenHarness struct {
	sess   runtime.Session
	packFS fs.FS

	// engine builds and runs each level's checks. It is the interface
	// internal/game declares rather than *verify.Engine, so a caller can
	// substitute one, and so this struct does not decide the budgets.
	engine game.Verifier
}

// goldenReport is what one level's run produced.
type goldenReport struct {
	LevelID string `json:"level"`
	OK      bool   `json:"ok"`

	// Stage is empty on success, and otherwise the stage that failed.
	Stage string `json:"stage,omitempty"`

	// Problem is what went wrong, in terms an author can act on.
	Problem string `json:"problem,omitempty"`

	// Timings records how long each stage took, for spotting a level that
	// passes but is too slow to verify inside the engine's budget.
	Timings map[string]string `json:"timings,omitempty"`
}

// fail returns a failing report for a stage.
func fail(levelID, stage string, timings map[string]string, format string, args ...any) goldenReport {
	return goldenReport{
		LevelID: levelID,
		Stage:   stage,
		Problem: fmt.Sprintf(format, args...),
		Timings: timings,
	}
}

// runGoldenLevel runs docs/LEVEL-FORMAT.md section 7's contract against one
// level and reports what happened.
//
// It returns a report rather than an error, because every caller wants the same
// thing: which stage failed and why, for every level, rather than stopping at the
// first one.
//
// Teardown always runs, including after a failure, so one bad level does not
// leave a world behind that breaks the next level's setup.
func runGoldenLevel(ctx context.Context, h goldenHarness, level *content.Level) (report goldenReport) {
	timings := map[string]string{}
	stopwatch := func(stage string, start time.Time) {
		timings[stage] = time.Since(start).Round(time.Millisecond).String()
	}

	session, err := game.NewSession(game.Config{
		Level:    level,
		Sess:     h.sess,
		PackFS:   h.packFS,
		Verifier: h.engine,
	})
	if err != nil {
		return fail(level.ID, stageSetup, timings, "build the level: %v", err)
	}

	// A snapshot of the sandbox user's processes before anything runs, so
	// teardown can be held to "no stray processes remain". A level with a
	// setup.script that backgrounds something and a teardown that forgets to
	// kill it is the case this catches, and it is invisible otherwise: the level
	// passes, and the next level inherits a process it did not start.
	processesBefore, err := learnerProcesses(ctx, h.sess)
	if err != nil {
		return fail(level.ID, stageSetup, timings, "%v", err)
	}

	// Teardown runs on every exit path, including a panic, and on a context
	// that cannot already be cancelled, matching the rule the run flow follows.
	// Registered before Setup so a Setup that fails halfway still cleans up.
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()

		start := time.Now()
		err := session.Teardown(cleanupCtx)
		stopwatch(stageTeardown, start)

		if err != nil {
			// Do not mask an earlier failure with a teardown failure: the
			// earlier one is the cause and this is likely a consequence.
			if report.OK {
				report = fail(level.ID, stageTeardown, timings, "teardown: %v", err)
			}
			return
		}
		if report.OK {
			if problem := assertTornDown(cleanupCtx, h.sess, level); problem != "" {
				report = fail(level.ID, stageTeardown, timings, "%s", problem)
				return
			}
			if problem := assertNoStrayProcesses(cleanupCtx, h.sess, processesBefore); problem != "" {
				report = fail(level.ID, stageTeardown, timings, "%s", problem)
			}
		}
	}()

	// 1. Fresh world.
	start := time.Now()
	err = session.Setup(ctx)
	stopwatch(stageSetup, start)
	if err != nil {
		return fail(level.ID, stageSetup, timings, "setup: %v", err)
	}

	// 2. Every required objective must FAIL before the learner does anything.
	//
	// The assertion is per objective rather than on Passed alone, deliberately.
	// Passed being false only proves SOME required check failed; a level can
	// ship with a required check that passes on a bare setup, verifying nothing
	// the learner has to do, and Passed would still be false because of its
	// siblings. Checking each one catches that, and it is the bug this stage
	// exists to find.
	start = time.Now()
	before, err := session.Check(ctx)
	stopwatch(stagePreCheck, start)
	if err != nil {
		return fail(level.ID, stagePreCheck, timings, "run the checks: %v", err)
	}
	for _, obj := range before.Objectives {
		if obj.Optional {
			continue
		}
		if obj.Status == verify.StatusPass {
			return fail(level.ID, stagePreCheck, timings,
				"objective %q passed before the solution ran, so the check does not test anything the learner has to do",
				obj.ID)
		}
	}

	// 3. Apply the level's own solution, as the learner, in a login shell.
	//
	// A login shell because that is what a learner has: it sources the
	// instrumentation rc and gets the same PATH, so a solution that only works
	// under a bare shell fails here rather than in front of somebody.
	start = time.Now()
	err = applySolution(ctx, h.sess, level)
	stopwatch(stageSolution, start)
	if err != nil {
		return fail(level.ID, stageSolution, timings, "%v", err)
	}

	// 4. Now every required objective must pass.
	//
	// Journal-backed objectives are exempt from this by being optional: the
	// validator requires them to be, and nothing populates the journal yet, so
	// a bonus that depends on it cannot pass. That is why steps 2 and 4 both
	// look only at required objectives, and it is a real limitation rather than
	// a loophole: a level whose only interesting assertion is a journal check
	// is not covered here at all.
	start = time.Now()
	after, err := session.Check(ctx)
	stopwatch(stagePostCheck, start)
	if err != nil {
		return fail(level.ID, stagePostCheck, timings, "run the checks: %v", err)
	}
	for _, obj := range after.Objectives {
		if obj.Optional {
			continue
		}
		if obj.Status != verify.StatusPass {
			return fail(level.ID, stagePostCheck, timings,
				"objective %q is %s after the level's own solution ran: %s.\n"+
					"      The level is wrong, not the solution. Do NOT loosen the check to make this pass.",
				obj.ID, obj.Status, strings.TrimSpace(firstLine(obj.Message)))
		}
	}
	if !after.Passed {
		return fail(level.ID, stagePostCheck, timings,
			"the level reports not passed after its own solution ran, even though every required objective passed")
	}

	// 5. Purity: the checks must not have changed anything.
	start = time.Now()
	problem := assertCheckPurity(ctx, h.sess, session, level)
	stopwatch(stagePurity, start)
	if problem != "" {
		return fail(level.ID, stagePurity, timings, "%s", problem)
	}

	return goldenReport{LevelID: level.ID, OK: true, Timings: timings}
}

// applySolution runs the level's solution the way a learner would.
func applySolution(ctx context.Context, sess runtime.Session, level *content.Level) error {
	solution := strings.TrimSpace(level.Solution)
	if solution == "" {
		return fmt.Errorf("the level declares no solution, so the golden contract cannot be checked")
	}

	// The whole solution is ONE argv element handed to bash -lc, never
	// concatenated into a host command line. -l for a login shell.
	//
	// WorkDir is the learner's home, NOT level.Setup.Root, and it has to match
	// what runLevel's Attach uses or this harness stops testing the level a
	// learner actually plays. That is not hypothetical: the first CI run of
	// this test failed nav-01 because both started in the level root, so
	// `pwd` answered /home/learner/quest while the level asks the learner
	// where they started and expects /home/learner. A solution verified from
	// a directory no learner is ever in is not a verified solution.
	// TestEverySandboxWorkDirIsTheLearnersHome holds both call sites to it.
	res, err := sess.Exec(ctx, []string{"bash", "-lc", solution}, runtime.ExecOpts{
		User:    sandboxUser,
		WorkDir: sandboxHome,
		Env:     map[string]string{"SF_STATE": setupStateDir()},
		Timeout: solutionTimeout,
	})
	if err != nil {
		return fmt.Errorf("run the level's solution: %w", err)
	}
	if res.TimedOut {
		return fmt.Errorf("the level's solution did not finish within %s; it may be waiting on input", solutionTimeout)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("bash -lc exited %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	return nil
}

// assertCheckPurity hashes the level's world, runs the checks twice more, and
// hashes again.
//
// This is the half of the purity guarantee that internal/verify's own
// purity_test.go cannot provide: that file asserts no check ASKS the sandbox to
// do anything mutating, from an allowlist of commands. This asserts that nothing
// actually changed, which catches a mutation the allowlist did not anticipate.
func assertCheckPurity(ctx context.Context, sess runtime.Session, session *game.Session, level *content.Level) string {
	before, err := hashLevelWorld(ctx, sess, level.Setup.Root)
	if err != nil {
		return fmt.Sprintf("hash the level world: %v", err)
	}

	for i := 0; i < 2; i++ {
		if _, err := session.Check(ctx); err != nil {
			return fmt.Sprintf("run the checks: %v", err)
		}
	}

	after, err := hashLevelWorld(ctx, sess, level.Setup.Root)
	if err != nil {
		return fmt.Sprintf("hash the level world: %v", err)
	}

	if before != after {
		return "running the checks changed the level world, so a check is mutating state.\n" +
			"      A check reads and nothing else: see internal/verify/doc.go's first invariant.\n" +
			diffFirstLine(before, after)
	}
	return ""
}

// hashLevelWorld returns a fingerprint of everything under root.
//
// It covers content AND mode, owner and size, because a check that chmods
// without changing a byte would pass a content-only hash. That is the same
// recipe internal/sandbox/demo_golden_test.go established.
//
// It runs INSIDE the sandbox, so it physically cannot see the host filesystem,
// and the root is passed as an argument to a fixed script rather than
// interpolated into it. find is invoked without -L, so a symlink a level planted
// cannot walk the walker out of the root.
func hashLevelWorld(ctx context.Context, sess runtime.Session, root string) (string, error) {
	if err := validateHashRoot(root); err != nil {
		return "", err
	}

	const script = `set -e
find "$1" -printf '%P %m %u:%g %s\n' | sort
find "$1" -type f -exec sha256sum {} + | sort`

	res, err := sess.Exec(ctx, []string{"sh", "-c", script, "sh", root}, runtime.ExecOpts{
		User:    sandboxUser,
		Timeout: 60 * time.Second,
	})
	if err != nil {
		return "", fmt.Errorf("hash %s: %w", root, err)
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("hash %s: exited %d: %s", root, res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	return string(res.Stdout), nil
}

// validateHashRoot refuses a root that is not safe to walk.
//
// The walk only reads, so this is not protecting against a delete. It is
// protecting against a bug in this file: a root that came through empty, or as
// "/", would hash the whole sandbox, which is slow enough to look like a hang and
// would make the purity comparison meaningless. Refusing rather than adjusting,
// per the house rule, and with a refusal table in the tests.
func validateHashRoot(root string) error {
	clean := path.Clean(strings.TrimSpace(root))

	switch {
	case strings.TrimSpace(root) == "":
		return fmt.Errorf("refusing to hash an empty path")
	case strings.Contains(root, ".."):
		return fmt.Errorf("refusing to hash %q: it contains a .. segment", root)
	case !strings.HasPrefix(clean, "/"):
		return fmt.Errorf("refusing to hash %q: it is not absolute", root)
	case clean == "/" || clean == ".":
		return fmt.Errorf("refusing to hash %q: that is the whole filesystem", root)
	}

	// Strictly under /home/learner, compared by path component so that
	// /home/learnerX is refused rather than accepted by a string prefix.
	const home = "/home/learner"
	if clean == home || !strings.HasPrefix(clean, home+"/") {
		return fmt.Errorf("refusing to hash %q: a level world lives strictly under %s/", root, home)
	}
	return nil
}

// assertTornDown checks that teardown removed the level's world and left no
// stray process behind.
func assertTornDown(ctx context.Context, sess runtime.Session, level *content.Level) string {
	res, err := sess.Exec(ctx, []string{"test", "-e", level.Setup.Root}, runtime.ExecOpts{User: sandboxUser})
	if err != nil {
		return fmt.Sprintf("check whether the level world was removed: %v", err)
	}
	if res.ExitCode == 0 {
		return fmt.Sprintf("teardown left %s in place", level.Setup.Root)
	}
	return ""
}

// assertNoStrayProcesses compares the sandbox user's processes against a
// baseline taken before the level ran.
//
// A delta means the level started something that outlived its own teardown. That
// matters beyond tidiness: the container is shared across levels within a run, so
// a leaked process becomes the next level's problem, and a level whose checks
// look at `ps` would start seeing something a previous level left behind.
func assertNoStrayProcesses(ctx context.Context, sess runtime.Session, before []string) string {
	after, err := learnerProcesses(ctx, sess)
	if err != nil {
		return fmt.Sprintf("%v", err)
	}

	baseline := map[string]int{}
	for _, p := range before {
		baseline[p]++
	}

	var stray []string
	for _, p := range after {
		if baseline[p] > 0 {
			baseline[p]--
			continue
		}
		stray = append(stray, p)
	}

	if len(stray) > 0 {
		return fmt.Sprintf("teardown left this running: %s.\n"+
			"      A level that starts a process is responsible for stopping it, in teardown.script.",
			strings.Join(stray, "; "))
	}
	return ""
}

// learnerProcesses lists the sandbox user's processes, for the stray-process
// assertion. Sorted so two snapshots compare cleanly.
func learnerProcesses(ctx context.Context, sess runtime.Session) ([]string, error) {
	res, err := sess.Exec(ctx, []string{"ps", "-u", sandboxUser, "-o", "args="}, runtime.ExecOpts{User: sandboxUser})
	if err != nil {
		return nil, fmt.Errorf("list the sandbox user's processes: %w", err)
	}
	// ps exits non-zero when it matches nothing, which is a legitimate answer
	// rather than a failure.
	var out []string
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// The ps that produced this snapshot is itself a process, and so is
		// whatever shell the runtime wrapped it in. Neither is a stray.
		if strings.Contains(line, "ps -u") || strings.HasPrefix(line, "sh -c") {
			continue
		}
		out = append(out, line)
	}
	sort.Strings(out)
	return out, nil
}

// firstLine returns the first line of s, for a one-line failure report.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// diffFirstLine names the first line where two hashes differ, so a purity
// failure points at a file rather than at two walls of text.
func diffFirstLine(before, after string) string {
	b := strings.Split(before, "\n")
	a := strings.Split(after, "\n")
	for i := 0; i < len(b) && i < len(a); i++ {
		if b[i] != a[i] {
			return fmt.Sprintf("      first difference:\n        before: %s\n        after:  %s", b[i], a[i])
		}
	}
	if len(b) != len(a) {
		return fmt.Sprintf("      the number of entries changed: %d before, %d after", len(b), len(a))
	}
	return ""
}
