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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"

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

// sandboxInit is PID 1 inside the sandbox. It sleeps, it reaps, and it exits
// cleanly when asked.
//
// This used to be `sleep infinity`, which is the wrong PID 1 for a machine
// where a learner backgrounds a process and then kills it. An orphaned
// process is re-parented to PID 1, and a PID 1 that never calls wait leaves
// it as a zombie for the life of the container. `sleep` never calls wait.
// Level proc-01 is what made that visible, because the golden contract
// asserts that no process outlives a level's teardown and a zombie is still
// a row in `ps`, but it was never a proc-01 problem: any level where a
// learner runs `sleep 100 &` and then kills it has the same hole, and Act V
// is the act that teaches them to.
//
// bash reaps. Its SIGCHLD handling calls waitpid(-1) in a loop and discards
// the pids it does not recognise, which is exactly the job of an init. The
// obvious alternative, `docker run --init`, is refused: Docker implements it
// by bind mounting docker-init in from the host, and this sandbox promises
// exactly one host mount. TestSandboxHasNoHostMounts would be right to fail
// it.
//
// The trap is so `docker stop` gets a clean exit instead of having to
// escalate to SIGKILL after ten seconds.
const sandboxInit = "trap 'exit 0' TERM INT; while :; do sleep infinity & wait $!; done"

// containerInspectFormat asks one `docker inspect` for everything
// ensureContainerRunning needs to choose between reusing a container and
// replacing it. The JSON argv is last on purpose: a separator byte inside it
// then cannot split a field, because SplitN stops counting.
const containerInspectFormat = `{{.State.Running}}|{{index .Config.Labels "` + sandboxLabel + `"}}|{{.Image}}|{{json .Config.Cmd}}`

// containerInspectFields is how many fields containerInspectFormat produces.
const containerInspectFields = 4

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
		return rt.classifyFailure(ctx, "build the sandbox image", runErr, stderr)
	}
	if code != 0 {
		return rt.classifyFailure(ctx, "build the sandbox image", fmt.Errorf("docker build exited %d: %s", code, summarizeFailure(stdout, stderr)), stderr)
	}
	return nil
}

func (rt *dockerRuntime) ensureContainerRunning(ctx context.Context, image string) error {
	st, err := rt.inspectContainer(ctx)
	if err != nil {
		return err
	}

	if !st.exists {
		return rt.createContainer(ctx, image)
	}
	if !st.hasLabel {
		return fmt.Errorf("docker: refusing to reuse container %q: it does not carry the %s label, so it was not created by Shellforge", rt.name, sandboxLabel)
	}

	// The container is ours, which is the only reason the next step is
	// allowed to remove it. Reuse is correct only when it was built from the
	// image we would build from today and started with the argv we would
	// start with today. Neither is implied by the name: `make image`
	// rebuilds a tag in place, so a container created weeks ago keeps the
	// old layers under the same name, and a change to sandboxInit leaves an
	// old PID 1 running under the new binary. Both were real: the Day 5
	// content run reported three phantom failures against a stale container,
	// because the group membership the image had just gained and the PID 1
	// that had just learned to reap were both absent from the one being
	// reused.
	stale, err := rt.containerIsStale(ctx, image, st)
	if err != nil {
		return err
	}
	if stale {
		if err := rt.removeContainer(ctx); err != nil {
			return err
		}
		return rt.createContainer(ctx, image)
	}

	if st.running {
		return nil
	}
	_, stderr, code, err := rt.run.run(ctx, []string{"docker", "start", "--", rt.name}, nil)
	if err != nil {
		return rt.classifyFailure(ctx, "start the sandbox container", err, stderr)
	}
	if code != 0 {
		return rt.classifyFailure(ctx, "start the sandbox container", fmt.Errorf("docker start exited %d: %s", code, stderr), stderr)
	}
	return nil
}

// containerIsStale reports whether an existing container of ours differs
// from the one createContainer would produce now.
//
// It fails closed. A container whose image id or argv cannot be read back is
// not provably current, so it is reported stale and recreated rather than
// reused. That costs a rebuild in the worst case; reusing a container that
// does not match the code running against it costs a debugging session
// chasing a bug that was fixed weeks ago.
func (rt *dockerRuntime) containerIsStale(ctx context.Context, image string, st containerState) (bool, error) {
	if !equalArgv(st.cmd, sandboxCommand()) {
		return true, nil
	}

	stdout, stderr, code, err := rt.run.run(ctx, []string{
		"docker", "image", "inspect", "--format", "{{.Id}}", "--", image,
	}, nil)
	if err != nil {
		return false, rt.classifyFailure(ctx, "inspect the sandbox image", err, stderr)
	}
	if code != 0 {
		// The tag has gone missing between ensureImage and here. Treat the
		// container as stale rather than erroring: the next createContainer
		// reports the real problem, with docker's own message.
		return true, nil
	}

	want := string(bytes.TrimSpace(stdout))
	return want == "" || want != st.imageID, nil
}

// removeContainer deletes the container this Runtime owns. Every caller must
// have established that it carries sandboxLabel first: the label is the
// marker proving Shellforge created it, and nothing else licenses a `docker
// rm -f` against a name that could collide with something a user built.
func (rt *dockerRuntime) removeContainer(ctx context.Context) error {
	if rt.name == "" {
		return errors.New("docker: refusing to remove the sandbox container: container name is empty")
	}
	_, stderr, code, err := rt.run.run(ctx, []string{"docker", "rm", "-f", "--", rt.name}, nil)
	if err != nil {
		return rt.classifyFailure(ctx, "replace the stale sandbox container", err, stderr)
	}
	if code != 0 {
		return rt.classifyFailure(ctx, "replace the stale sandbox container", fmt.Errorf("docker rm exited %d: %s", code, stderr), stderr)
	}
	return nil
}

func (rt *dockerRuntime) createContainer(ctx context.Context, image string) error {
	argv := append([]string{
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
		"--", image,
	}, sandboxCommand()...)

	_, stderr, code, err := rt.run.run(ctx, argv, nil)
	if err != nil {
		return rt.classifyFailure(ctx, "start the sandbox container", err, stderr)
	}
	if code != 0 {
		return rt.classifyFailure(ctx, "start the sandbox container", fmt.Errorf("docker run exited %d: %s", code, stderr), stderr)
	}
	return nil
}

// sandboxCommand is the argv PID 1 runs. It is a function rather than a
// package variable so that no caller can mutate the slice the staleness
// comparison depends on.
func sandboxCommand() []string { return []string{"bash", "-c", sandboxInit} }

// containerState is what one `docker inspect` of rt.name reports.
type containerState struct {
	// exists is false when docker has no container by that name. That is
	// not a failure: it is the common case Provision and Status both need
	// to handle without treating "not found" as an environment error.
	exists bool
	// running mirrors .State.Running.
	running bool
	// hasLabel reports the Shellforge marker label. It is the only thing
	// that licenses removing the container, so nothing may act on the two
	// fields below before this one has been checked.
	hasLabel bool
	// imageID is the id of the image the container was created from, not
	// the tag it was named with. A tag is rebuilt in place; an id is not,
	// which is what makes this the honest question to ask.
	imageID string
	// cmd is the container's argv. It is nil when docker reports it as
	// null, or as anything that is not a JSON array of strings, which
	// containerIsStale reads as "not provably current".
	cmd []string
}

// inspectContainer reports the state of rt.name in a single docker call.
func (rt *dockerRuntime) inspectContainer(ctx context.Context) (containerState, error) {
	stdout, stderr, code, runErr := rt.run.run(ctx, []string{
		"docker", "inspect",
		"--format", containerInspectFormat,
		"--", rt.name,
	}, nil)
	if runErr != nil {
		return containerState{}, rt.classifyFailure(ctx, "inspect the sandbox container", runErr, stderr)
	}
	if code != 0 {
		// docker inspect exits 1 with "No such object" when the target does
		// not exist. That is the expected shape of "not provisioned", not
		// an environment failure, so it is not run through classifyFailure.
		return containerState{}, nil
	}

	out := bytes.TrimSpace(stdout)
	parts := bytes.SplitN(out, []byte("|"), containerInspectFields)
	if len(parts) != containerInspectFields {
		return containerState{}, fmt.Errorf("docker: unexpected `docker inspect` output for %q: %q", rt.name, out)
	}

	st := containerState{
		exists:   true,
		running:  string(parts[0]) == "true",
		hasLabel: string(parts[1]) == "1",
		imageID:  string(bytes.TrimSpace(parts[2])),
	}
	// An argv that will not unmarshal leaves cmd nil, which reads as stale.
	// Refusing to load the runtime over it would be worse: the recovery is
	// a container rebuild either way, and only one of those two options can
	// be taken without the learner doing anything.
	if err := json.Unmarshal(bytes.TrimSpace(parts[3]), &st.cmd); err != nil {
		st.cmd = nil
	}
	return st, nil
}

// equalArgv reports whether two argv slices are element-wise identical.
func equalArgv(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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

	st, err := rt.inspectContainer(ctx)
	if err != nil {
		return err
	}
	if !st.exists {
		return nil
	}
	if !st.hasLabel {
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
	st, err := rt.inspectContainer(ctx)
	if err != nil {
		return runtime.Status{}, err
	}
	if !st.exists {
		return runtime.Status{Backend: "docker"}, nil
	}
	return runtime.Status{
		Provisioned: true,
		Running:     st.running,
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

// summarizeFailure reports the last line of both stdout and stderr,
// labelled, rather than guessing which stream `docker build` chose for its
// fatal error: BuildKit writes it to stdout, the classic builder to
// stderr, and either way it is the last line, not the first. An earlier
// version of this took only the first non-empty line across the two
// streams, which reliably surfaced the "Sending build context..." progress
// banner instead of the actual failure.
func summarizeFailure(stdout, stderr []byte) string {
	out, errLine := lastLine(stdout), lastLine(stderr)
	switch {
	case out != "" && errLine != "":
		return fmt.Sprintf("stdout: %s | stderr: %s", out, errLine)
	case errLine != "":
		return errLine
	default:
		return out
	}
}
