package sandbox

import (
	"context"
	"fmt"
	"os/exec"
	goruntime "runtime"
	"strings"

	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/runtime/docker"
	"github.com/JoottunAtish/ShellForge/internal/runtime/wsl"
)

// sandboxContainerName, sandboxImageName and sandboxDistroName are the
// closed set of compile-time names this package will ever construct a
// backend with. They match internal/runtime/wsl's own sandboxDistro
// constant for WSL: see TestSandboxNameIsAcceptedByTheWSLBackend, which
// fails the moment this package's WSL name drifts from the runtime's own
// constant.
const (
	sandboxContainerName = "shellforge-sandbox"
	sandboxImageName     = "shellforge-sandbox"
	sandboxDistroName    = "shellforge-sandbox"
)

// decideOp names what Decide is trying to do, for every ux.Fail it returns.
const decideOp = "choose a sandbox backend"

// Doc anchors this package hands to ux.Fail. Named as constants, not
// inlined, so TestEveryDocAnchorInThisPackageHasAHeading can resolve them
// the same way it resolves a literal.
const (
	anchorDockerNotFound     = "docker-not-found"
	anchorWSLNotInstalled    = "wsl-not-installed"
	anchorNoRuntimeAvailable = "no-runtime-available"
)

// Backend names which implementation Resolve should construct.
type Backend string

const (
	// Auto lets Decide choose: WSL2 on Windows when it is available,
	// Docker everywhere else, and Docker as an announced fallback on
	// Windows when WSL2 is not usable.
	Auto Backend = "auto"

	// WSL requests the WSL2 backend explicitly. Outside Windows this is
	// always a refusal: there is nothing to fall back to, because asking
	// for WSL specifically means the caller does not want the choice made
	// for them.
	WSL Backend = "wsl"

	// Docker requests the Docker backend explicitly.
	Docker Backend = "docker"
)

// ParseBackend parses the --runtime flag's value. Only the three exact,
// lowercase strings "auto", "wsl" and "docker" are accepted; anything else,
// including a different case, surrounding whitespace, or the empty string,
// is refused rather than normalised. A destroy target eventually derives
// from this value by way of Resolve, and destructive-safety's rule is to
// refuse an unexpected shape rather than guess what the caller meant.
func ParseBackend(s string) (Backend, error) {
	switch s {
	case string(Auto):
		return Auto, nil
	case string(WSL):
		return WSL, nil
	case string(Docker):
		return Docker, nil
	default:
		return "", ux.Fail(
			"understand the --runtime option",
			fmt.Errorf("unknown --runtime value %q", s),
			"Use --runtime=auto, --runtime=wsl or --runtime=docker. Leaving it out is the same as auto.",
			"",
		)
	}
}

// Available reports whether backend b is usable on this host right now,
// without provisioning, starting, or otherwise altering anything. ok true
// means the backend can be constructed and provisioned; detail is a short,
// human-readable reason, filled in whenever ok is false and optional when
// it is true.
//
// A nil Available in Options means the real, on-host probes: exec.LookPath
// for Docker, and wsl.Version2Available for WSL on Windows (false
// immediately, with no subprocess, on every other GOOS).
type Available func(ctx context.Context, b Backend) (ok bool, detail string)

// defaultAvailable is the real probe defaultAvailable substitutes for a nil
// Options.Available. It is cheap and never touches a daemon: it does not
// prove a backend will actually provision successfully, only that it is
// worth trying.
func defaultAvailable(ctx context.Context, b Backend) (bool, string) {
	switch b {
	case Docker:
		if _, err := exec.LookPath("docker"); err != nil {
			return false, "docker was not found on PATH"
		}
		return true, ""
	case WSL:
		return wsl.Version2Available(ctx)
	default:
		return false, "unknown backend"
	}
}

// Options configures Decide and Resolve. Every field's zero value means
// "use the real thing": an empty GOOS means goruntime.GOOS, a nil
// Available means defaultAvailable, and a nil NewDocker or NewWSL means
// docker.New or wsl.New. This is what lets every caller in production
// construct a bare Options{Want: ...} and get real behaviour, while a test
// substitutes any seam it needs without touching a package-level variable.
type Options struct {
	// Want is the backend the caller asked for. There is no default value
	// for this field: callers name Auto explicitly to get the
	// pick-for-me behaviour. See ParseBackend for parsing a flag into this.
	Want Backend

	// GOOS overrides runtime.GOOS for testing. Empty means the real value.
	GOOS string

	// Available overrides the availability probes for testing. Nil means
	// defaultAvailable.
	Available Available

	// NewDocker overrides the Docker backend constructor for testing. Nil
	// means docker.New.
	NewDocker func(name, image string) (runtime.Runtime, error)

	// NewWSL overrides the WSL backend constructor for testing. Nil means
	// wsl.New.
	NewWSL func(name string) (runtime.Runtime, error)
}

// Choice is what Decide picked, and why. Reason is one ASCII sentence
// naming the chosen backend and explaining the pick; it is never empty for
// a successful Choice, which is what makes "it never picks silently" a
// fact a test can assert rather than a hope. The field is named Chosen,
// not Backend, so that nothing comparing or switching on it needs an
// exemption from the no-branching-on-Status.Backend rule: that rule is
// about runtime.Status.Backend, a display-only field, not about this
// package's own selection outcome.
type Choice struct {
	// Chosen is the backend Decide picked.
	Chosen Backend

	// Reason is a human-readable sentence naming Chosen and why. This
	// package prints nothing itself; the caller renders Reason alongside
	// its own output.
	Reason string

	// Fallback reports that Chosen is not what Auto would prefer on this
	// platform: specifically, Docker chosen on Windows because WSL2 was
	// not usable. It is always false when Want was not Auto, since an
	// explicit request that succeeds is not a fallback.
	Fallback bool
}

// nonEmpty returns s, or fallback when s is empty or all whitespace.
func nonEmpty(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// failDockerNotFound builds the ux.Error Decide returns whenever Docker is
// the only backend that could apply and it is not usable.
func failDockerNotFound(detail string) error {
	return ux.Fail(
		decideOp,
		fmt.Errorf("docker is not usable on this host: %s", nonEmpty(detail, "docker was not found")),
		"Install Docker and run `shellforge doctor` again; see docs/02-install-linux.md.",
		anchorDockerNotFound,
	)
}

// Decide picks a backend without constructing anything. It never prints:
// the caller renders Choice.Reason alongside its own output.
//
// The order of operations matters and is tested: an explicit --runtime=wsl
// on a non-Windows host is refused from the platform check alone, before
// Available is ever consulted, because the platform answer needs no probe.
func Decide(ctx context.Context, opts Options) (Choice, error) {
	want, err := ParseBackend(string(opts.Want))
	if err != nil {
		return Choice{}, err
	}

	goos := opts.GOOS
	if goos == "" {
		goos = goruntime.GOOS
	}
	available := opts.Available
	if available == nil {
		available = defaultAvailable
	}

	switch want {
	case Docker:
		ok, detail := available(ctx, Docker)
		if !ok {
			return Choice{}, failDockerNotFound(detail)
		}
		return Choice{
			Chosen: Docker,
			Reason: "chose the docker backend because --runtime=docker was requested",
		}, nil

	case WSL:
		if goos != "windows" {
			return Choice{}, ux.Fail(
				decideOp,
				fmt.Errorf("the WSL backend was requested but this host is %q, not windows", goos),
				"The WSL backend runs only on Windows. On Linux and macOS run `shellforge init --runtime=docker`, or drop the flag and let Shellforge choose for you.",
				anchorWSLNotInstalled,
			)
		}
		ok, detail := available(ctx, WSL)
		if !ok {
			return Choice{}, ux.Fail(
				decideOp,
				fmt.Errorf("WSL2 is not usable on this host: %s", nonEmpty(detail, "no usable WSL2 distribution was found")),
				"Run `wsl --install --no-distribution` from an administrator PowerShell, then run `shellforge init` again, or run `shellforge init --runtime=docker` to use the Docker backend instead.",
				anchorWSLNotInstalled,
			)
		}
		return Choice{
			Chosen: WSL,
			Reason: "chose the wsl backend because --runtime=wsl was requested",
		}, nil

	case Auto:
		if goos == "windows" {
			wslOK, wslDetail := available(ctx, WSL)
			if wslOK {
				return Choice{
					Chosen: WSL,
					Reason: "chose the wsl backend because a WSL2 distribution is available",
				}, nil
			}

			dockerOK, dockerDetail := available(ctx, Docker)
			if dockerOK {
				return Choice{
					Chosen:   Docker,
					Fallback: true,
					Reason: fmt.Sprintf(
						"chose the docker backend because WSL2 is not available (%s)",
						nonEmpty(wslDetail, "no usable WSL2 distribution was found"),
					),
				}, nil
			}

			return Choice{}, ux.Fail(
				decideOp,
				fmt.Errorf("neither backend is usable: wsl: %s; docker: %s",
					nonEmpty(wslDetail, "unavailable"), nonEmpty(dockerDetail, "unavailable")),
				"Neither backend is usable on this machine. Run `shellforge doctor` to see which one is closest to working, then follow the fix it names.",
				anchorNoRuntimeAvailable,
			)
		}

		ok, detail := available(ctx, Docker)
		if !ok {
			return Choice{}, failDockerNotFound(detail)
		}
		return Choice{
			Chosen: Docker,
			Reason: "chose the docker backend because this platform has no WSL backend",
		}, nil

	default:
		// Unreachable: ParseBackend already refused anything that is not
		// Auto, WSL or Docker.
		return Choice{}, fmt.Errorf("sandbox: decide: unreachable backend %q", want)
	}
}

// Resolve decides a backend, then constructs it. An unknown or refused
// Want fails inside Decide, before either constructor is ever called: see
// TestResolveWithAnUnknownBackendConstructsNoRuntime.
func Resolve(ctx context.Context, opts Options) (runtime.Runtime, Choice, error) {
	choice, err := Decide(ctx, opts)
	if err != nil {
		return nil, Choice{}, err
	}

	newDocker := opts.NewDocker
	if newDocker == nil {
		newDocker = docker.New
	}
	newWSL := opts.NewWSL
	if newWSL == nil {
		newWSL = wsl.New
	}

	switch choice.Chosen {
	case WSL:
		rt, err := newWSL(sandboxDistroName)
		if err != nil {
			return nil, choice, err
		}
		return rt, choice, nil
	case Docker:
		rt, err := newDocker(sandboxContainerName, sandboxImageName)
		if err != nil {
			return nil, choice, err
		}
		return rt, choice, nil
	default:
		return nil, choice, fmt.Errorf("sandbox: resolve: unexpected chosen backend %q", choice.Chosen)
	}
}

// Spec returns the ImageSpec both backends provision from. It names no
// tag, reference or digest, meaning "the backend's own default image or
// rootfs" (see runtime.ImageSpec), and its Name matches the compile-time
// container, image and distribution constants this package resolves to.
func Spec() runtime.ImageSpec {
	return runtime.ImageSpec{Name: sandboxContainerName}
}

// RemovalPlan is what `sandbox destroy` is about to remove, in words a
// learner can read before confirming, plus what to check afterward.
type RemovalPlan struct {
	// Name is the sandbox name the caller must type back to confirm.
	Name string

	// Items describes, in plain sentences, exactly what will be removed.
	// It always includes a line stating that the progress database is a
	// separate concern and is not removed by this.
	Items []string

	// VerifyPath is the absolute host directory to check by hand if
	// removal is ever in doubt. Empty for the Docker backend, which
	// touches no host directory.
	VerifyPath string

	// VerifyCommand is the command that shows whether the sandbox is
	// really gone.
	VerifyCommand string
}

// Plan describes what removing c.Chosen's sandbox will do, without
// removing anything itself. cmd/shellforge prints the result verbatim
// before asking for confirmation, and again to verify removal afterward.
func Plan(ctx context.Context, c Choice) (RemovalPlan, error) {
	switch c.Chosen {
	case WSL:
		dir, err := wsl.InstallDir()
		if err != nil {
			return RemovalPlan{}, err
		}
		return RemovalPlan{
			Name: sandboxDistroName,
			Items: []string{
				fmt.Sprintf("the %q WSL distribution", sandboxDistroName),
				fmt.Sprintf("its install directory and backing .vhdx file, at %s", dir),
				"your progress database is not removed by this; that is a separate step",
			},
			VerifyPath:    dir,
			VerifyCommand: "wsl -l -v",
		}, nil

	case Docker:
		return RemovalPlan{
			Name: sandboxContainerName,
			Items: []string{
				fmt.Sprintf("the %q container", sandboxContainerName),
				fmt.Sprintf("the %q image is left in place; run `docker rmi %s` by hand if you want the disk space back", sandboxImageName, sandboxImageName),
				"your progress database is not removed by this; that is a separate step",
			},
			VerifyPath:    "",
			VerifyCommand: "docker ps -a and docker images",
		}, nil

	default:
		return RemovalPlan{}, fmt.Errorf("sandbox: plan: unexpected chosen backend %q", c.Chosen)
	}
}
