package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
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
// comments.
const sandboxRoot = "/home/learner"

// ownerPattern allows "user" or "user:group", where each half is an
// identifier. FileEntry.Owner is not independently allowlisted by the
// runtime package's doc comments, and it reaches an in-sandbox shell script
// this code assembles, so it is validated here before that happens.
var ownerPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*(:[a-zA-Z0-9][a-zA-Z0-9_-]*)?$`)

// dockerSession is one open connection to a provisioned container.
type dockerSession struct {
	rt   *dockerRuntime
	spec runtime.SessionSpec
}

var _ runtime.Session = (*dockerSession)(nil)

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
		return fmt.Errorf("docker: path %q must be absolute", p)
	}
	if clean != sandboxRoot && !strings.HasPrefix(clean, sandboxRoot+"/") {
		return fmt.Errorf("docker: path %q does not resolve under %s", p, sandboxRoot)
	}
	return nil
}

func (s *dockerSession) effectiveUser(override string) (string, error) {
	user := override
	if user == "" {
		user = s.spec.User
	}
	if user != "" && !platform.ValidIdentifier(user) {
		return "", fmt.Errorf("docker: user %q does not match %s", user, platform.IdentifierPattern)
	}
	return user, nil
}

func (s *dockerSession) effectiveWorkDir(override string) (string, error) {
	workdir := override
	if workdir == "" {
		workdir = s.spec.WorkDir
	}
	if err := validateSandboxPath(workdir); err != nil {
		return "", err
	}
	return workdir, nil
}

func sortedEnvArgs(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	args := make([]string, 0, len(keys)*2)
	for _, k := range keys {
		args = append(args, "-e", k+"="+env[k])
	}
	return args
}

// execArgv builds the argv for a non-interactive docker exec. "--" ends
// docker's own options before the container name and the command, per the
// security skill: that is what keeps a command whose first character
// happens to be "-" from being read as another docker flag.
//
// withStdin adds "-i", and it is required for ExecOpts.Stdin to work at all.
// Setting cmd.Stdin on the local docker process is not enough: without -i,
// `docker exec` does not connect the container process's standard input to
// anything, so the bytes are read by nobody and the process sees an immediate
// EOF. Measured against a live container, not inferred: `echo x | docker exec
// c tee /tmp/probe` leaves /tmp/probe empty, and the same command with -i
// writes "x". That silently discarded every ExecOpts.Stdin this backend was
// ever given, and the contract suite had no assertion covering the field, so
// nothing caught it until the demo level's control channel replied with zero
// bytes and `check` printed nothing at all.
//
// It is added only when there is stdin to send, because there is nothing for
// it to do otherwise: execRunner.run leaves cmd.Stdin nil when the caller
// passed no bytes, and os/exec then connects the child to os.DevNull, so -i
// would allocate a standard input the container process sees EOF on
// immediately.
//
// Be clear about what that reasoning is NOT, because an earlier version of
// this comment got it wrong and the wrong version is the more alarming one. An
// unconditional -i could not have reached the learner's terminal. os/exec
// never substitutes this process's own os.Stdin for a nil cmd.Stdin, so a
// check running behind an attached shell was never able to consume the
// learner's keystrokes, and nobody should re-derive a security argument from
// that. The conditional is a tidiness choice, not a safety control.
func (s *dockerSession) execArgv(user, workdir string, env map[string]string, command []string, withStdin bool) []string {
	argv := []string{"docker", "exec"}
	if withStdin {
		argv = append(argv, "-i")
	}
	if user != "" {
		argv = append(argv, "-u", user)
	}
	if workdir != "" {
		argv = append(argv, "-w", workdir)
	}
	argv = append(argv, sortedEnvArgs(env)...)
	argv = append(argv, "--", s.rt.name)
	argv = append(argv, command...)
	return argv
}

// Exec runs argv inside the container without a shell and waits for it to
// finish.
func (s *dockerSession) Exec(ctx context.Context, argv []string, opts runtime.ExecOpts) (runtime.ExecResult, error) {
	if len(argv) == 0 {
		return runtime.ExecResult{}, errors.New("docker: Exec called with an empty argv")
	}
	user, err := s.effectiveUser(opts.User)
	if err != nil {
		return runtime.ExecResult{}, err
	}
	workdir, err := s.effectiveWorkDir(opts.WorkDir)
	if err != nil {
		return runtime.ExecResult{}, err
	}

	full := s.execArgv(user, workdir, opts.Env, wrapWithPIDMarker(argv), len(opts.Stdin) > 0)

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
		return runtime.ExecResult{}, fmt.Errorf("docker exec: %w", runErr)
	}

	return runtime.ExecResult{Stdout: stdout, Stderr: cleanStderr, ExitCode: code, Duration: duration}, nil
}

// execPIDMarkerPrefix tags the line wrapWithPIDMarker's shell prepends to
// stderr, ahead of anything argv itself writes.
const execPIDMarkerPrefix = "SFPID:"

// wrapWithPIDMarker rewrites argv so the sandbox-side process announces its
// own PID as the first line of stderr before anything else runs, then execs
// straight into argv so the PID it reports is argv[0]'s, not a wrapper
// shell's.
//
// This exists because killing the local docker client, which is all a
// cancelled context lets exec.CommandContext do on its own, does not stop
// what docker exec started inside the container: unlike docker run and
// docker attach, docker exec has no --sig-proxy, so the daemon is never
// told to act. Exec uses the PID this reports to send an explicit kill into
// the sandbox once its own invocation has failed, so a cancelled or timed
// out call does not orphan a process there.
func wrapWithPIDMarker(argv []string) []string {
	wrapped := []string{"/bin/sh", "-c", `printf '` + execPIDMarkerPrefix + `%d\n' "$$" >&2; exec "$@"`, "--"}
	return append(wrapped, argv...)
}

// parseExecPIDMarker splits the marker wrapWithPIDMarker prepends off of
// stderr, so a caller of Exec sees exactly what argv itself wrote and
// nothing this package added. A missing or malformed marker (the process
// was killed before it could write one, for instance) is reported as no
// PID rather than an error: Exec still has a real result or a real error
// to return either way, and a marker that never arrived just means there is
// nothing to kill, not that something is wrong with stderr.
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

// killSandboxProcess best-effort kills pid inside the container. Its own
// outcome is not reported: it only ever runs after Exec's own invocation
// has already failed, so the caller already has the real error to act on,
// and there is nothing more useful to tell them about a cleanup step.
// pid is digits-only, checked by parseExecPIDMarker, and this still reaches
// the sandbox as an argv element rather than a shell string regardless.
func (s *dockerSession) killSandboxProcess(pid string) {
	_, _, _, _ = s.rt.run.run(context.Background(), []string{"docker", "exec", "--", s.rt.name, "kill", "-9", pid}, nil)
}

// defaultAttachCommand is what AttachOpts.Command's doc comment calls "the
// instrumented bash invocation", matching the rc file the sandbox image
// installs at /opt/shellforge/rc/instrument.bash.
func defaultAttachCommand() []string {
	return []string{"/bin/bash", "--rcfile", "/opt/shellforge/rc/instrument.bash", "-i"}
}

// Attach starts an interactive process on a pseudo terminal and returns the
// handle the PTY multiplexer drives.
func (s *dockerSession) Attach(ctx context.Context, opts runtime.AttachOpts) (runtime.PTY, error) {
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

	argv := []string{"docker", "exec", "-it"}
	if user != "" {
		argv = append(argv, "-u", user)
	}
	if workdir != "" {
		argv = append(argv, "-w", workdir)
	}
	argv = append(argv, sortedEnvArgs(opts.Env)...)
	argv = append(argv, "--", s.rt.name)
	argv = append(argv, command...)

	real, ok := s.rt.run.(execRunner)
	if !ok {
		return nil, errors.New("docker: Attach needs the real docker binary and cannot run against a fake runner")
	}

	// #nosec G204 -- real.bin is exec.LookPath("docker") resolved once in
	// New and never variable from config; argv[1:] is the vector this
	// method builds above from allowlist-validated fields (user, workdir,
	// name) and the caller's own Command, never a shell string.
	cmd := exec.CommandContext(ctx, real.bin, argv[1:]...)
	f, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("docker exec -it: %w", err)
	}
	return &dockerPTY{file: f, cmd: cmd}, nil
}

// stripCR removes the carriage return from every CRLF pair, leaving a lone
// CR (no matching LF) untouched. See runtimetest's TODO(v0.2) on lone CR and
// binary content: freezing that rule to pass a test would risk silently
// corrupting a binary level asset, so it is deliberately left alone here.
func stripCR(content []byte) []byte {
	return bytes.ReplaceAll(content, []byte("\r\n"), []byte("\n"))
}

// shQuote wraps s in single quotes so it is safe to interpolate into the
// bash script PushFiles assembles for the chmod/chown follow-up, even
// though every value reaching it has already been validated against an
// allowlist. Single-quoting is the second, independent layer, not a
// substitute for the allowlist.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// pushEntry is one validated FileEntry, ready to become a tar entry and a
// line of the chmod/chown script.
type pushEntry struct {
	path    string
	content []byte
	owner   string
	mode    fs.FileMode
}

// buildPushTar builds the tar that `docker cp - <name>:/` extracts at the
// container root, and reports the directories it creates.
//
// It emits an entry for every file and for every ancestor directory
// strictly below sandboxRoot, and deliberately emits nothing at or above
// sandboxRoot. That omission is the whole point of building the archive by
// hand rather than staging a mirrored tree on the host and copying it:
// `docker cp` overwrites the ownership and mode of directories that
// already exist, so a staged tree containing home/ and home/learner/
// silently reassigns /home and /home/learner to whatever uid and mode the
// host staging had. On a Linux host that is the uid of whoever ran the
// process, which owns nothing inside the container, and the sandbox user
// is then locked out of its own home directory. Verified directly: a copy
// of such a tree turned /home from learner:learner 0755 into 1001:1001
// 0750, after which the learner user could not stat anything beneath it.
//
// Every entry is written with uid and gid 0 so the result does not depend
// on who ran the process. That makes this path behave identically on a
// Linux host and on Docker Desktop for Windows, which has no POSIX
// ownership to preserve and silently substitutes root and 0755. That
// difference is exactly why the staged-tree version passed on Windows and
// failed on Linux CI.
func buildPushTar(entries []pushEntry) ([]byte, []string, error) {
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
	// Lexicographic order puts a parent before its children, because a
	// parent path is a prefix of every path below it.
	sort.Strings(dirs)

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, d := range dirs {
		if err := tw.WriteHeader(&tar.Header{
			Name:     strings.TrimPrefix(d, "/") + "/",
			Typeflag: tar.TypeDir,
			Mode:     0o755,
		}); err != nil {
			return nil, nil, fmt.Errorf("docker: build the push archive: %w", err)
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
		}); err != nil {
			return nil, nil, fmt.Errorf("docker: build the push archive: %w", err)
		}
		if _, err := tw.Write(e.content); err != nil {
			return nil, nil, fmt.Errorf("docker: build the push archive: %w", err)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, nil, fmt.Errorf("docker: build the push archive: %w", err)
	}
	return buf.Bytes(), dirs, nil
}

// PushFiles materializes every entry in m inside the container.
//
// It builds one tar in memory and streams it to `docker cp - <name>:/`,
// then applies owner with one follow-up Exec running as root. Nothing is
// staged on the host filesystem, so there is no temp directory to leak on
// an error path and no host ownership to leak into the sandbox. See
// buildPushTar for why the archive is built by hand.
func (s *dockerSession) PushFiles(ctx context.Context, m runtime.FileManifest) error {
	if len(m.Files) == 0 {
		return nil
	}

	entries := make([]pushEntry, 0, len(m.Files))
	for _, f := range m.Files {
		if f.Path == "" {
			return errors.New("docker: PushFiles: a FileEntry has an empty Path")
		}
		if err := validateSandboxPath(f.Path); err != nil {
			return err
		}
		if f.Owner != "" && !ownerPattern.MatchString(f.Owner) {
			return fmt.Errorf("docker: owner %q does not match %s", f.Owner, ownerPattern.String())
		}
		entries = append(entries, pushEntry{
			path:    path.Clean(f.Path),
			content: stripCR(f.Content),
			owner:   f.Owner,
			mode:    f.Mode,
		})
	}

	archive, dirs, err := buildPushTar(entries)
	if err != nil {
		return err
	}

	_, stderr, code, err := s.rt.run.run(ctx, []string{"docker", "cp", "-", s.rt.name + ":/"}, archive)
	if err != nil {
		return fmt.Errorf("docker cp (push): %w", err)
	}
	if code != 0 {
		return fmt.Errorf("docker cp (push) exited %d: %s", code, stderr)
	}

	// The tar already carries each file's mode, so the chmod below is
	// belt and braces rather than the mechanism. The chown is not: the
	// interface hands us an owner by name, and only the sandbox can
	// resolve a name to a uid, so it has to happen here.
	var script strings.Builder
	for _, d := range dirs {
		fmt.Fprintf(&script, "chmod 0755 %s && ", shQuote(d))
	}
	for _, e := range entries {
		fmt.Fprintf(&script, "chmod %o %s && ", e.mode.Perm(), shQuote(e.path))
		if e.owner != "" {
			fmt.Fprintf(&script, "chown %s %s && ", shQuote(e.owner), shQuote(e.path))
		}
	}
	script.WriteString("true")

	argv := []string{"docker", "exec", "-u", "root", "--", s.rt.name, "/bin/bash", "-lc", script.String()}
	_, stderr, code, err = s.rt.run.run(ctx, argv, nil)
	if err != nil {
		return fmt.Errorf("docker exec (apply mode and owner): %w", err)
	}
	if code != 0 {
		return fmt.Errorf("docker exec (apply mode and owner) exited %d: %s", code, stderr)
	}
	return nil
}

// PullFile reads an absolute path inside the container and returns its
// content, via `docker cp <name>:<path> -`, which writes a tar stream to
// stdout. archive/tar reads it; nothing here shells out to tar.
func (s *dockerSession) PullFile(ctx context.Context, p string) ([]byte, error) {
	if p == "" {
		return nil, errors.New("docker: PullFile: empty path")
	}
	if err := validateSandboxPath(p); err != nil {
		return nil, err
	}

	stdout, stderr, code, err := s.rt.run.run(ctx, []string{"docker", "cp", s.rt.name + ":" + p, "-"}, nil)
	if err != nil {
		return nil, fmt.Errorf("docker cp (pull) %s: %w", p, err)
	}
	if code != 0 {
		return nil, fmt.Errorf("docker cp (pull) %s exited %d: %s", p, code, stderr)
	}

	tr := tar.NewReader(bytes.NewReader(stdout))
	hdr, err := tr.Next()
	if err != nil {
		return nil, fmt.Errorf("docker cp (pull) %s: reading the tar stream: %w", p, err)
	}
	if hdr.Typeflag != tar.TypeReg {
		return nil, fmt.Errorf("docker cp (pull) %s: tar entry %q is not a regular file", p, hdr.Name)
	}
	content, err := io.ReadAll(tr)
	if err != nil {
		return nil, fmt.Errorf("docker cp (pull) %s: reading the tar entry: %w", p, err)
	}
	return content, nil
}

// Close releases the session. A dockerSession holds no host-side resource
// beyond one-shot docker invocations that have already completed by the
// time any method returns, so there is nothing to release, and calling
// this more than once is trivially safe.
func (s *dockerSession) Close() error { return nil }

// dockerPTY wraps the master side of the pseudo terminal creack/pty
// allocated around a `docker exec -it` child.
type dockerPTY struct {
	file *os.File
	cmd  *exec.Cmd
}

var _ runtime.PTY = (*dockerPTY)(nil)

func (p *dockerPTY) Read(b []byte) (int, error)  { return p.file.Read(b) }
func (p *dockerPTY) Write(b []byte) (int, error) { return p.file.Write(b) }
func (p *dockerPTY) Close() error                { return p.file.Close() }

func (p *dockerPTY) Resize(rows, cols uint16) error {
	return pty.Setsize(p.file, &pty.Winsize{Rows: rows, Cols: cols})
}

func (p *dockerPTY) Wait() error { return p.cmd.Wait() }
