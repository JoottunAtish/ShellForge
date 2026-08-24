package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/game"
	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
	"github.com/JoottunAtish/ShellForge/internal/pty"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/sandbox"
	"github.com/JoottunAtish/ShellForge/internal/verify"
	"github.com/JoottunAtish/ShellForge/packs"
)

// The `run` verb: play one level, from the embedded YAML pack, end to end.
//
// The flow is the one #11 proved for a single hardcoded level, generalised to
// any level in the pack. Every ordering comment below was load bearing then and
// still is; where one explains a defect that was measured rather than reasoned
// about, it says so.

const (
	// sandboxHome is where the learner starts: the session's default working
	// directory, the interactive shell's, and the golden harness's when it runs
	// a level's own solution.
	//
	// It is deliberately NOT the level root, for two separate reasons. The
	// mechanical one: `docker exec -w` fails on a directory that does not
	// exist, and the level root does not exist until Setup creates it, so a
	// session defaulting to the level root could not run the very calls that
	// create it. The one a learner can see: a login shell starts you in your
	// home directory, and nav-01 teaches that by asking where you started, so
	// a shell opening anywhere else would make the campaign's first lesson
	// false.
	sandboxHome = "/home/learner"

	// sandboxUser is the unprivileged user a learner plays as. It matches the
	// Containerfile's USER and setup.DefaultOwner.
	sandboxUser = "learner"

	// controlReplyTimeout bounds a write to the response FIFO.
	//
	// The shim opens the response FIFO for reading immediately after
	// writing its request, so a write normally rendezvouses at once. Two things
	// can leave nobody reading. The learner can kill the shim, and, more
	// commonly, the learner can press Ctrl-C during a slow check: raw mode
	// means their 0x03 reaches the sandbox as a byte, the container's line
	// discipline turns it into SIGINT for the foreground group, and `check`
	// dies with its `_sf-request` child. Either way an unbounded write would
	// block forever on open(2) and wedge the control loop for the rest of the
	// session. A bounded write turns that into one skipped reply.
	//
	// TODO(v0.2): the cost of that bound is that a `check` typed immediately
	// after an abandoned one can wait up to this long, because the shim's own
	// write blocks until the host loops back to its read. One short line is
	// well under PIPE_BUF so nothing is interleaved or lost, only late.
	controlReplyTimeout = 10 * time.Second

	// cleanupTimeout bounds teardown after the shell has exited.
	cleanupTimeout = 30 * time.Second

	// controlDrainTimeout bounds how long the exit path waits for the control
	// loop to return after its context is cancelled.
	//
	// Cancelling runCtx unblocks the loop's read, and the docker backend then
	// kills the sandbox-side process it started. That kill runs INSIDE the
	// goroutine, so returning without waiting for it races process exit, and a
	// lost race leaves a `cat` holding the FIFO open in a container that
	// outlives the run.
	//
	// Bounded rather than unconditional: a control loop that will not come
	// back must not be able to stop the learner from getting their prompt
	// back. One orphaned process in a disposable container is the smaller
	// problem.
	controlDrainTimeout = 5 * time.Second

	// checkSlack is added to the engine's level budget to bound one `check`.
	//
	// The host cannot observe the learner's Ctrl-C, so it cannot cancel a run
	// in flight; what it can do is refuse to keep working forever on a reply
	// nobody will read. The engine's own level timeout does the real bounding
	// and this is only the margin that keeps the two from racing.
	checkSlack = 5 * time.Second

	// docAnchorLevelNotFound explains an unknown level id.
	//
	// DocAnchor: "level-not-found"
	docAnchorLevelNotFound = "level-not-found"
)

// runOptions is what `run` parsed out of its arguments.
type runOptions struct {
	levelID string
	debug   bool
}

// controlResponder answers one control request from inside the sandbox.
//
// The interface exists so the control loop is level-agnostic: one loop serves
// every level in the pack, and will serve whatever `run` learns to play next,
// without a second copy of the FIFO plumbing to keep in step.
//
// The reply is written verbatim to the learner's terminal by the shim, so an
// implementation returns text that is already CRLF terminated.
type controlResponder interface {
	Reply(ctx context.Context, verb, args string) string
}

// playable is what the run flow needs of a level.
//
// Setup and Teardown take no session: an implementation closes over the one it
// was built with, which keeps the flow below from having to know how a level
// gets at the sandbox.
type playable interface {
	LevelID() string
	Root() string
	StateDir() string
	Setup(ctx context.Context) error
	Teardown(ctx context.Context) error
	Responder(color bool) controlResponder
	PrintBriefing(w io.Writer, color bool)
}

// cmdRun implements `shellforge run <level-id>`.
//
// The order here is deliberate and pinned by a test: the pack is loaded and the
// level looked up BEFORE the platform guard, so an unknown level is reported as
// an unknown level on every host rather than being masked by the Windows
// refusal. Both steps run on the host with no Docker and no platform
// dependency, so neither can fail for an unrelated reason.
func cmdRun(ctx context.Context, args []string) error {
	pack, err := content.Embedded()
	if err != nil {
		// content.LoadPack already returns a *ux.Error with the
		// level-pack-invalid anchor.
		return err
	}

	order := levelOrder(pack)
	if len(order) == 0 {
		return ux.Fail(
			"find a level to play",
			nil,
			"The content pack in this build has no levels. This is a packaging bug rather than anything you did: please run `shellforge bug-report`.",
			docAnchorLevelNotFound,
		)
	}

	opts, err := parseRunArgs(args, order[0])
	if err != nil {
		return err
	}

	level, ok := pack.Level(opts.levelID)
	if !ok {
		return unknownLevel(opts.levelID, order)
	}

	if err := checkInteractiveShellSupported(opts.levelID); err != nil {
		return err
	}
	return runLevel(ctx, opts, level)
}

// levelOrder returns the pack's level ids in campaign order.
//
// Pack.Order sorts by act and then by the prerequisite DAG, which is the order a
// learner should meet them in and therefore the right order to list. Its only
// error is a prerequisite cycle; on one, fall back to load order rather than
// give up. A malformed pack must not turn a helpful message into a failure to
// name any level at all.
func levelOrder(pack *content.Pack) []string {
	if order, err := pack.Order(); err == nil {
		return order
	}
	ids := make([]string, 0, len(pack.Levels))
	for i := range pack.Levels {
		ids = append(ids, pack.Levels[i].ID)
	}
	return ids
}

// unknownLevel reports a level id that is not in the pack, naming the ids that
// are.
//
// It names campaign order and nothing else. An id that is not in the pack is
// never offered back as a suggestion, however plausible it looks: sending
// somebody who mistyped a real level to an id that teaches them nothing about
// what they were trying to do is worse than telling them the truth.
//
// order may be empty: a pack can have a valid pack.yaml and no levels/
// directory at all (content.LoadPack allows it), and `shellforge author test
// <id> --pack <such a dir>` reaches this function directly, bypassing
// cmdRun's own empty-pack guard. Indexing order[0] unconditionally would
// panic in front of a level author, which is exactly what non-negotiable 6
// exists to prevent.
func unknownLevel(id string, order []string) error {
	if len(order) == 0 {
		return ux.Fail(
			fmt.Sprintf("play level %q", id),
			nil,
			fmt.Sprintf("There is no level called %q, because this pack contains no levels at all. Check that the pack directory has a levels/ directory.", id),
			docAnchorLevelNotFound,
		)
	}
	return ux.Fail(
		fmt.Sprintf("play level %q", id),
		nil,
		fmt.Sprintf("There is no level called %q. This pack has %s, in campaign order: %s. Run `shellforge run %s` to start at the beginning.",
			id, plural(len(order), "level"), strings.Join(order, ", "), order[0]),
		docAnchorLevelNotFound,
	)
}

// checkInteractiveShellSupported refuses, up front, on a host that cannot give
// the learner an interactive shell at all.
//
// Attaching allocates a pseudo terminal on the HOST with creack/pty, and that
// package's Windows implementation returns ErrUnsupported unconditionally:
// there is no ConPTY path in it. This is true of the Docker backend, and it is
// also true of the WSL backend's own interactive attach, even though the WSL
// backend can already run one-shot commands and push files into the sandbox
// by shelling out to wsl.exe directly (see internal/runtime/wsl/session.go's
// Attach and resize_windows.go's Resize, both of which return the same
// ErrUnsupported). So `run` on a Windows host, on either backend, gets all
// the way through provisioning and a level setup and then fails at the last
// step with an error that reads like a backend problem and is not one.
//
// The runtime contract suite does not catch this, because it has no Attach
// assertion at all, which is why it passes on Windows.
//
// Refusing here rather than at Attach saves several minutes of provisioning
// before an error the user cannot act on. Until Windows console support
// (ConPTY) is built, which is issue #138 rather than this ticket, running
// from inside WSL is the way: WSL is a real Linux host, and Docker Desktop's
// WSL integration shares one daemon between the two sides.
//
// TODO(v0.2): this tests goruntime.GOOS rather than asking
// Runtime.Capabilities(), which is issue #77. The refusal is correct today and
// the fix touches runtime.Caps, a code-owner path.
func checkInteractiveShellSupported(levelID string) error {
	if goruntime.GOOS != "windows" {
		return nil
	}
	return ux.Fail(
		"open an interactive sandbox shell on Windows",
		nil,
		fmt.Sprintf("Open your WSL distribution, change to this repository, then build and run there: `go build -o bin/shellforge ./cmd/shellforge && ./bin/shellforge run %s`. Neither backend can open an interactive shell from PowerShell or the command prompt yet; Docker Desktop's WSL integration shares one daemon, so the sandbox image is not rebuilt.", levelID),
		"windows-needs-wsl",
	)
}

// parseRunArgs reads the level id and the one flag `run` understands today.
//
// This stays a hand-rolled parser under cobra: newRunCommand in root.go sets
// DisableFlagParsing so this function keeps owning --log-level, rather than a
// later ticket rewriting and re-testing logic it was not asked to touch.
//
// firstLevel is the id to suggest in an error, which is the first level in
// campaign order. Passing it in is what removed the three hardcoded references
// to the demo level that used to be here: the suggestion now follows the pack.
func parseRunArgs(args []string, firstLevel string) (runOptions, error) {
	var opts runOptions

	badFlag := func(a string) error {
		return ux.Fail(
			fmt.Sprintf("understand the option %q", a),
			nil,
			"Run `shellforge help run` for the usage. The only option today is --log-level=debug.",
			"",
		)
	}

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--log-level":
			if i+1 >= len(args) {
				return opts, badFlag(a)
			}
			i++
			opts.debug = args[i] == "debug"
		case strings.HasPrefix(a, "--log-level="):
			opts.debug = strings.TrimPrefix(a, "--log-level=") == "debug"
		case strings.HasPrefix(a, "-"):
			return opts, badFlag(a)
		case opts.levelID != "":
			return opts, ux.Fail(
				"work out which level to play",
				nil,
				fmt.Sprintf("You named more than one level (%q and %q). Name just one, as in `shellforge run %s`.", opts.levelID, a, firstLevel),
				"",
			)
		default:
			opts.levelID = a
		}
	}

	if opts.levelID == "" {
		return opts, ux.Fail(
			"work out which level to play",
			nil,
			fmt.Sprintf("Name a level: `shellforge run %s`.", firstLevel),
			"",
		)
	}
	return opts, nil
}

// runLevel plays one level loaded from the pack.
func runLevel(ctx context.Context, opts runOptions, level *content.Level) error {
	packFS, err := packFilesystem()
	if err != nil {
		return err
	}

	rt, sess, cleanupSession, err := openSandbox(ctx, opts.levelID)
	if err != nil {
		return err
	}
	_ = rt
	defer cleanupSession()

	session, err := game.NewSession(game.Config{
		Level:    level,
		Sess:     sess,
		PackFS:   packFS,
		Verifier: verify.NewEngine(),
	})
	if err != nil {
		return err
	}

	return play(ctx, opts, sess, &gameLevel{session: session, level: level})
}

// packFilesystem returns the embedded pack, rooted AT the pack directory.
//
// This is load bearing and the reason it is its own function with this comment.
// setup.Runner reads a level's assets with fs.ReadFile(packFS, spec.Source) and
// joins no pack root of its own, while packs.FS is rooted one level higher with
// paths beginning "core-linux-basics/". Handing it packs.FS directly makes every
// `source:` in every level fail to resolve. No shipped level used `source:`
// before pipe-05, so nothing caught this until a level needed it.
func packFilesystem() (fs.FS, error) {
	packFS, err := fs.Sub(packs.FS, packs.CoreLinuxBasics)
	if err != nil {
		return nil, ux.Fail(
			"read the level assets built into this binary",
			err,
			"This is a packaging bug rather than anything you did: please run `shellforge bug-report`.",
			docAnchorPackInvalid,
		)
	}
	return packFS, nil
}

// openSandbox provisions the sandbox and opens a session on it.
//
// The returned function closes the session and is safe to defer immediately.
//
// Resolution goes through internal/sandbox.Resolve rather than constructing
// docker.New directly, so `run` and `init` share one resolution path and
// neither picks a backend silently: Choice.Reason is printed either way.
func openSandbox(ctx context.Context, levelID string) (runtime.Runtime, runtime.Session, func(), error) {
	rt, choice, err := sandbox.Resolve(ctx, sandbox.Options{Want: sandbox.Auto})
	if err != nil {
		return nil, nil, nil, err
	}

	fmt.Fprintln(os.Stdout, choice.Reason)
	fmt.Fprintln(os.Stdout, "Preparing the sandbox. The first run builds the image, which takes a few minutes.")
	if err := rt.Provision(ctx, sandbox.Spec()); err != nil {
		return nil, nil, nil, ux.Fail(
			"provision the sandbox",
			err,
			fmt.Sprintf("Run `shellforge doctor` to check the backend this machine uses, fix anything it names, then run `shellforge run %s` again.", levelID),
			"sandbox-unhealthy",
		)
	}

	sess, err := rt.StartSession(ctx, runtime.SessionSpec{
		User:     sandboxUser,
		WorkDir:  sandboxHome,
		StateDir: setupStateDir(),
		Env:      map[string]string{"SF_STATE": setupStateDir()},
	})
	if err != nil {
		return nil, nil, nil, ux.Fail(
			"open a session on the sandbox",
			err,
			fmt.Sprintf("Run `shellforge sandbox status` to see whether the sandbox is running, then run `shellforge run %s` again.", levelID),
			"sandbox-missing",
		)
	}

	return rt, sess, func() {
		// Close is documented idempotent and safe to call more than once.
		_ = sess.Close()
	}, nil
}

// play is the level-agnostic run flow: set up, brief, attach, serve `check`,
// tear down.
//
// Every exit path tears the level down, through the SAME code as the normal
// exit. A cleanup path that only runs on the happy path is not a cleanup path,
// which is why the teardown defer is registered before Setup and runs on a
// context that cannot already be cancelled.
func play(ctx context.Context, opts runOptions, sess runtime.Session, lvl playable) error {
	// Colour is decided once, here, on the host's own stdout, and threaded into
	// every renderer. By the time a check reply is built there is no host file
	// to ask: the bytes are about to be handed to the sandbox.
	color := ux.ColorEnabled(os.Stdout)

	// Teardown runs on every exit path from here, including a panic, and on a
	// context that cannot already be cancelled. Using ctx directly would mean
	// a Ctrl-C at the process level skipped cleanup entirely, which is the one
	// moment cleanup matters most.
	//
	// Registered BEFORE Setup on purpose, so that a Setup which fails halfway
	// still has its partial state removed rather than left for the next run to
	// inherit.
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		if err := lvl.Teardown(cleanupCtx); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not remove the level world at %s: %v\n", lvl.Root(), err)
			return
		}
		fmt.Fprintf(os.Stdout, "Removed the level world at %s.\n", lvl.Root())
	}()

	if err := lvl.Setup(ctx); err != nil {
		// Setup errors already carry the right doc anchor: setup.Runner
		// attaches unsafe-level-root, setup-script-failed, level-pack-invalid
		// or sandbox-unhealthy depending on what actually went wrong.
		// Re-wrapping them all as level-state-corrupted, which this function
		// used to do, sent the learner to a page about a corrupt level when the
		// real problem was a bad path or a failing script. A wrong link is
		// worse than no link.
		var uxErr *ux.Error
		if errors.As(err, &uxErr) {
			return err
		}
		return ux.Fail(
			fmt.Sprintf("set the level %q up", lvl.LevelID()),
			err,
			fmt.Sprintf("Run `shellforge run %s` again. Setup tears the level world down before rebuilding it, so a half-finished attempt cannot wedge it.", lvl.LevelID()),
			"level-state-corrupted",
		)
	}

	reqPath := path.Join(lvl.StateDir(), "control.req")
	resPath := path.Join(lvl.StateDir(), "control.res")
	if err := prepareControlChannel(ctx, sess, reqPath, resPath); err != nil {
		return ux.Fail(
			"open the control channel into the sandbox",
			err,
			fmt.Sprintf("Run `shellforge run %s` again. If it keeps failing, run `docker rm -f shellforge-sandbox` and let the next run rebuild it.", lvl.LevelID()),
			"sandbox-unhealthy",
		)
	}

	// The briefing prints before the prompt appears, and before the host
	// terminal goes into raw mode, so a plain newline is still a newline
	// here. Anything written after Run starts needs a carriage return too,
	// which is what crlf in render_check.go is for.
	lvl.PrintBriefing(os.Stdout, color)

	// The learner starts in their home directory, not in the level root.
	//
	// A login shell puts you in your home directory, so that is where a
	// learner's mental model already is, and nav-01 teaches exactly that by
	// asking where you started. Starting in the level root instead would make
	// the first thing the campaign teaches be false, and it would mean `cd ~`
	// silently changed the answer to a question the level had already asked.
	//
	// Every level's own solution either uses a ~ path or cds first, so none of
	// them depends on this, and the golden harness runs solutions from the same
	// directory for the same reason.
	sandboxPTY, err := sess.Attach(ctx, runtime.AttachOpts{
		User:    sandboxUser,
		WorkDir: sandboxHome,
		Env: map[string]string{
			"SF_STATE": lvl.StateDir(),
			// Read by instrument.bash so the message it prints after the
			// state directory vanishes (a destructive command wiped it along
			// with everything else) names a real, copy-pasteable command
			// rather than a placeholder.
			"SF_LEVEL_ID": lvl.LevelID(),
		},
	})
	if err != nil {
		return ux.Fail(
			"start a shell inside the sandbox",
			err,
			fmt.Sprintf("Run `docker ps` to confirm the sandbox container is running, then run `shellforge run %s` again.", lvl.LevelID()),
			"sandbox-unhealthy",
		)
	}

	mux := pty.New(sandboxPTY, os.Stdin, os.Stdout)

	// runCtx bounds both helper goroutines to the lifetime of the shell.
	// Cancelling it is what unblocks the control channel's own blocking
	// read: the docker backend kills the sandbox-side process it started,
	// so no `cat` is left holding the FIFO open after the session ends.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// served closes when the control loop has returned, which is after it has
	// killed whatever it left running inside the sandbox. Waiting on it is what
	// makes the comment above true rather than merely likely.
	served := make(chan struct{})
	go func() {
		defer close(served)
		serveControlRequests(runCtx, sess, lvl.Responder(color), reqPath, resPath)
	}()
	go logCommandEvents(runCtx, mux.Events(), os.Stderr, opts.debug)

	runErr := mux.Run(runCtx)
	cancel()

	if !waitForControlLoop(served, controlDrainTimeout) {
		fmt.Fprintln(os.Stderr, "warning: the control channel did not stop cleanly, so a process may be left running inside the sandbox container. "+
			"It is harmless, and `docker rm -f shellforge-sandbox` clears it if you would rather not leave it there.")
	}

	if runErr != nil {
		if errors.Is(runErr, pty.ErrSignalled) {
			fmt.Fprintln(os.Stdout, "Interrupted.")
			return nil
		}
		return ux.Fail(
			fmt.Sprintf("run the level %q", lvl.LevelID()),
			runErr,
			"If your terminal is behaving oddly, run `reset`. Then run `shellforge run "+lvl.LevelID()+"` again.",
			"terminal-no-vt",
		)
	}

	fmt.Fprintln(os.Stdout, "Shell exited.")
	return nil
}

// prepareControlChannel creates the request and response FIFOs the in-sandbox
// shims expect at $SF_STATE/control.req and $SF_STATE/control.res.
//
// A stale pair can survive from an earlier session, because the container
// outlives any one `run`, so the pair is removed and recreated rather than
// reused. Both paths are derived from compile-time constants inside the
// sandbox state directory, never from level data, and the removal runs inside
// the sandbox through Session.Exec.
func prepareControlChannel(ctx context.Context, s runtime.Session, reqPath, resPath string) error {
	if err := run1(ctx, s, "remove any stale control channel", []string{"rm", "-f", "--", reqPath, resPath}); err != nil {
		return err
	}
	return run1(ctx, s, "create the control channel", []string{"mkfifo", "--", reqPath, resPath})
}

// run1 runs one argv inside the sandbox and turns a non-zero exit into an
// error, which is the right reading for the setup steps that use it: unlike a
// check, none of them has a meaningful failing outcome.
func run1(ctx context.Context, s runtime.Session, what string, argv []string) error {
	res, err := s.Exec(ctx, argv, runtime.ExecOpts{})
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("%s: %s exited %d: %s", what, argv[0], res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	return nil
}

// serveControlRequests answers `check` from inside the sandbox until ctx is
// cancelled.
//
// This is the host half of the control channel that images/bin/_sf-request
// already expects: the learner types `check`, the shim writes one request line
// to a FIFO, and the host writes the result back on a second FIFO. Verification
// has to run on the host, outside the learner's shell, so no game logic lives
// somewhere a learner could break during a permissions level.
//
// The loop returns ONLY when ctx is done. A failed reply is a skipped reply, not
// a reason to stop serving: the commonest cause is a learner pressing Ctrl-C
// during a check, and a loop that gave up then would leave `check` dead for the
// rest of the session.
//
// TODO(v0.2): a FIFO pair plus one blocking `docker exec` per direction is the
// simplest thing that works against the shims as written. It costs two
// sandbox-side processes per request and cannot serve two requests at once.
func serveControlRequests(ctx context.Context, s runtime.Session, responder controlResponder, reqPath, resPath string) {
	for {
		if ctx.Err() != nil {
			return
		}

		// No timeout on the read: waiting is the normal state, because the
		// next request only arrives when the learner decides to type
		// `check`. Cancelling ctx is what ends this.
		res, err := s.Exec(ctx, []string{"cat", "--", reqPath}, runtime.ExecOpts{})
		if err != nil || res.ExitCode != 0 {
			// The session is going away, or the FIFO is gone. Either way
			// there is nothing left to serve and nothing useful to say to
			// a learner whose shell has already ended.
			return
		}

		verb, args := parseControlRequest(string(res.Stdout))
		if verb == "" {
			continue
		}

		reply := responder.Reply(ctx, verb, args)

		replyCtx, cancelReply := context.WithTimeout(ctx, controlReplyTimeout)
		// tee opens resPath and copies stdin into it, so the reply travels
		// as an argv operand plus stdin with no shell anywhere in the path.
		_, err = s.Exec(replyCtx, []string{"tee", "--", resPath}, runtime.ExecOpts{Stdin: []byte(reply)})
		cancelReply()

		// Deliberately NOT a return on err. A write that timed out means the
		// shim went away, which is what Ctrl-C during a check looks like from
		// here. Only a cancelled run ends the loop.
		if err != nil && ctx.Err() != nil {
			return
		}
	}
}

// waitForControlLoop waits for the control loop to return, up to budget, and
// reports whether it did.
//
// Its own function so the bound is testable without a Docker daemon and a host
// pseudo terminal, neither of which the run flow can do without.
func waitForControlLoop(served <-chan struct{}, budget time.Duration) bool {
	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case <-served:
		return true
	case <-timer.C:
		return false
	}
}

// parseControlRequest splits one request line into its verb and the rest.
//
// The wire format is what images/bin/_sf-request writes: one line, the verb, a
// tab, then the arguments.
func parseControlRequest(line string) (verb, args string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", ""
	}
	verb, args, _ = strings.Cut(line, "\t")
	return strings.TrimSpace(verb), strings.TrimSpace(args)
}

// logCommandEvents drains the multiplexer's event channel for the whole
// session, and prints each event only when --log-level=debug asked for it.
//
// Draining unconditionally is not optional. The channel is buffered, and a
// consumer that stops reading makes the multiplexer start dropping events and
// warning about it on the learner's terminal mid-level.
//
// Every line ends "\r\n". Run has the host terminal in raw mode, which turns
// off the line discipline that would otherwise supply the carriage return, so
// a bare "\n" renders as a corrupted staircase line.
//
// CommandEvent.Raw is deliberately NOT logged. It is always empty today,
// pending the journal wiring, and it is the one field that would carry text the
// learner typed: a password on a command line lands there. Logging it needs a
// redaction decision that belongs with the journal work, not here.
func logCommandEvents(ctx context.Context, events <-chan pty.CommandEvent, w io.Writer, debug bool) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if !debug {
				continue
			}
			fmt.Fprintf(w, "debug: command exit=%d cwd=%q duration=%s\r\n",
				ev.ExitCode, ev.Cwd, ev.Duration.Round(time.Millisecond))
		}
	}
}
