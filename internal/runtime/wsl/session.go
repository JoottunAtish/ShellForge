package wsl

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/creack/pty"

	"github.com/JoottunAtish/ShellForge/internal/platform"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

// sandboxRoot is the prefix every in-sandbox path this package touches must
// resolve under. It matches FileEntry.Path and ExecOpts.WorkDir's doc
// comments. It uses path, not filepath, because every value it is compared
// against is a Linux path inside the sandbox, never a host path: filepath
// on a Windows build of this binary would turn a leading "/" into "\" and
// silently disable the whole guard.
const sandboxRoot = "/home/learner"

// ownerPattern allows "user" or "user:group", where each half is an
// identifier. FileEntry.Owner reaches an in-sandbox tar entry this code
// assembles, so it is validated here before that happens.
var ownerPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*(:[a-zA-Z0-9][a-zA-Z0-9_-]*)?$`)

// ownerIDs maps the account names the sandbox image ships to their numeric
// ids, so PushFiles can set tar Uid/Gid directly and complete in a single
// wsl.exe invocation rather than a second follow-up chown, matching the
// argv construction the plan pins. root and learner are the only accounts
// images/Containerfile creates.
var ownerIDs = map[string]int{
	"root":    0,
	"learner": 1000,
}

// wslSession is one open connection to a provisioned WSL distribution.
type wslSession struct {
	rt   *wslRuntime
	spec runtime.SessionSpec
}

var _ runtime.Session = (*wslSession)(nil)

// validateSandboxPath rejects, rather than adjusts, a path that does not
// resolve under sandboxRoot once cleaned. An empty path is accepted here: it
// means "use the session default" on every field that calls this, and the
// caller decides what that default is.
func validateSandboxPath(p string) error {
	if p == "" {
		return nil
	}
	clean := path.Clean(p)
	if !path.IsAbs(clean) {
		return fmt.Errorf("wsl: path %q must be absolute", p)
	}
	if clean != sandboxRoot && !strings.HasPrefix(clean, sandboxRoot+"/") {
		return fmt.Errorf("wsl: path %q does not resolve under %s", p, sandboxRoot)
	}
	return nil
}

func (s *wslSession) effectiveUser(override string) (string, error) {
	user := override
	if user == "" {
		user = s.spec.User
	}
	if user != "" && !platform.ValidIdentifier(user) {
		return "", fmt.Errorf("wsl: user %q does not match %s", user, platform.IdentifierPattern)
	}
	return user, nil
}

func (s *wslSession) effectiveWorkDir(override string) (string, error) {
	workdir := override
	if workdir == "" {
		workdir = s.spec.WorkDir
	}
	if err := validateSandboxPath(workdir); err != nil {
		return "", err
	}
	return workdir, nil
}

// sortedEnvKV returns "KEY=VALUE" for every entry of env, sorted by key, so
// the resulting argv is deterministic.
func sortedEnvKV(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	kv := make([]string, 0, len(keys))
	for _, k := range keys {
		kv = append(kv, k+"="+env[k])
	}
	return kv
}

// execPIDMarkerPrefix tags the line the exec wrapper script prints to
// stderr, ahead of anything the caller's own argv writes.
const execPIDMarkerPrefix = "SFPID:"

// execWrapperScript is the fixed-literal /bin/sh -c body every non-
// interactive Exec runs. It receives the working directory as $1, a
// positional argument rather than something interpolated into the script
// text, cds into it when non-empty, announces its own PID as the first
// line of stderr, then execs the remaining arguments. Printing the PID and
// then exec-ing, rather than forking, means the line this reports is the
// caller's own argv's PID, not a wrapper shell's: killSandboxProcess needs
// that PID to be the process actually doing the work.
const execWrapperScript = `d="$1"; shift; if [ -n "$d" ]; then cd -- "$d" || exit 127; fi; printf '` + execPIDMarkerPrefix + `%d\n' "$$" >&2; exec "$@"`

// execArgv builds the argv for one non-interactive wsl.exe --exec
// invocation. user is never omitted: --exec without -u runs as the
// distribution's own default user, which would make ExecOpts.User a lie.
// Environment is applied inside the sandbox through /usr/bin/env, sorted,
// so the argv is deterministic; the working directory rides in the fixed
// wrapper script's positional argument rather than in a --cd flag, because
// --cd needs a wsl.exe version newer than this ticket wants to require.
func execArgv(distro, user, workdir string, env map[string]string, command []string) []string {
	inner := command
	if len(env) > 0 {
		inner = append(append([]string{"/usr/bin/env"}, sortedEnvKV(env)...), command...)
	}
	argv := []string{"wsl.exe", "-d", distro}
	if user != "" {
		argv = append(argv, "-u", user)
	}
	argv = append(argv, "--exec", "/bin/sh", "-c", execWrapperScript, "--", workdir)
	return append(argv, inner...)
}

// Exec runs argv inside the distribution without a shell and waits for it
// to finish.
func (s *wslSession) Exec(ctx context.Context, argv []string, opts runtime.ExecOpts) (runtime.ExecResult, error) {
	if len(argv) == 0 {
		return runtime.ExecResult{}, errors.New("wsl: Exec called with an empty argv")
	}
	user, err := s.effectiveUser(opts.User)
	if err != nil {
		return runtime.ExecResult{}, err
	}
	workdir, err := s.effectiveWorkDir(opts.WorkDir)
	if err != nil {
		return runtime.ExecResult{}, err
	}

	full := execArgv(s.rt.distro, user, workdir, opts.Env, argv)

	execCtx := ctx
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	start := time.Now()
	stdout, stderr, code, runErr := s.rt.run.run(execCtx, full, opts.Stdin)
	duration := time.Since(start)
	pid, cleanStderr := parseExecPIDMarker(stderr)

	if runErr != nil {
		if pid != "" {
			s.killSandboxProcess(pid)
		}
		if ctx.Err() != nil {
			return runtime.ExecResult{}, ctx.Err()
		}
		if opts.Timeout > 0 && execCtx.Err() != nil {
			return runtime.ExecResult{ExitCode: -1, TimedOut: true, Duration: duration}, nil
		}
		return runtime.ExecResult{}, fmt.Errorf("wsl exec: %w", runErr)
	}

	return runtime.ExecResult{Stdout: stdout, Stderr: cleanStderr, ExitCode: code, Duration: duration}, nil
}

// parseExecPIDMarker splits the marker execWrapperScript prepends off of
// stderr, so a caller of Exec sees exactly what argv itself wrote. A
// missing or malformed marker is reported as no PID rather than an error.
func parseExecPIDMarker(stderr []byte) (pid string, rest []byte) {
	nl := bytes.IndexByte(stderr, '\n')
	if nl < 0 {
		return "", stderr
	}
	line := stderr[:nl]
	if !bytes.HasPrefix(line, []byte(execPIDMarkerPrefix)) {
		return "", stderr
	}
	digits := line[len(execPIDMarkerPrefix):]
	if len(digits) == 0 {
		return "", stderr
	}
	for _, b := range digits {
		if b < '0' || b > '9' {
			return "", stderr
		}
	}
	return string(digits), stderr[nl+1:]
}

// killSandboxProcess best-effort kills pid inside the distribution. Its own
// outcome is not reported: it only ever runs after Exec's own invocation
// has already failed, so the caller already has the real error to act on.
func (s *wslSession) killSandboxProcess(pid string) {
	_, _, _, _ = s.rt.run.run(context.Background(), []string{"wsl.exe", "-d", s.rt.distro, "-u", "root", "--exec", "/bin/kill", "-9", pid}, nil)
}

// defaultAttachCommand is the instrumented bash invocation, matching the rc
// file the sandbox image installs at /opt/shellforge/rc/instrument.bash.
func defaultAttachCommand() []string {
	return []string{"/bin/bash", "--rcfile", "/opt/shellforge/rc/instrument.bash", "-i"}
}

// attachArgv builds the argv for an interactive wsl.exe --exec invocation.
// Unlike execArgv, it carries no PID marker (there is nothing to cancel and
// clean up: the caller drives the PTY directly and kills the local wsl.exe
// client on Close) and, when workdir and env are both empty, execs the
// command directly with no wrapper at all.
func attachArgv(distro, user, workdir string, env map[string]string, command []string) []string {
	argv := []string{"wsl.exe", "-d", distro}
	if user != "" {
		argv = append(argv, "-u", user)
	}
	argv = append(argv, "--exec")

	switch {
	case workdir == "":
		if len(env) > 0 {
			argv = append(argv, "/usr/bin/env")
			argv = append(argv, sortedEnvKV(env)...)
		}
		return append(argv, command...)
	default:
		argv = append(argv, "/bin/sh", "-c", `d="$1"; shift; if [ -n "$d" ]; then cd -- "$d" || exit 127; fi; exec "$@"`, "--", workdir)
		if len(env) > 0 {
			argv = append(argv, "/usr/bin/env")
			argv = append(argv, sortedEnvKV(env)...)
		}
		return append(argv, command...)
	}
}

// Attach starts an interactive process on a pseudo terminal and returns the
// handle the PTY multiplexer drives.
func (s *wslSession) Attach(ctx context.Context, opts runtime.AttachOpts) (runtime.PTY, error) {
	user, err := s.effectiveUser(opts.User)
	if err != nil {
		return nil, err
	}
	workdir, err := s.effectiveWorkDir(opts.WorkDir)
	if err != nil {
		return nil, err
	}
	command := opts.Command
	if len(command) == 0 {
		command = defaultAttachCommand()
	}

	argv := attachArgv(s.rt.distro, user, workdir, opts.Env, command)

	real, ok := s.rt.run.(execRunner)
	if !ok {
		return nil, errors.New("wsl: Attach needs the real wsl.exe binary and cannot run against a fake runner")
	}

	// #nosec G204 -- real.bin is exec.LookPath("wsl.exe") resolved once in
	// New and never variable from config; argv[1:] is the vector this
	// method builds above from allowlist-validated fields (user, workdir,
	// distro) and the caller's own Command, never a shell string.
	cmd := exec.CommandContext(ctx, real.bin, argv[1:]...)
	f, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("wsl.exe --exec: %w", err)
	}
	return &wslPTY{file: f, cmd: cmd}, nil
}

// stripCR removes the carriage return from every CRLF pair, leaving a lone
// CR (no matching LF) untouched. An unconditional strip would corrupt a
// binary level asset containing byte 0x0d.
func stripCR(content []byte) []byte {
	return bytes.ReplaceAll(content, []byte("\r\n"), []byte("\n"))
}

// parseOwner resolves a FileEntry.Owner such as "learner:learner" or
// "root" to the numeric uid and gid PushFiles writes into the tar. An
// empty owner defaults to uid 0, gid 0 (root), matching the tar entries
// this package writes for the directories it creates on the way to a file.
// A name outside ownerIDs is refused: root and learner are the only
// accounts the sandbox image ships, and guessing at a numeric id for
// anything else would fail silently rather than fail closed.
func parseOwner(owner string) (uid, gid int, err error) {
	if owner == "" {
		return 0, 0, nil
	}
	if !ownerPattern.MatchString(owner) {
		return 0, 0, fmt.Errorf("wsl: owner %q does not match %s", owner, ownerPattern.String())
	}
	user, group, hasGroup := strings.Cut(owner, ":")
	if !hasGroup {
		group = user
	}
	uid, ok := ownerIDs[user]
	if !ok {
		return 0, 0, fmt.Errorf("wsl: owner %q: unknown account %q", owner, user)
	}
	gid, ok = ownerIDs[group]
	if !ok {
		return 0, 0, fmt.Errorf("wsl: owner %q: unknown group %q", owner, group)
	}
	return uid, gid, nil
}

// pushEntry is one validated FileEntry, ready to become a tar entry.
type pushEntry struct {
	path    string
	content []byte
	mode    os.FileMode
	uid     int
	gid     int
}

// buildPushTar builds the tar `tar -xf - -C /` extracts at the
// distribution's filesystem root, and returns it. It emits an entry for
// every file and for every ancestor directory strictly below sandboxRoot,
// deliberately emitting nothing at or above sandboxRoot for the same
// reason the docker sibling's buildPushTar does not: extracting a
// directory entry for a path GNU tar's extraction reassigns the ownership
// and mode of an existing directory, and home/ and home/learner/ already
// exist and must keep learner's ownership. Every entry carries its final
// uid, gid, and mode directly, so a single wsl.exe invocation, not a
// follow-up chmod/chown exec, is enough.
func buildPushTar(entries []pushEntry) ([]byte, error) {
	dirSet := make(map[string]bool)
	for _, e := range entries {
		for dir := path.Dir(e.path); strings.HasPrefix(dir, sandboxRoot+"/"); dir = path.Dir(dir) {
			dirSet[dir] = true
		}
	}
	dirs := make([]string, 0, len(dirSet))
	for d := range dirSet {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, d := range dirs {
		if err := tw.WriteHeader(&tar.Header{
			Name:     strings.TrimPrefix(d, "/") + "/",
			Typeflag: tar.TypeDir,
			Mode:     0o755,
		}); err != nil {
			return nil, fmt.Errorf("wsl: build the push archive: %w", err)
		}
	}
	// Entries stay in manifest order so that two entries at the same path
	// resolve the way FileManifest documents: the later one wins.
	for _, e := range entries {
		if err := tw.WriteHeader(&tar.Header{
			Name:     strings.TrimPrefix(e.path, "/"),
			Typeflag: tar.TypeReg,
			Mode:     int64(e.mode.Perm()),
			Size:     int64(len(e.content)),
			Uid:      e.uid,
			Gid:      e.gid,
		}); err != nil {
			return nil, fmt.Errorf("wsl: build the push archive: %w", err)
		}
		if _, err := tw.Write(e.content); err != nil {
			return nil, fmt.Errorf("wsl: build the push archive: %w", err)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("wsl: build the push archive: %w", err)
	}
	return buf.Bytes(), nil
}

// PushFiles materializes every entry in m inside the distribution. It
// builds one tar in memory, strips the CR of every CRLF pair while
// building it, and pipes it to a single `/bin/tar -xf - -C /` invocation
// run as root.
func (s *wslSession) PushFiles(ctx context.Context, m runtime.FileManifest) error {
	if len(m.Files) == 0 {
		return nil
	}

	entries := make([]pushEntry, 0, len(m.Files))
	for _, f := range m.Files {
		if f.Path == "" {
			return errors.New("wsl: PushFiles: a FileEntry has an empty Path")
		}
		if err := validateSandboxPath(f.Path); err != nil {
			return err
		}
		uid, gid, err := parseOwner(f.Owner)
		if err != nil {
			return err
		}
		entries = append(entries, pushEntry{
			path:    path.Clean(f.Path),
			content: stripCR(f.Content),
			mode:    f.Mode,
			uid:     uid,
			gid:     gid,
		})
	}

	archive, err := buildPushTar(entries)
	if err != nil {
		return err
	}

	_, stderr, code, err := s.rt.run.run(ctx, []string{"wsl.exe", "-d", s.rt.distro, "-u", "root", "--exec", "/bin/tar", "-xf", "-", "-C", "/"}, archive)
	if err != nil {
		return fmt.Errorf("wsl tar (push): %w", err)
	}
	if code != 0 {
		return fmt.Errorf("wsl tar (push) exited %d: %s", code, stderr)
	}
	return nil
}

// PullFile reads an absolute path inside the distribution and returns its
// content, via `/bin/cat`.
func (s *wslSession) PullFile(ctx context.Context, p string) ([]byte, error) {
	if p == "" {
		return nil, errors.New("wsl: PullFile: empty path")
	}
	if err := validateSandboxPath(p); err != nil {
		return nil, err
	}

	stdout, stderr, code, err := s.rt.run.run(ctx, []string{"wsl.exe", "-d", s.rt.distro, "-u", "root", "--exec", "/bin/cat", "--", p}, nil)
	if err != nil {
		return nil, fmt.Errorf("wsl cat (pull) %s: %w", p, err)
	}
	if code != 0 {
		return nil, fmt.Errorf("wsl cat (pull) %s exited %d: %s", p, code, stderr)
	}
	return stdout, nil
}

// Close releases the session. A wslSession holds no host-side resource
// beyond one-shot wsl.exe invocations that have already completed by the
// time any method returns, so there is nothing to release.
func (s *wslSession) Close() error { return nil }

// wslPTY wraps the master side of the pseudo terminal creack/pty allocated
// around a `wsl.exe --exec` child.
type wslPTY struct {
	file *os.File
	cmd  *exec.Cmd
}

var _ runtime.PTY = (*wslPTY)(nil)

func (p *wslPTY) Read(b []byte) (int, error)  { return p.file.Read(b) }
func (p *wslPTY) Write(b []byte) (int, error) { return p.file.Write(b) }
func (p *wslPTY) Close() error                { return p.file.Close() }
func (p *wslPTY) Wait() error                 { return p.cmd.Wait() }
