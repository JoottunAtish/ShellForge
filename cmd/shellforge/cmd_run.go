package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
	"github.com/JoottunAtish/ShellForge/internal/pty"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/runtime/docker"
	"github.com/JoottunAtish/ShellForge/internal/sandbox"
)

// The `run` verb. On Day 1 it plays exactly one hardcoded level, `demo`, and
// exists to demonstrate the go/no-go gate: real bash in a real container, with
// the game watching every command and verifying real state.
//
// Day 2 replaces the hardcoded level with YAML and this file grows a level
// loader. Nothing here is designed to be the permanent shape of `run`.

const (
	// sandboxName is the container's identity and sandboxImage is the image
	// built from images/Containerfile. Both match `make image` and the
	// Makefile's IMAGE_NAME so that a locally built image is reused rather
	// than rebuilt.
	//
	// These are compile-time constants on purpose. A destroy path must
	// never take its target as a parameter, and although this command
	// destroys no container, keeping the identity constant here is what
	// makes that true by construction.
	sandboxName  = "shellforge-sandbox"
	sandboxImage = "shellforge-sandbox"

	// sandboxHome is the session's default working directory.
	//
	// It is deliberately NOT the level root. `docker exec -w` fails on a
	// directory that does not exist, and the level root does not exist
	// until Setup creates it, so a session defaulting to the level root
	// could not run the very calls that create it. The interactive shell
	// gets the level root through AttachOpts.WorkDir instead, once Setup
	// has run.
	sandboxHome = "/home/learner"

	// demoLevelID is the only level id `run` accepts on Day 1.
	demoLevelID = "demo"

	// controlReplyTimeout bounds a write to the response FIFO.
	//
	// The shim opens the response FIFO for reading immediately after
	// writing its request, so a write normally rendezvouses at once. If the
	// learner kills the shim in between, nothing ever opens the read end
	// and the write would block forever, wedging the control loop for the
	// rest of the session. A bounded write turns that into one skipped
	// reply instead.
	controlReplyTimeout = 10 * time.Second

	// cleanupTimeout bounds teardown after the shell has exited.
	cleanupTimeout = 30 * time.Second
)

// runOptions is what `run` parsed out of its arguments.
type runOptions struct {
	levelID string
	debug   bool
}

// cmdRun implements `shellforge run <level-id>`.
func cmdRun(ctx context.Context, args []string) error {
	opts, err := parseRunArgs(args)
	if err != nil {
		return err
	}
	if opts.levelID != demoLevelID {
		return ux.Fail(
			fmt.Sprintf("play level %q", opts.levelID),
			nil,
			"Only `shellforge run demo` works today. Levels load from YAML on Day 2 of the build plan; see PROGRESS.md.",
			"",
		)
	}
	if err := checkInteractiveShellSupported(); err != nil {
		return err
	}
	return runDemo(ctx, opts)
}

// checkInteractiveShellSupported refuses, up front, on a host that cannot give
// the learner an interactive shell at all.
//
// The Docker backend attaches by allocating a pseudo terminal on the HOST with
// creack/pty, and that package's Windows implementation returns ErrUnsupported
// unconditionally: there is no ConPTY path in it. So `run demo` on a Windows
// host gets all the way through an image build and a level setup and then fails
// at the last step with "docker exec -it: unsupported", which reads like a
// Docker problem and is not one.
//
// The runtime contract suite does not catch this, because it has no Attach
// assertion at all, which is why it passes on Windows.
//
// Refusing here rather than at Attach saves several minutes of image build
// before an error the user cannot act on. Windows gets a real answer on Day 3
// with WslRuntime; until then, running from inside WSL is the way, and Docker
// Desktop's WSL integration puts the same daemon on both sides.
func checkInteractiveShellSupported() error {
	if goruntime.GOOS != "windows" {
		return nil
	}
	return ux.Fail(
		"open an interactive sandbox shell on Windows",
		nil,
		"Run this from inside WSL instead: open your WSL distribution, `cd` to this repository, then `go build -o bin/shellforge ./cmd/shellforge && ./bin/shellforge run demo`. Docker Desktop's WSL integration shares the same daemon, so the sandbox image is not rebuilt.",
		"windows-needs-wsl",
	)
}

// parseRunArgs reads the level id and the one flag `run` understands today.
//
// This is a hand-rolled parser because the dispatcher it plugs into is also
// hand-rolled. Day 1 Session A replaces both with cobra.
func parseRunArgs(args []string) (runOptions, error) {
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
				fmt.Sprintf("You named more than one level (%q and %q). Run `shellforge run demo`.", opts.levelID, a),
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
			"Name a level: `shellforge run demo`.",
			"",
		)
	}
	return opts, nil
}

// runDemo provisions the sandbox, materializes the demo level, hands the
// learner a real bash, and serves `check` until they exit.
//
// Every exit path closes the session and tears the level world down. The
// ordering is load bearing and is documented inline at each step.
func runDemo(ctx context.Context, opts runOptions) error {
	level := sandbox.Demo()

	rt, err := docker.New(sandboxName, sandboxImage)
	if err != nil {
		// docker.New already returns a ux.Error for a missing binary.
		return err
	}

	fmt.Fprintln(os.Stdout, "Preparing the sandbox. The first run builds the image, which takes a few minutes.")
	if err := rt.Provision(ctx, runtime.ImageSpec{Name: sandboxImage}); err != nil {
		return ux.Fail(
			"provision the sandbox",
			err,
			"Run `docker info` to confirm the daemon is running, then run `shellforge run demo` again.",
			"sandbox-unhealthy",
		)
	}

	sess, err := rt.StartSession(ctx, runtime.SessionSpec{
		User:     level.User(),
		WorkDir:  sandboxHome,
		StateDir: level.StateDir(),
		Env:      map[string]string{"SF_STATE": level.StateDir()},
	})
	if err != nil {
		return ux.Fail(
			"open a session on the sandbox",
			err,
			"Run `docker ps` to see whether the sandbox container is running, then run `shellforge run demo` again.",
			"sandbox-missing",
		)
	}
	defer func() {
		// Close is documented idempotent and safe to call more than once.
		_ = sess.Close()
	}()

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
		if err := level.Teardown(cleanupCtx, sess); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not remove the level world at %s: %v\n", level.Root, err)
			return
		}
		fmt.Fprintf(os.Stdout, "Removed the level world at %s.\n", level.Root)
	}()

	if err := level.Setup(ctx, sess); err != nil {
		return ux.Fail(
			"set the demo level up",
			err,
			"Run `shellforge run demo` again. Setup tears the level world down before rebuilding it, so a half-finished attempt cannot wedge it.",
			"level-state-corrupted",
		)
	}

	reqPath := path.Join(level.StateDir(), "control.req")
	resPath := path.Join(level.StateDir(), "control.res")
	if err := prepareControlChannel(ctx, sess, reqPath, resPath); err != nil {
		return ux.Fail(
			"open the control channel into the sandbox",
			err,
			"Run `shellforge run demo` again. If it keeps failing, run `docker rm -f shellforge-sandbox` and let the next run rebuild it.",
			"sandbox-unhealthy",
		)
	}

	// The briefing prints before the prompt appears, and before the host
	// terminal goes into raw mode, so a plain newline is still a newline
	// here. Anything written after Run starts needs a carriage return too.
	printBriefing(os.Stdout, level)

	sandboxPTY, err := sess.Attach(ctx, runtime.AttachOpts{
		User:    level.User(),
		WorkDir: level.Root,
		Env:     map[string]string{"SF_STATE": level.StateDir()},
	})
	if err != nil {
		return ux.Fail(
			"start a shell inside the sandbox",
			err,
			"Run `docker ps` to confirm the sandbox container is running, then run `shellforge run demo` again.",
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

	go serveControlRequests(runCtx, sess, level, reqPath, resPath)
	go logCommandEvents(runCtx, mux.Events(), os.Stderr, opts.debug)

	runErr := mux.Run(runCtx)
	cancel()

	if runErr != nil {
		if errors.Is(runErr, pty.ErrSignalled) {
			fmt.Fprintln(os.Stdout, "Interrupted.")
			return nil
		}
		return ux.Fail(
			"run the demo level",
			runErr,
			"If your terminal is behaving oddly, run `reset`. Then run `shellforge run demo` again.",
			"terminal-no-vt",
		)
	}

	fmt.Fprintln(os.Stdout, "Shell exited.")
	return nil
}

// printBriefing writes the level briefing and objective.
//
// Deliberately plain. Making `check` and the briefing pretty is out of scope
// for the gate; being correct is not.
func printBriefing(w io.Writer, level sandbox.DemoLevel) {
	const rule = "----------------------------------------------------------------------"
	fmt.Fprintf(w, "\n%s\n%s\n%s\n\n", rule, level.Briefing, rule)
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

// serveControlRequests answers `check` from inside the sandbox until runCtx is
// cancelled.
//
// This is the host half of the control channel that images/bin/_sf-request
// already expects: the learner types `check`, the shim writes one request line
// to a FIFO, and the host writes the result back on a second FIFO. Verification
// has to run on the host, outside the learner's shell, so no game logic lives
// somewhere a learner could break during a permissions level.
//
// TODO(v0.2): a FIFO pair plus one blocking `docker exec` per direction is the
// simplest thing that works against the shims as written. It costs two
// sandbox-side processes per request and cannot serve two requests at once.
// Day 2 owns the real control channel; if it needs concurrency or a richer
// message than one tab-separated line, this is the piece to replace.
func serveControlRequests(ctx context.Context, s runtime.Session, level sandbox.DemoLevel, reqPath, resPath string) {
	for {
		if ctx.Err() != nil {
			return
		}

		// No timeout on the read: waiting is the normal state, because the
		// next request only arrives when the learner decides to type
		// `check`. Cancelling runCtx is what ends this.
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

		reply := handleControlVerb(ctx, s, level, verb, args)
		replyCtx, cancel := context.WithTimeout(ctx, controlReplyTimeout)
		// tee opens resPath and copies stdin into it, so the reply travels
		// as an argv operand plus stdin with no shell anywhere in the path.
		_, err = s.Exec(replyCtx, []string{"tee", "--", resPath}, runtime.ExecOpts{Stdin: []byte(reply)})
		cancel()
		if err != nil && ctx.Err() != nil {
			return
		}
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

// handleControlVerb answers one request. The reply is written verbatim to the
// learner's terminal by the shim, so it ends in a newline.
func handleControlVerb(ctx context.Context, s runtime.Session, level sandbox.DemoLevel, verb, _ string) string {
	switch verb {
	case "check":
		passed, message, err := level.Check(ctx, s)
		if err != nil {
			return fmt.Sprintf("check could not run: %v\n", err)
		}
		if passed {
			return fmt.Sprintf("PASS: %s\n", message)
		}
		return fmt.Sprintf("FAIL: %s\n", message)
	case "brief":
		return level.Briefing + "\n"
	case "hint", "reset":
		return fmt.Sprintf("`%s` is not built yet. It lands later in the build plan; see PROGRESS.md.\n", verb)
	default:
		return fmt.Sprintf("unknown request %q\n", verb)
	}
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
// a bare "\n" renders as a staircase.
//
// CommandEvent.Raw is deliberately NOT logged. It is always empty today,
// pending #51, and it is the one field that would carry text the learner
// typed: a password on a command line lands there. Logging it needs a
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
