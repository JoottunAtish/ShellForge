// Package docker implements internal/runtime.Runtime by shelling out to the
// docker CLI. It is the reference backend: the one Linux and macOS use, and
// the one the WSL implementation is checked against.
//
// Every docker invocation is exec.CommandContext with an argv vector. There
// is no sh -c and no fmt.Sprintf building a command line. See the security
// skill for why that distinction matters.
package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/JoottunAtish/ShellForge/internal/platform"
	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

// imagePattern is the allowlist for a Docker image reference. A container
// name or a user name is validated with platform.ValidIdentifier instead,
// the strict allowlist exactly what the security skill quotes, but a real
// image reference needs '.', ':', '/', and '@' for a registry host, a tag,
// and a digest (name@sha256:hex), which platform.ValidIdentifier does not
// admit. The first character stays restricted to alphanumeric: that is the
// property that actually defeats argument injection, and it is preserved
// here even though the character class is wider than
// platform.IdentifierPattern's.
var imagePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9/_.:@-]*$`)

// sandboxLabel marks a container as ours. Destroy refuses to remove a
// container that does not carry it, because that is the only way to tell
// "ours" apart from "something else happens to have this name" once the
// name alone is not proof.
const sandboxLabel = "shellforge.sandbox"

// containerfilePath and containerfileContext are where Provision builds the
// image from, relative to the repository root. This matches `make image`.
const (
	containerfilePath    = "images/Containerfile"
	containerfileContext = "images/"
)

// repoRootRelative resolves rel against the repository root rather than the
// process's current working directory. `go test` runs a package's tests
// with the working directory set to that package's own directory, not the
// repository root, so a plain relative "images/Containerfile" resolves to
// nothing when this runs under `go test ./internal/runtime/docker/...`. The
// repository root is found by walking up from the working directory to the
// nearest go.mod, the same landmark `go build` and every Makefile target
// already treat as the root.
func repoRootRelative(rel string) (string, error) {
	if _, err := os.Stat(rel); err == nil {
		abs, err := filepath.Abs(rel)
		return abs, err
	}

	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("docker: find the repository root: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, rel), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("docker: could not find %s: no go.mod between %s and the filesystem root", rel, dir)
		}
		dir = parent
	}
}

// dockerRuntime implements runtime.Runtime over a single named container.
//
// name is the container's identity: fixed at construction, never a
// parameter to any method, and never read back from an ImageSpec. That is
// what destructive-safety requires of a destroy target.
type dockerRuntime struct {
	name  string
	image string
	run   runner
}

var _ runtime.Runtime = (*dockerRuntime)(nil)

// New returns a DockerRuntime for the named container and image.
//
// name and image are validated against a strict allowlist before they can
// reach an argv, and New returns an error rather than sanitizing a bad
// value.
func New(name, image string) (runtime.Runtime, error) {
	if !platform.ValidIdentifier(name) {
		return nil, fmt.Errorf("docker.New: container name %q does not match %s", name, platform.IdentifierPattern)
	}
	if !imagePattern.MatchString(image) {
		return nil, fmt.Errorf("docker.New: image %q does not match %s", image, imagePattern.String())
	}

	bin, err := exec.LookPath("docker")
	if err != nil {
		return nil, ux.Fail(
			"find the docker command",
			err,
			"install Docker, then run `shellforge doctor` again; see docs/02-install-linux.md",
			"docker-not-found",
		)
	}

	return &dockerRuntime{name: name, image: image, run: execRunner{bin: bin}}, nil
}

// resolveImage picks the image Provision acts on: spec.Name when set,
// otherwise the image New was constructed with.
//
// See the PR that introduced this package for why ImageSpec.Name, not the
// image New received, is the authority here: the interface's Name field
// exists so a caller can provision a different image than the one it will
// eventually run against (the contract suite does exactly this, naming
// SandboxName rather than a production image), and Provision has no other
// way to learn that choice.
func (rt *dockerRuntime) resolveImage(spec runtime.ImageSpec) (string, error) {
	image := spec.Name
	if image == "" {
		image = rt.image
	}
	if !imagePattern.MatchString(image) {
		return "", fmt.Errorf("docker: image %q does not match %s", image, imagePattern.String())
	}
	return image, nil
}

// Provision ensures the image exists, building it if not, then ensures the
// container exists and is running. Both halves are idempotent.
//
// Provision creates and starts the container, not merely the image. See
// runtime.Runtime.Provision: a successful Provision must leave Status
// reporting Running true with no separate start step. The ticket's mapping
// table shows `docker run` under StartSession, but that contradicts the
// interface doc comment and the contract test (TestStatusReportsProvisioned
// asserts Running true immediately after Provision, with no StartSession
// call in between). This implementation follows the interface and the
// contract, which CLAUDE.md names as authoritative over ticket prose when
// the two disagree.
func (rt *dockerRuntime) Provision(ctx context.Context, spec runtime.ImageSpec) error {
	image, err := rt.resolveImage(spec)
	if err != nil {
		return err
	}
	if spec.Reference != "" {
		return fmt.Errorf("docker: Provision with a non-empty ImageSpec.Reference is not supported; this backend only builds %s locally", containerfilePath)
	}

	if err := rt.ensureImage(ctx, image); err != nil {
		return err
	}
	return rt.ensureContainerRunning(ctx, image)
}

func (rt *dockerRuntime) ensureImage(ctx context.Context, image string) error {
	_, _, code, err := rt.run.run(ctx, []string{"docker", "image", "inspect", "--", image}, nil)
	if err == nil && code == 0 {
		return nil
	}

	containerfile, err := repoRootRelative(containerfilePath)
	if err != nil {
		return err
	}
	buildContext, err := repoRootRelative(containerfileContext)
	if err != nil {
		return err
	}

	stdout, stderr, code, runErr := rt.run.run(ctx, []string{"docker", "build", "-f", containerfile, "-t", image, "--", buildContext}, nil)
	if runErr != nil {
		return rt.classifyFailure(ctx, "build the sandbox image", runErr, combinedOutput(stdout, stderr))
	}
	if code != 0 {
		return rt.classifyFailure(ctx, "build the sandbox image", fmt.Errorf("docker build exited %d: %s", code, summarizeFailure(stdout, stderr)), combinedOutput(stdout, stderr))
	}
	return nil
}

func (rt *dockerRuntime) ensureContainerRunning(ctx context.Context, image string) error {
	exists, running, hasLabel, err := rt.inspectContainer(ctx)
	if err != nil {
		return err
	}

	switch {
	case !exists:
		stdout, stderr, code, err := rt.run.run(ctx, []string{
			"docker", "run", "-d",
			"--name", rt.name,
			"--label", sandboxLabel + "=1",
			"--network", "none",
			"--cap-drop", "ALL",
			// PushFiles hands a file to "learner" after writing it as
			// root, and chown needs CAP_CHOWN once every capability is
			// dropped. FOWNER covers the chmod of a file a previous push
			// already handed away. This is the security skill's "drop all
			// capabilities, add back only what a level provably needs",
			// applied to a need every level has rather than a specific
			// one, which is as provable as this gets.
			//
			// DAC_OVERRIDE is deliberately NOT here. An earlier revision
			// needed it because the staged-tree push left the copied
			// files owned by an arbitrary host uid that root did not own.
			// buildPushTar writes every entry as uid 0 instead, so root
			// owns what it is about to chmod and chown, and the
			// capability that would let it override permissions
			// altogether is not required.
			"--cap-add", "CHOWN",
			"--cap-add", "FOWNER",
			"--security-opt", "no-new-privileges",
			"--", image, "sleep", "infinity",
		}, nil)
		if err != nil {
			return rt.classifyFailure(ctx, "start the sandbox container", err, combinedOutput(stdout, stderr))
		}
		if code != 0 {
			return rt.classifyFailure(ctx, "start the sandbox container", fmt.Errorf("docker run exited %d: %s", code, stderr), combinedOutput(stdout, stderr))
		}
		return nil
	case !hasLabel:
		return fmt.Errorf("docker: refusing to reuse container %q: it does not carry the %s label, so it was not created by Shellforge", rt.name, sandboxLabel)
	case running:
		return nil
	default:
		stdout, stderr, code, err := rt.run.run(ctx, []string{"docker", "start", "--", rt.name}, nil)
		if err != nil {
			return rt.classifyFailure(ctx, "start the sandbox container", err, combinedOutput(stdout, stderr))
		}
		if code != 0 {
			return rt.classifyFailure(ctx, "start the sandbox container", fmt.Errorf("docker start exited %d: %s", code, stderr), combinedOutput(stdout, stderr))
		}
		return nil
	}
}

// inspectContainer reports whether rt.name exists, whether it is running,
// and whether it carries the Shellforge marker label. A container that does
// not exist is reported as exists=false with a nil error: that is not a
// failure, it is the common case Provision and Status both need to handle
// without treating "not found" as an environment error.
func (rt *dockerRuntime) inspectContainer(ctx context.Context) (exists, running, hasLabel bool, err error) {
	stdout, stderr, code, runErr := rt.run.run(ctx, []string{
		"docker", "inspect",
		"--format", `{{.State.Running}}|{{index .Config.Labels "` + sandboxLabel + `"}}`,
		"--", rt.name,
	}, nil)
	if runErr != nil {
		return false, false, false, rt.classifyFailure(ctx, "inspect the sandbox container", runErr, combinedOutput(stdout, stderr))
	}
	if code != 0 {
		// docker inspect exits 1 with "No such object" when the target does
		// not exist. That is the expected shape of "not provisioned", not
		// an environment failure, so it is not run through classifyFailure.
		return false, false, false, nil
	}

	out := bytes.TrimSpace(stdout)
	parts := bytes.SplitN(out, []byte("|"), 2)
	if len(parts) != 2 {
		return false, false, false, fmt.Errorf("docker: unexpected `docker inspect` output for %q: %q", rt.name, out)
	}
	return true, string(parts[0]) == "true", string(parts[1]) == "1", nil
}

// Destroy removes the container this Runtime owns. It refuses when the name
// is empty rather than letting an empty argument turn `docker rm -f` into a
// broader command than intended, and it refuses to remove a container that
// does not carry the Shellforge marker label, because a name collision with
// something we did not create is not ours to delete.
func (rt *dockerRuntime) Destroy(ctx context.Context) error {
	if rt.name == "" {
		return errors.New("docker: refusing to destroy: container name is empty")
	}

	exists, _, hasLabel, err := rt.inspectContainer(ctx)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if !hasLabel {
		return fmt.Errorf("docker: refusing to remove container %q: it does not carry the %s label, so it was not created by Shellforge", rt.name, sandboxLabel)
	}

	stdout, stderr, code, err := rt.run.run(ctx, []string{"docker", "rm", "-f", "--", rt.name}, nil)
	if err != nil {
		return rt.classifyFailure(ctx, "remove the sandbox container", err, combinedOutput(stdout, stderr))
	}
	if code != 0 {
		return rt.classifyFailure(ctx, "remove the sandbox container", fmt.Errorf("docker rm exited %d: %s", code, stderr), combinedOutput(stdout, stderr))
	}
	return nil
}

// Status reports whether the container exists and is running. Provisioned
// mirrors "exists", per the interface's Status doc.
func (rt *dockerRuntime) Status(ctx context.Context) (runtime.Status, error) {
	exists, running, _, err := rt.inspectContainer(ctx)
	if err != nil {
		return runtime.Status{}, err
	}
	if !exists {
		return runtime.Status{Backend: "docker"}, nil
	}
	return runtime.Status{
		Provisioned: true,
		Running:     running,
		Backend:     "docker",
		Detail:      rt.name,
	}, nil
}

// StartSession opens a session on the already-provisioned container. It
// does not create or start anything: Provision is documented to leave the
// sandbox running, so StartSession only has to check that promise held.
func (rt *dockerRuntime) StartSession(ctx context.Context, spec runtime.SessionSpec) (runtime.Session, error) {
	status, err := rt.Status(ctx)
	if err != nil {
		return nil, err
	}
	if !status.Provisioned || !status.Running {
		return nil, fmt.Errorf("start a docker session: %w", runtime.ErrSandboxMissing)
	}
	return &dockerSession{rt: rt, spec: spec}, nil
}

// Capabilities reports what this backend supports. Docker on Linux and
// macOS supports networking (a level can opt in) and multiple users (the
// image ships a learner and a root account), but not systemd, snapshotting,
// or privileged mode.
func (rt *dockerRuntime) Capabilities() runtime.Caps {
	return runtime.Caps{
		Networking: true,
		MultiUser:  true,
	}
}

// combinedOutput concatenates stdout and stderr for classifyFailure, which
// must not assume which stream carries docker's diagnostic message.
// summarizeFailure documents the same split for BuildKit, which writes to
// stdout, against the classic builder, which writes to stderr; the WSL
// stub message this function keys on has no established stream of its own
// either, only a reproduction that did not distinguish the two, so
// classification reads both rather than guess.
func combinedOutput(stdout, stderr []byte) []byte {
	return append(append([]byte{}, stdout...), stderr...)
}

// classifyFailure turns a raw docker failure into a ux.Fail carrying a
// remediation and a doc anchor, distinguishing "docker missing" (caught
// earlier, in New), "permission denied", and "daemon not running". The
// permission-denied case is detected from docker's own stable message
// rather than a generic daemon probe, because docker's client reports that
// case distinctly and unambiguously. Anything else falls back to an
// independent `docker version` probe of the daemon's own health, rather
// than string-matching the original command's message, per the ticket's
// approach.
//
// output is the failed command's stdout and stderr concatenated by the
// caller via combinedOutput, not stderr alone: a stub docker binary inside
// a WSL distribution without Docker Desktop integration writes its "could
// not be found" message with no daemon involved to pick a stream by
// convention, so the branch below cannot assume it landed on stderr.
func (rt *dockerRuntime) classifyFailure(ctx context.Context, op string, err error, output []byte) error {
	if isPermissionDenied(output) {
		return ux.Fail(op, err, `sudo usermod -aG docker "$USER", then log out and back in`, "docker-permission-denied")
	}

	// This branch MUST come before the `docker version` probe below. The
	// message is written by Docker Desktop's own shim inside the WSL
	// distribution, not by the daemon, so the probe fails exactly the way a
	// dead daemon does and cannot tell the two apart. Probing first would
	// send someone whose Docker is running and healthy to "start Docker".
	// Keyed on Docker's own stable wording rather than on the URL that
	// follows it, matching the permission-denied branch above.
	if bytes.Contains(output, []byte("could not be found in this WSL 2 distro")) {
		return ux.Fail(op, err,
			"Open Docker Desktop, go to Settings, Resources, WSL integration, and turn the integration on for this distribution. Then run `docker version` inside WSL to confirm it is reachable.",
			"docker-wsl-integration-off")
	}

	_, versionStderr, versionCode, versionErr := rt.run.run(ctx, []string{"docker", "version"}, nil)
	if versionErr != nil || versionCode != 0 {
		if isPermissionDenied(versionStderr) {
			return ux.Fail(op, err, `sudo usermod -aG docker "$USER", then log out and back in`, "docker-permission-denied")
		}
		return ux.Fail(op, err, "start Docker: `sudo systemctl start docker` on Linux, or start Docker Desktop", "docker-daemon-down")
	}

	return fmt.Errorf("%s: %w", op, err)
}

// lastLine returns the last non-empty line of b, trimmed.
func lastLine(b []byte) string {
	lines := bytes.Split(bytes.TrimSpace(b), []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		if line := bytes.TrimSpace(lines[i]); len(line) > 0 {
			return string(line)
		}
	}
	return ""
}

// isPermissionDenied reports whether b is docker's own "you are not in the
// docker group" message.
//
// The match is case-insensitive because docker capitalises the product name:
// what it actually prints is "Got permission denied while trying to connect
// to the Docker daemon socket at unix:///var/run/docker.sock", and
// docs/05-troubleshooting.md quotes it that way. This was a case-sensitive
// match against a lowercase "docker daemon socket" until issue #74, so the
// branch never fired: a user in the wrong group fell through to the `docker
// version` probe, which fails on the same message, and was told to start a
// daemon that was already running.
func isPermissionDenied(b []byte) bool {
	lower := bytes.ToLower(b)
	return bytes.Contains(lower, []byte("permission denied")) &&
		bytes.Contains(lower, []byte("docker daemon socket"))
}

// firstLine returns the first non-empty line of b, trimmed. It is the right
// summary when the output is a human-written wrapper message whose diagnosis
// comes first and whose last line is a bare URL.
func firstLine(b []byte) string {
	for _, line := range bytes.Split(bytes.TrimSpace(b), []byte("\n")) {
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			return string(trimmed)
		}
	}
	return ""
}

// isBareURL reports whether line is nothing but a URL, and so says nothing
// about what went wrong.
//
// Deliberately a prefix test against two schemes and nothing cleverer. A line
// that merely contains a URL alongside text is a perfectly good summary and
// must be left alone, and anything that scores lines by "informativeness"
// would misfire on the build failure this file's default gets right.
func isBareURL(line string) bool {
	return strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://")
}

// summarize picks the summary line for one stream: the last non-empty line,
// unless that line is only a URL, in which case the first non-empty line.
func summarize(b []byte) string {
	if line := lastLine(b); !isBareURL(line) {
		return line
	}
	return firstLine(b)
}

// summarizeFailure reports the summary line of both stdout and stderr,
// labelled, rather than guessing which stream `docker build` chose for its
// fatal error: BuildKit writes it to stdout, the classic builder to
// stderr, and either way it is the last line, not the first. An earlier
// version of this took only the first non-empty line across the two
// streams, which reliably surfaced the "Sending build context..." progress
// banner instead of the actual failure.
//
// The last line is wrong for one narrow case, which is why summarize exists:
// when docker is not really present, the output is a human-written wrapper
// message whose diagnosis is the FIRST line and whose last line is a bare
// URL. Reporting the URL threw away the only sentence that said what to do.
// The fallback is limited to a summary line that is nothing but a URL, so the
// BuildKit reasoning above still holds everywhere else.
func summarizeFailure(stdout, stderr []byte) string {
	out, errLine := summarize(stdout), summarize(stderr)
	switch {
	case out != "" && errLine != "":
		return fmt.Sprintf("stdout: %s | stderr: %s", out, errLine)
	case errLine != "":
		return errLine
	default:
		return out
	}
}
