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

	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

// identifierPattern is the strict allowlist for a container name or a user
// name: exactly what the security skill quotes. The first character is
// restricted to alphanumeric so a validated value cannot be flag-shaped.
var identifierPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

// imagePattern is the allowlist for a Docker image reference. A real image
// reference needs '.', ':', '/', and '@' for a registry host, a tag, and a
// digest (name@sha256:hex), which identifierPattern does not admit. The
// first character stays restricted to alphanumeric: that is the property
// that actually defeats argument injection, and it is preserved here even
// though the character class is wider than identifierPattern's.
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
	if !identifierPattern.MatchString(name) {
		return nil, fmt.Errorf("docker.New: container name %q does not match %s", name, identifierPattern.String())
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
		return rt.classifyFailure(ctx, "build the sandbox image", runErr, stderr)
	}
	if code != 0 {
		return rt.classifyFailure(ctx, "build the sandbox image", fmt.Errorf("docker build exited %d: %s", code, firstLine(stdout, stderr)), stderr)
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
		_, stderr, code, err := rt.run.run(ctx, []string{
			"docker", "run", "-d",
			"--name", rt.name,
			"--label", sandboxLabel + "=1",
			"--network", "none",
			"--cap-drop", "ALL",
			// PushFiles must be able to hand a file to "learner" after
			// staging it as root, and chown needs CAP_CHOWN even for root
			// once every capability is dropped. This is exactly the
			// security skill's "drop all capabilities, add back only what
			// a level provably needs": PushFiles needs these two for
			// every level, not a specific one, which is as provable as
			// this gets.
			"--cap-add", "CHOWN",
			"--cap-add", "FOWNER",
			"--security-opt", "no-new-privileges",
			"--", image, "sleep", "infinity",
		}, nil)
		if err != nil {
			return rt.classifyFailure(ctx, "start the sandbox container", err, stderr)
		}
		if code != 0 {
			return rt.classifyFailure(ctx, "start the sandbox container", fmt.Errorf("docker run exited %d: %s", code, stderr), stderr)
		}
		return nil
	case !hasLabel:
		return fmt.Errorf("docker: refusing to reuse container %q: it does not carry the %s label, so it was not created by Shellforge", rt.name, sandboxLabel)
	case running:
		return nil
	default:
		_, stderr, code, err := rt.run.run(ctx, []string{"docker", "start", "--", rt.name}, nil)
		if err != nil {
			return rt.classifyFailure(ctx, "start the sandbox container", err, stderr)
		}
		if code != 0 {
			return rt.classifyFailure(ctx, "start the sandbox container", fmt.Errorf("docker start exited %d: %s", code, stderr), stderr)
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
		return false, false, false, rt.classifyFailure(ctx, "inspect the sandbox container", runErr, stderr)
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

	_, stderr, code, err := rt.run.run(ctx, []string{"docker", "rm", "-f", "--", rt.name}, nil)
	if err != nil {
		return rt.classifyFailure(ctx, "remove the sandbox container", err, stderr)
	}
	if code != 0 {
		return rt.classifyFailure(ctx, "remove the sandbox container", fmt.Errorf("docker rm exited %d: %s", code, stderr), stderr)
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

// classifyFailure turns a raw docker failure into a ux.Fail carrying a
// remediation and a doc anchor, distinguishing "docker missing" (caught
// earlier, in New), "permission denied", and "daemon not running". The
// permission-denied case is detected from docker's own stable message
// rather than a generic daemon probe, because docker's client reports that
// case distinctly and unambiguously. Anything else falls back to an
// independent `docker version` probe of the daemon's own health, rather
// than string-matching the original command's message, per the ticket's
// approach.
func (rt *dockerRuntime) classifyFailure(ctx context.Context, op string, err error, stderr []byte) error {
	if bytes.Contains(stderr, []byte("permission denied")) && bytes.Contains(stderr, []byte("docker daemon socket")) {
		return ux.Fail(op, err, `sudo usermod -aG docker "$USER", then log out and back in`, "docker-permission-denied")
	}

	_, versionStderr, versionCode, versionErr := rt.run.run(ctx, []string{"docker", "version"}, nil)
	if versionErr != nil || versionCode != 0 {
		if bytes.Contains(versionStderr, []byte("permission denied")) && bytes.Contains(versionStderr, []byte("docker daemon socket")) {
			return ux.Fail(op, err, `sudo usermod -aG docker "$USER", then log out and back in`, "docker-permission-denied")
		}
		return ux.Fail(op, err, "start Docker: `sudo systemctl start docker` on Linux, or start Docker Desktop", "docker-daemon-down")
	}

	return fmt.Errorf("%s: %w", op, err)
}

func firstLine(streams ...[]byte) string {
	for _, s := range streams {
		if line := bytes.SplitN(bytes.TrimSpace(s), []byte("\n"), 2)[0]; len(line) > 0 {
			return string(line)
		}
	}
	return ""
}
