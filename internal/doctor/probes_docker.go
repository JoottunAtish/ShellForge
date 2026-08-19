package doctor

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// dockerVersionArgv is the one argv both Docker probes run. It is a
// machine-readable format string, never scraped prose, per the security
// skill's rule against parsing localized output.
var dockerVersionArgv = []string{"docker", "version", "--format", "{{.Server.Version}} {{.Server.Os}}"}

const remediationInstallDocker = "Install Docker. See docs/02-install-linux.md, or Docker Desktop on Windows and macOS."

// dockerPresentProbe checks whether the docker binary is on PATH.
type dockerPresentProbe struct {
	baseProbe
	r runner
}

func (p dockerPresentProbe) Run(ctx context.Context) Result {
	if err := ctx.Err(); err != nil {
		return p.interrupted(err)
	}
	o := dockerServerInfo(ctx, p.r)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return p.interrupted(ctxErr)
	}
	switch o.Present {
	case Fail:
		return p.result(Fail, "docker was not found on PATH", remediationInstallDocker)
	default:
		detail := "docker is on PATH"
		if o.ServerVersion != "" {
			detail = fmt.Sprintf("docker %s is on PATH", o.ServerVersion)
		}
		return p.result(OK, detail, "No action needed.")
	}
}

// dockerDaemonProbe checks whether the Docker daemon is reachable and
// running Linux containers, distinct from dockerPresentProbe because "not
// installed" and "installed but not running" need different remediations.
type dockerDaemonProbe struct {
	baseProbe
	r runner
}

func (p dockerDaemonProbe) Run(ctx context.Context) Result {
	if err := ctx.Err(); err != nil {
		return p.interrupted(err)
	}
	o := dockerServerInfo(ctx, p.r)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return p.interrupted(ctxErr)
	}
	switch o.Present {
	case Fail:
		return p.result(Warn, "docker is not installed, so the daemon was not checked",
			remediationInstallDocker)
	}

	switch o.Daemon {
	case OK:
		return p.result(OK, fmt.Sprintf("the Docker daemon is running Linux containers (%s)", nonEmpty(o.ServerVersion, "unknown version")),
			"No action needed.")
	case Fail:
		return p.result(Fail, "the Docker daemon is not reachable",
			"Fix on Linux: `sudo systemctl start docker`. Fix on Windows or macOS: start Docker Desktop and wait for it to report running.")
	default:
		if strings.EqualFold(o.ServerOS, "windows") {
			return p.result(Warn, "the Docker daemon is running Windows containers, not Linux containers",
				"Switch to Linux containers in Docker Desktop, then run `shellforge doctor` again.")
		}
		return p.result(Warn, "could not determine which containers the Docker daemon runs",
			"Run `docker version` yourself and check the output.")
	}
}

// dockerOutcome is what one `docker version` invocation tells both probes.
type dockerOutcome struct {
	Present       Level
	Daemon        Level
	ServerOS      string
	ServerVersion string
}

// dockerServerInfo runs dockerVersionArgv through r and classifies it. Both
// probes call this under their own context and their own budget, rather
// than sharing a memoised result: see the plan's note on why a shared cache
// costs more than running the fast call twice.
func dockerServerInfo(ctx context.Context, r runner) dockerOutcome {
	stdout, stderr, code, err := r.run(ctx, dockerVersionArgv, nil)
	return classifyDockerOutput(err, code, string(stdout), string(stderr))
}

// classifyDockerOutput turns one docker invocation's outcome into the pair
// of statuses the two docker probes need. A missing binary is (Fail, Warn):
// one failing row, and the daemon row says it was not checked rather than
// telling the learner to start something that is not installed. A non-zero
// exit with the binary present is (OK, Fail). A healthy Linux-mode daemon
// is (OK, OK). A daemon in Windows-container mode, or one that answered
// with no parseable body, is (OK, Warn).
func classifyDockerOutput(runErr error, code int, stdout, stderr string) dockerOutcome {
	_ = stderr // carried for a future richer detail message; not needed to classify.

	if runErr != nil && errors.Is(runErr, exec.ErrNotFound) {
		return dockerOutcome{Present: Fail, Daemon: Warn}
	}
	if code != 0 {
		return dockerOutcome{Present: OK, Daemon: Fail}
	}

	fields := strings.Fields(strings.TrimSpace(stdout))
	if len(fields) < 2 {
		return dockerOutcome{Present: OK, Daemon: Warn}
	}
	version, os := fields[0], fields[1]
	if strings.EqualFold(os, "windows") {
		return dockerOutcome{Present: OK, Daemon: Warn, ServerOS: os, ServerVersion: version}
	}
	return dockerOutcome{Present: OK, Daemon: OK, ServerOS: os, ServerVersion: version}
}

// nonEmpty returns s unless it is empty, in which case it returns fallback.
func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
