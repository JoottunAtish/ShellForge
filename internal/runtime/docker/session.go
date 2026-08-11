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
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/creack/pty"

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
	if user != "" && !identifierPattern.MatchString(user) {
		return "", fmt.Errorf("docker: user %q does not match %s", user, identifierPattern.String())
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
func (s *dockerSession) execArgv(user, workdir string, env map[string]string, command []string) []string {
	argv := []string{"docker", "exec"}
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

	full := s.execArgv(user, workdir, opts.Env, argv)

	execCtx := ctx
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	start := time.Now()
	stdout, stderr, code, runErr := s.rt.run.run(execCtx, full, opts.Stdin)
	duration := time.Since(start)

	if runErr != nil {
		if ctx.Err() != nil {
			return runtime.ExecResult{}, ctx.Err()
		}
		if opts.Timeout > 0 && execCtx.Err() != nil {
			return runtime.ExecResult{ExitCode: -1, TimedOut: true, Duration: duration}, nil
		}
		return runtime.ExecResult{}, fmt.Errorf("docker exec: %w", runErr)
	}

	return runtime.ExecResult{Stdout: stdout, Stderr: stderr, ExitCode: code, Duration: duration}, nil
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

// PushFiles materializes every entry in m inside the container.
//
// It stages every file into one host temp directory, mirroring each
// entry's absolute sandbox path so a single `docker cp` of the staging
// root onto the container's root places every file correctly, then applies
// mode and owner with one follow-up Exec running as root: `docker cp` does
// not preserve them usefully across platforms.
func (s *dockerSession) PushFiles(ctx context.Context, m runtime.FileManifest) error {
	if len(m.Files) == 0 {
		return nil
	}

	stage, err := os.MkdirTemp("", "shellforge-docker-push-*")
	if err != nil {
		return fmt.Errorf("docker: create staging directory: %w", err)
	}
	defer os.RemoveAll(stage)

	type pending struct {
		path  string
		owner string
		mode  fs.FileMode
	}
	toApply := make([]pending, 0, len(m.Files))

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
		clean := path.Clean(f.Path)

		dest := filepath.Join(stage, filepath.FromSlash(clean))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fmt.Errorf("docker: stage %s: %w", f.Path, err)
		}
		if err := os.WriteFile(dest, stripCR(f.Content), 0o644); err != nil {
			return fmt.Errorf("docker: stage %s: %w", f.Path, err)
		}
		toApply = append(toApply, pending{path: clean, owner: f.Owner, mode: f.Mode})
	}

	src := stage + string(filepath.Separator) + "."
	_, stderr, code, err := s.rt.run.run(ctx, []string{"docker", "cp", src, s.rt.name + ":/"}, nil)
	if err != nil {
		return fmt.Errorf("docker cp (push): %w", err)
	}
	if code != 0 {
		return fmt.Errorf("docker cp (push) exited %d: %s", code, stderr)
	}

	var script strings.Builder
	for _, p := range toApply {
		fmt.Fprintf(&script, "chmod %o %s && ", p.mode.Perm(), shQuote(p.path))
		if p.owner != "" {
			fmt.Fprintf(&script, "chown %s %s && ", shQuote(p.owner), shQuote(p.path))
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
