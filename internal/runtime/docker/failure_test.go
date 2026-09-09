package docker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

// wslWrapperMessage is what Docker Desktop's shim prints inside a WSL
// distribution that the integration is switched off for, verbatim from the
// reproduction in issue #74. Its diagnosis is the FIRST line and its last
// line is a bare URL, which is the whole reason summarizeFailure needs a
// fallback.
const wslWrapperMessage = `The command 'docker' could not be found in this WSL 2 distro.
We recommend to activate the WSL integration in Docker Desktop settings.

For details about using Docker Desktop with WSL 2, visit:

https://docs.docker.com/go/wsl2/
`

// TestSummarizeFailurePicksTheInformativeLine pins BOTH directions of the
// heuristic in one table on purpose. They pull against each other: a build
// failure's fatal error is the last line, and the WSL wrapper's diagnosis is
// the first. Split across two tests, the next person fixing one would break
// the other and see nothing go red.
func TestSummarizeFailurePicksTheInformativeLine(t *testing.T) {
	buildFailure := `#5 [2/4] RUN apt-get install -y nonexistent-package
#5 0.412 E: Unable to locate package nonexistent-package
#5 ERROR: process "/bin/sh -c apt-get install -y nonexistent-package" did not complete successfully: exit code: 100
`

	cases := []struct {
		name   string
		stdout string
		stderr string
		want   string
	}{
		{
			name:   "a last line that is only a URL falls back to the first line",
			stderr: wslWrapperMessage,
			want:   "The command 'docker' could not be found in this WSL 2 distro.",
		},
		{
			name:   "an ordinary build failure still reports its LAST line",
			stdout: buildFailure,
			want:   `#5 ERROR: process "/bin/sh -c apt-get install -y nonexistent-package" did not complete successfully: exit code: 100`,
		},
		{
			name:   "a last line containing a URL alongside text is left alone",
			stderr: "failed to solve: pull access denied, see https://docs.docker.com/go/auth/\n",
			want:   "failed to solve: pull access denied, see https://docs.docker.com/go/auth/",
		},
		{
			name: "both streams empty",
			want: "",
		},
		{
			name:   "stdout only keeps its unlabelled form",
			stdout: "boom\n",
			want:   "boom",
		},
		{
			name:   "stderr only keeps its unlabelled form",
			stderr: "boom\n",
			want:   "boom",
		},
		{
			name:   "both streams are labelled",
			stdout: "out line\n",
			stderr: "err line\n",
			want:   "stdout: out line | stderr: err line",
		},
		{
			name:   "the fallback applies per stream",
			stdout: "https://example.invalid/only\n",
			stderr: wslWrapperMessage,
			want:   "stdout: https://example.invalid/only | stderr: The command 'docker' could not be found in this WSL 2 distro.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := summarizeFailure([]byte(tc.stdout), []byte(tc.stderr))
			if got != tc.want {
				t.Errorf("summarizeFailure() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSummarizeFailureNeverReportsABareURLAlone is the property the ticket
// was actually filed about, stated directly rather than as a line-picking
// rule: whatever else it does, the summary must not be a naked link.
func TestSummarizeFailureNeverReportsABareURLAlone(t *testing.T) {
	got := summarizeFailure(nil, []byte(wslWrapperMessage))
	if isBareURL(got) {
		t.Errorf("summarizeFailure returned a bare URL %q; the user is told to visit a page instead of what went wrong", got)
	}
}

func TestFirstLine(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"leading blank lines", "\n\n  first\nsecond\n", "first"},
		{"no trailing newline", "only", "only"},
		{"all whitespace", "   \n\t\n", ""},
		{"empty", "", ""},
		{"trims the line it returns", "   padded   \nnext\n", "padded"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := firstLine([]byte(tc.in)); got != tc.want {
				t.Errorf("firstLine(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsBareURL(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"https://docs.docker.com/go/wsl2/", true},
		{"http://example.invalid", true},
		{"see https://docs.docker.com/go/wsl2/ for details", false},
		{"failed to solve", false},
		{"", false},
	}

	for _, tc := range cases {
		if got := isBareURL(tc.in); got != tc.want {
			t.Errorf("isBareURL(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestClassifyFailureWSLIntegrationOff asserts both the answer and the
// ordering. The `docker version` probe cannot distinguish this case from a
// dead daemon, because the message comes from Docker Desktop's shim rather
// than from the daemon, so the branch has to run BEFORE the probe. Asserting
// on the fake's recorded calls is what pins that: if the probe ran, the
// branch is in the wrong place, even though the returned anchor would look
// right in a test that only checked the anchor.
func TestClassifyFailureWSLIntegrationOff(t *testing.T) {
	fake := &fakeRunner{}
	rt := &dockerRuntime{name: "shellforge-sandbox", image: "shellforge-sandbox", run: fake}

	err := rt.classifyFailure(context.Background(), "build the sandbox image",
		errors.New("docker build exited 1"), []byte(wslWrapperMessage))

	var uxErr *ux.Error
	if !errors.As(err, &uxErr) {
		t.Fatalf("classifyFailure returned a bare error, so a learner sees no remediation: %v", err)
	}
	if uxErr.DocAnchor != "docker-wsl-integration-off" {
		t.Errorf("DocAnchor = %q, want docker-wsl-integration-off", uxErr.DocAnchor)
	}
	if uxErr.Remediation == "" {
		t.Error("remediation is empty, which non-negotiable 6 forbids")
	}
	if !strings.Contains(uxErr.Remediation, "WSL integration") {
		t.Errorf("remediation does not name the Docker Desktop setting to change: %q", uxErr.Remediation)
	}
	if len(fake.calls) != 0 {
		t.Errorf("the `docker version` probe ran %d time(s) before the WSL branch was reached: %v\n"+
			"The branch must come first: the probe fails identically for a dead daemon and would send a "+
			"user whose Docker is healthy to \"start Docker\".", len(fake.calls), fake.calls)
	}
}

// TestClassifyFailureRegressionPins covers the two paths this ticket must not
// change, so a future edit to the WSL branch cannot quietly capture them.
func TestClassifyFailureRegressionPins(t *testing.T) {
	// Docker capitalises its own product name, so the message carries
	// "Docker daemon socket". This branch matched lowercase only until #74
	// and therefore never fired in practice; both casings are pinned so the
	// real one cannot regress and the test cannot pass on a fiction.
	for _, tc := range []struct {
		name   string
		stderr string
	}{
		{
			name:   "as docker really prints it",
			stderr: "Got permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock",
		},
		{
			name:   "all lowercase",
			stderr: "permission denied while trying to connect to the docker daemon socket at unix:///var/run/docker.sock",
		},
	} {
		t.Run("permission denied wins with no probe: "+tc.name, func(t *testing.T) {
			fake := &fakeRunner{}
			rt := &dockerRuntime{name: "shellforge-sandbox", image: "shellforge-sandbox", run: fake}

			err := rt.classifyFailure(context.Background(), "start the sandbox container", errors.New("exit 1"), []byte(tc.stderr))

			var uxErr *ux.Error
			if !errors.As(err, &uxErr) {
				t.Fatalf("got a bare error: %v", err)
			}
			if uxErr.DocAnchor != "docker-permission-denied" {
				t.Errorf("DocAnchor = %q, want docker-permission-denied: a user in the wrong group is being told to start a daemon that is already running", uxErr.DocAnchor)
			}
			if len(fake.calls) != 0 {
				t.Errorf("the probe ran for a case detected from docker's own message: %v", fake.calls)
			}
		})
	}

	// The same casing bug sat in the probe's own arm, so it gets its own pin.
	t.Run("the probe arm also recognises the real casing", func(t *testing.T) {
		fake := &fakeRunner{results: []fakeResult{{
			stderr: []byte("Got permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock"),
			code:   1,
		}}}
		rt := &dockerRuntime{name: "shellforge-sandbox", image: "shellforge-sandbox", run: fake}

		err := rt.classifyFailure(context.Background(), "build the sandbox image", errors.New("exit 1"),
			[]byte("failed to solve: exit code 1\n"))

		var uxErr *ux.Error
		if !errors.As(err, &uxErr) {
			t.Fatalf("got a bare error: %v", err)
		}
		if uxErr.DocAnchor != "docker-permission-denied" {
			t.Errorf("DocAnchor = %q, want docker-permission-denied", uxErr.DocAnchor)
		}
	})

	t.Run("an unrecognised failure still falls back to the probe", func(t *testing.T) {
		fake := &fakeRunner{results: []fakeResult{{code: 1}}}
		rt := &dockerRuntime{name: "shellforge-sandbox", image: "shellforge-sandbox", run: fake}

		err := rt.classifyFailure(context.Background(), "build the sandbox image", errors.New("exit 1"),
			[]byte("something nobody has seen before\n"))

		var uxErr *ux.Error
		if !errors.As(err, &uxErr) {
			t.Fatalf("got a bare error: %v", err)
		}
		if uxErr.DocAnchor != "docker-daemon-down" {
			t.Errorf("DocAnchor = %q, want docker-daemon-down", uxErr.DocAnchor)
		}
		if len(fake.calls) != 1 {
			t.Fatalf("the probe ran %d time(s), want exactly 1: %v", len(fake.calls), fake.calls)
		}
		if got := strings.Join(fake.calls[0], " "); got != "docker version" {
			t.Errorf("probe argv = %q, want \"docker version\"", got)
		}
	})

	t.Run("a healthy daemon leaves the error unclassified for the caller to wrap", func(t *testing.T) {
		fake := &fakeRunner{results: []fakeResult{{code: 0}}}
		rt := &dockerRuntime{name: "shellforge-sandbox", image: "shellforge-sandbox", run: fake}

		err := rt.classifyFailure(context.Background(), "build the sandbox image", errors.New("exit 1"),
			[]byte("a build failure unrelated to the daemon\n"))

		var uxErr *ux.Error
		if errors.As(err, &uxErr) {
			t.Errorf("classifyFailure invented a classification for a healthy daemon: anchor %q", uxErr.DocAnchor)
		}
	})
}

// TestCombinedOutput pins the concatenation order and that neither input is
// mutated: append on a zero-length slice literal is the pattern that stays
// safe here, but the equivalent append(stdout, stderr...) would silently
// corrupt a caller's stdout slice if it happened to have spare capacity.
func TestCombinedOutput(t *testing.T) {
	stdout := []byte("out\n")
	stderr := []byte("err\n")

	got := combinedOutput(stdout, stderr)
	if string(got) != "out\nerr\n" {
		t.Errorf("combinedOutput = %q, want %q", got, "out\nerr\n")
	}
	if string(stdout) != "out\n" || string(stderr) != "err\n" {
		t.Errorf("combinedOutput mutated an input: stdout=%q stderr=%q", stdout, stderr)
	}
}

// TestProvisionClassifiesAWSLMessageOnStdout is the review finding on #103:
// classifyFailure only ever saw stderr, but summarizeFailure's own doc
// comment already establishes that docker does not pick a stream by
// convention (BuildKit writes to stdout, the classic builder to stderr),
// and the WSL stub message has no daemon behind it to pick one either. The
// reproduction that opened #74 did not establish which stream carried it.
//
// This drives the failure through Provision, the real caller, rather than
// calling classifyFailure directly: classifyFailure's own logic was always
// stream-agnostic once handed the right bytes, so a unit test that hands it
// the bytes directly cannot tell the difference between "the call site
// combines both streams" and "the call site still passes stderr alone and
// happens to work when the fixture puts the message on stderr." The fake's
// build result below puts the message on stdout ONLY, with empty stderr,
// which the pre-fix code (stderr only) could not have classified.
func TestProvisionClassifiesAWSLMessageOnStdout(t *testing.T) {
	fake := &fakeRunner{results: []fakeResult{
		{code: 1}, // docker image inspect: miss
		{stdout: []byte(wslWrapperMessage), code: 1}, // docker build: fails, message on stdout only
	}}
	rt := &dockerRuntime{name: "shellforge-sandbox", image: "shellforge-sandbox", run: fake}

	err := rt.Provision(context.Background(), runtime.ImageSpec{Name: "shellforge-sandbox"})

	var uxErr *ux.Error
	if !errors.As(err, &uxErr) {
		t.Fatalf("Provision returned a bare error, so a learner sees no remediation: %v", err)
	}
	if uxErr.DocAnchor != "docker-wsl-integration-off" {
		t.Errorf("DocAnchor = %q, want docker-wsl-integration-off: the message was on stdout and must still be found", uxErr.DocAnchor)
	}
	// Exactly two docker invocations: the image inspect miss and the failed
	// build. If this is more than two, classifyFailure's `docker version`
	// probe ran, meaning the stdout-only message was NOT recognised and the
	// code fell through past the branch under test.
	if len(fake.calls) != 2 {
		t.Errorf("docker was invoked %d time(s), want exactly 2 (image inspect, build): %v\n"+
			"A third call means the WSL branch was missed and the `docker version` probe ran instead.",
			len(fake.calls), fake.calls)
	}
}

// TestProvisionClassifiesAPermissionDeniedMessageOnStdout is the same
// finding applied to the other stream-sensitive branch this function has:
// isPermissionDenied must also see stdout, not only stderr.
func TestProvisionClassifiesAPermissionDeniedMessageOnStdout(t *testing.T) {
	fake := &fakeRunner{results: []fakeResult{
		{code: 1}, // docker image inspect: miss
		{stdout: []byte("Got permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock"), code: 1},
	}}
	rt := &dockerRuntime{name: "shellforge-sandbox", image: "shellforge-sandbox", run: fake}

	err := rt.Provision(context.Background(), runtime.ImageSpec{Name: "shellforge-sandbox"})

	var uxErr *ux.Error
	if !errors.As(err, &uxErr) {
		t.Fatalf("Provision returned a bare error, so a learner sees no remediation: %v", err)
	}
	if uxErr.DocAnchor != "docker-permission-denied" {
		t.Errorf("DocAnchor = %q, want docker-permission-denied: the message was on stdout and must still be found", uxErr.DocAnchor)
	}
	if len(fake.calls) != 2 {
		t.Errorf("docker was invoked %d time(s), want exactly 2: %v", len(fake.calls), fake.calls)
	}
}
