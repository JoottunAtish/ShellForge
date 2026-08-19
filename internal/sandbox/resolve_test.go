package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/runtime/wsl"
)

// fakeAvailable is a table-driven double for the Available seam. Every call
// is recorded, in order, so a test can assert which backends were actually
// asked about and which were not.
type fakeAvailable struct {
	wslOK        bool
	wslDetail    string
	dockerOK     bool
	dockerDetail string
	calls        []Backend
}

func (f *fakeAvailable) probe(_ context.Context, b Backend) (bool, string) {
	f.calls = append(f.calls, b)
	switch b {
	case WSL:
		return f.wslOK, f.wslDetail
	case Docker:
		return f.dockerOK, f.dockerDetail
	default:
		return false, "unknown backend"
	}
}

// TestDecideChoosesTheExpectedBackend is the table covering every
// combination of platform, requested backend, and availability that Decide
// must get right.
func TestDecideChoosesTheExpectedBackend(t *testing.T) {
	tests := []struct {
		name         string
		want         Backend
		goos         string
		wslOK        bool
		wslDetail    string
		dockerOK     bool
		dockerDetail string
		wantChosen   Backend
		wantFallback bool
		wantErr      bool
	}{
		{
			name:       "windows_with_wsl2",
			want:       Auto,
			goos:       "windows",
			wslOK:      true,
			dockerOK:   true,
			wantChosen: WSL,
		},
		{
			name:         "windows_without_wsl",
			want:         Auto,
			goos:         "windows",
			wslOK:        false,
			wslDetail:    "no WSL2 distribution",
			dockerOK:     true,
			wantChosen:   Docker,
			wantFallback: true,
		},
		{
			name:         "windows_with_wsl1_only",
			want:         Auto,
			goos:         "windows",
			wslOK:        false,
			wslDetail:    "every registered distribution reports WSL version 1, not WSL version 2",
			dockerOK:     true,
			wantChosen:   Docker,
			wantFallback: true,
		},
		{
			name:       "linux_with_docker",
			want:       Auto,
			goos:       "linux",
			dockerOK:   true,
			wantChosen: Docker,
		},
		{
			name:       "darwin_with_docker",
			want:       Auto,
			goos:       "darwin",
			dockerOK:   true,
			wantChosen: Docker,
		},
		{
			name:       "explicit_docker_on_windows_with_wsl_available",
			want:       Docker,
			goos:       "windows",
			wslOK:      true,
			dockerOK:   true,
			wantChosen: Docker,
		},
		{
			name:       "explicit_docker_on_linux",
			want:       Docker,
			goos:       "linux",
			dockerOK:   true,
			wantChosen: Docker,
		},
		{
			name:       "explicit_wsl_on_windows",
			want:       WSL,
			goos:       "windows",
			wslOK:      true,
			wantChosen: WSL,
		},
		{
			name:     "linux_without_docker",
			want:     Auto,
			goos:     "linux",
			dockerOK: false,
			wantErr:  true,
		},
		{
			name:    "explicit_wsl_on_linux",
			want:    WSL,
			goos:    "linux",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeAvailable{
				wslOK:        tt.wslOK,
				wslDetail:    tt.wslDetail,
				dockerOK:     tt.dockerOK,
				dockerDetail: tt.dockerDetail,
			}
			choice, err := Decide(context.Background(), Options{
				Want:      tt.want,
				GOOS:      tt.goos,
				Available: fake.probe,
			})

			if tt.wantErr {
				if err == nil {
					t.Fatalf("Decide() = %+v, nil, want an error", choice)
				}
				return
			}
			if err != nil {
				t.Fatalf("Decide() error = %v, want nil", err)
			}
			if choice.Chosen != tt.wantChosen {
				t.Errorf("Chosen = %q, want %q", choice.Chosen, tt.wantChosen)
			}
			if choice.Fallback != tt.wantFallback {
				t.Errorf("Fallback = %v, want %v", choice.Fallback, tt.wantFallback)
			}
			if strings.TrimSpace(choice.Reason) == "" {
				t.Error("Reason is empty, want a non-empty explanation")
			}
			if !strings.Contains(strings.ToLower(choice.Reason), strings.ToLower(string(choice.Chosen))) {
				t.Errorf("Reason %q does not mention the chosen backend %q", choice.Reason, choice.Chosen)
			}
		})
	}
}

// TestDecideFallbackOnWindowsIsAnnouncedAndNotSilent pins the "it never
// picks silently" contract: the printed-later Reason must carry both the
// chosen backend's name and the detail explaining why the other one was
// skipped.
func TestDecideFallbackOnWindowsIsAnnouncedAndNotSilent(t *testing.T) {
	fake := &fakeAvailable{
		wslOK:     false,
		wslDetail: "no WSL2 distribution",
		dockerOK:  true,
	}
	choice, err := Decide(context.Background(), Options{
		Want:      Auto,
		GOOS:      "windows",
		Available: fake.probe,
	})
	if err != nil {
		t.Fatalf("Decide() error = %v, want nil", err)
	}
	if choice.Chosen != Docker {
		t.Fatalf("Chosen = %q, want %q", choice.Chosen, Docker)
	}
	if !choice.Fallback {
		t.Error("Fallback = false, want true")
	}
	if !strings.Contains(choice.Reason, "docker") {
		t.Errorf("Reason %q does not mention docker", choice.Reason)
	}
	if !strings.Contains(choice.Reason, "no WSL2 distribution") {
		t.Errorf("Reason %q does not carry the Available detail", choice.Reason)
	}
}

// TestDecideWithNoUsableBackendReturnsAUxErrorWithARemediationAndAnchor
// covers the case neither backend can be used at all.
func TestDecideWithNoUsableBackendReturnsAUxErrorWithARemediationAndAnchor(t *testing.T) {
	fake := &fakeAvailable{wslOK: false, dockerOK: false}
	_, err := Decide(context.Background(), Options{
		Want:      Auto,
		GOOS:      "windows",
		Available: fake.probe,
	})
	if err == nil {
		t.Fatal("Decide() error = nil, want an error")
	}

	var uxErr *ux.Error
	if !errors.As(err, &uxErr) {
		t.Fatalf("Decide() error = %v (%T), want it to errors.As into *ux.Error", err, err)
	}
	if strings.TrimSpace(uxErr.Remediation) == "" {
		t.Error("Remediation is empty, want a next command")
	}
	if uxErr.DocAnchor != "no-runtime-available" {
		t.Errorf("DocAnchor = %q, want %q", uxErr.DocAnchor, "no-runtime-available")
	}
}

// TestDecideRefusesTheWSLBackendOutsideWindows checks the platform refusal
// itself, and that it happens before any probe is consulted, since the
// platform answer needs no probe at all.
func TestDecideRefusesTheWSLBackendOutsideWindows(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			fake := &fakeAvailable{wslOK: true, dockerOK: true}
			_, err := Decide(context.Background(), Options{
				Want:      WSL,
				GOOS:      goos,
				Available: fake.probe,
			})
			if err == nil {
				t.Fatal("Decide() error = nil, want a refusal")
			}

			var uxErr *ux.Error
			if !errors.As(err, &uxErr) {
				t.Fatalf("Decide() error = %v (%T), want *ux.Error", err, err)
			}
			if uxErr.DocAnchor != "wsl-not-installed" {
				t.Errorf("DocAnchor = %q, want %q", uxErr.DocAnchor, "wsl-not-installed")
			}
			if !strings.Contains(uxErr.Remediation, "shellforge init --runtime=docker") {
				t.Errorf("Remediation = %q, want it to name `shellforge init --runtime=docker`", uxErr.Remediation)
			}
			if len(fake.calls) != 0 {
				t.Errorf("Available was consulted %d times, want 0: the platform answer needs no probe", len(fake.calls))
			}
		})
	}
}

// TestParseBackendRefusesAnUnknownValue covers every shape of bad input the
// --runtime flag can carry: an unrelated word, the empty string, a
// mistyped case-or-whitespace variant of a real value, and a flag-shaped
// value from an argument-parsing mixup.
func TestParseBackendRefusesAnUnknownValue(t *testing.T) {
	for _, in := range []string{"podman", "", "WSL ", "--yes"} {
		t.Run(in, func(t *testing.T) {
			_, err := ParseBackend(in)
			if err == nil {
				t.Fatalf("ParseBackend(%q) error = nil, want a refusal", in)
			}
			var uxErr *ux.Error
			if !errors.As(err, &uxErr) {
				t.Fatalf("ParseBackend(%q) error = %v (%T), want *ux.Error", in, err, err)
			}
			for _, name := range []string{"auto", "wsl", "docker"} {
				if !strings.Contains(uxErr.Remediation, name) {
					t.Errorf("ParseBackend(%q) remediation = %q, want it to name %q", in, uxErr.Remediation, name)
				}
			}
		})
	}
}

// TestParseBackendAcceptsTheThreeValues is ParseBackend's positive control.
func TestParseBackendAcceptsTheThreeValues(t *testing.T) {
	tests := []struct {
		in   string
		want Backend
	}{
		{"auto", Auto},
		{"wsl", WSL},
		{"docker", Docker},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseBackend(tt.in)
			if err != nil {
				t.Fatalf("ParseBackend(%q) error = %v, want nil", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ParseBackend(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// recordingRuntime is returned by the recording constructors below. It is
// never asked to do anything in these tests; it exists only so the
// constructor function values type-check.
type recordingRuntime struct{ runtime.Runtime }

// TestResolveWithAnUnknownBackendConstructsNoRuntime is the refusal one
// layer down from TestParseBackendRefusesAnUnknownValue: Resolve must
// refuse before it ever calls a constructor.
func TestResolveWithAnUnknownBackendConstructsNoRuntime(t *testing.T) {
	dockerCalls := 0
	wslCalls := 0

	_, _, err := Resolve(context.Background(), Options{
		Want: Backend("podman"),
		NewDocker: func(name, image string) (runtime.Runtime, error) {
			dockerCalls++
			return recordingRuntime{}, nil
		},
		NewWSL: func(name string) (runtime.Runtime, error) {
			wslCalls++
			return recordingRuntime{}, nil
		},
	})
	if err == nil {
		t.Fatal("Resolve() error = nil, want a refusal")
	}
	if dockerCalls != 0 {
		t.Errorf("NewDocker was called %d times, want 0", dockerCalls)
	}
	if wslCalls != 0 {
		t.Errorf("NewWSL was called %d times, want 0", wslCalls)
	}
}

// TestResolveConstructsTheChosenBackendOnly is Resolve's construction
// contract: exactly one constructor is called, with the compile-time names,
// and the other is never touched.
func TestResolveConstructsTheChosenBackendOnly(t *testing.T) {
	t.Run("wsl", func(t *testing.T) {
		var dockerCalls, wslCalls int
		var gotWSLName string

		fake := &fakeAvailable{wslOK: true, dockerOK: true}
		_, choice, err := Resolve(context.Background(), Options{
			Want:      Auto,
			GOOS:      "windows",
			Available: fake.probe,
			NewDocker: func(name, image string) (runtime.Runtime, error) {
				dockerCalls++
				return recordingRuntime{}, nil
			},
			NewWSL: func(name string) (runtime.Runtime, error) {
				wslCalls++
				gotWSLName = name
				return recordingRuntime{}, nil
			},
		})
		if err != nil {
			t.Fatalf("Resolve() error = %v, want nil", err)
		}
		if choice.Chosen != WSL {
			t.Errorf("Chosen = %q, want %q", choice.Chosen, WSL)
		}
		if wslCalls != 1 {
			t.Errorf("NewWSL was called %d times, want 1", wslCalls)
		}
		if dockerCalls != 0 {
			t.Errorf("NewDocker was called %d times, want 0", dockerCalls)
		}
		if gotWSLName != "shellforge-sandbox" {
			t.Errorf("NewWSL name = %q, want %q", gotWSLName, "shellforge-sandbox")
		}
	})

	t.Run("docker", func(t *testing.T) {
		var dockerCalls, wslCalls int
		var gotName, gotImage string

		fake := &fakeAvailable{dockerOK: true}
		_, choice, err := Resolve(context.Background(), Options{
			Want:      Auto,
			GOOS:      "linux",
			Available: fake.probe,
			NewDocker: func(name, image string) (runtime.Runtime, error) {
				dockerCalls++
				gotName = name
				gotImage = image
				return recordingRuntime{}, nil
			},
			NewWSL: func(name string) (runtime.Runtime, error) {
				wslCalls++
				return recordingRuntime{}, nil
			},
		})
		if err != nil {
			t.Fatalf("Resolve() error = %v, want nil", err)
		}
		if choice.Chosen != Docker {
			t.Errorf("Chosen = %q, want %q", choice.Chosen, Docker)
		}
		if dockerCalls != 1 {
			t.Errorf("NewDocker was called %d times, want 1", dockerCalls)
		}
		if wslCalls != 0 {
			t.Errorf("NewWSL was called %d times, want 0", wslCalls)
		}
		if gotName != "shellforge-sandbox" {
			t.Errorf("NewDocker name = %q, want %q", gotName, "shellforge-sandbox")
		}
		if gotImage != "shellforge-sandbox" {
			t.Errorf("NewDocker image = %q, want %q", gotImage, "shellforge-sandbox")
		}
	})
}

// TestSandboxNameIsAcceptedByTheWSLBackend guards against this package's
// distribution name constant drifting from internal/runtime/wsl's own
// sandboxDistro: that is the only way this layer could hand Destroy a
// target its own guard would refuse to act on.
//
// This test was verified to fail: with sandboxDistroName temporarily set to
// "shellforge-sandbox-x", wsl.New returned "... is not one of the
// Shellforge constants ...", which is the exact substring this test
// refuses to see, before the constant was restored.
func TestSandboxNameIsAcceptedByTheWSLBackend(t *testing.T) {
	_, err := wsl.New(sandboxDistroName)
	if err != nil && strings.Contains(err.Error(), "is not one of the Shellforge constants") {
		t.Fatalf("wsl.New(%q) = %v, want no name-drift refusal", sandboxDistroName, err)
	}
	// Any other error (most commonly wsl-not-installed, on a machine or CI
	// leg with no wsl.exe) is expected and accepted: this test is only
	// about the name, not about whether WSL itself is present.
}

// TestPlanForTheWSLBackendNamesAnAbsoluteInstallDirectory checks that the
// WSL removal plan names a real, absolute, on-disk path, not a value
// reused from the Docker branch.
func TestPlanForTheWSLBackendNamesAnAbsoluteInstallDirectory(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	plan, err := Plan(context.Background(), Choice{Chosen: WSL})
	if err != nil {
		t.Fatalf("Plan() error = %v, want nil", err)
	}

	if plan.Name != "shellforge-sandbox" {
		t.Errorf("Name = %q, want %q", plan.Name, "shellforge-sandbox")
	}
	if !filepath.IsAbs(plan.VerifyPath) {
		t.Errorf("VerifyPath = %q, want an absolute path", plan.VerifyPath)
	}
	wantDir, err := wsl.InstallDir()
	if err != nil {
		t.Fatalf("wsl.InstallDir: %v", err)
	}
	if plan.VerifyPath != wantDir {
		t.Errorf("VerifyPath = %q, want %q (wsl.InstallDir())", plan.VerifyPath, wantDir)
	}
	if !strings.Contains(plan.VerifyCommand, "wsl -l -v") {
		t.Errorf("VerifyCommand = %q, want it to mention `wsl -l -v`", plan.VerifyCommand)
	}

	var namesPath, mentionsProgressKept bool
	for _, item := range plan.Items {
		if strings.Contains(item, plan.VerifyPath) {
			namesPath = true
		}
		if strings.Contains(item, "progress") && strings.Contains(item, "not removed") {
			mentionsProgressKept = true
		}
	}
	if !namesPath {
		t.Errorf("Items = %v, want one entry naming VerifyPath %q", plan.Items, plan.VerifyPath)
	}
	if !mentionsProgressKept {
		t.Errorf("Items = %v, want one entry saying the progress database is not removed", plan.Items)
	}
}

// TestPlanForTheDockerBackendNamesTheContainerLeavesTheImageAndNoHostDirectory
// checks that the Docker removal plan never claims a host directory will be
// deleted, and that Plan does not reuse the WSL branch's install directory.
//
// This test used to be named
// TestPlanForTheDockerBackendNamesTheContainerAndImageAndNoHostDirectory and
// asserted that plan.Items named the image as something that would be
// removed. That assertion was wrong: dockerRuntime.Destroy only ever runs
// `docker rm -f`, never `docker rmi`, so the image is never removed. This
// rewrite corrects a wrong assertion that had locked the false claim in
// place; it does not loosen or bend a correct one.
func TestPlanForTheDockerBackendNamesTheContainerLeavesTheImageAndNoHostDirectory(t *testing.T) {
	plan, err := Plan(context.Background(), Choice{Chosen: Docker})
	if err != nil {
		t.Fatalf("Plan() error = %v, want nil", err)
	}

	if plan.Name != "shellforge-sandbox" {
		t.Errorf("Name = %q, want %q", plan.Name, "shellforge-sandbox")
	}
	if plan.VerifyPath != "" {
		t.Errorf("VerifyPath = %q, want empty for the Docker backend", plan.VerifyPath)
	}
	if !strings.Contains(plan.VerifyCommand, "docker ps -a") {
		t.Errorf("VerifyCommand = %q, want it to mention `docker ps -a`", plan.VerifyCommand)
	}
	if !strings.Contains(plan.VerifyCommand, "docker images") {
		t.Errorf("VerifyCommand = %q, want it to mention `docker images`, since the image is left behind", plan.VerifyCommand)
	}

	var namesContainer, namesImageAsRemoved, namesManualImageRemoval bool
	for _, item := range plan.Items {
		if strings.Contains(item, "container") {
			namesContainer = true
		}
		if strings.Contains(item, "image") {
			if strings.Contains(item, "docker rmi") {
				namesManualImageRemoval = true
			} else if !strings.Contains(item, "left in place") && !strings.Contains(item, "not removed") {
				namesImageAsRemoved = true
			}
		}
		if strings.Contains(item, string(os.PathSeparator)) {
			t.Errorf("Items entry %q looks like it names a host directory, which the Docker backend never removes", item)
		}
	}
	if !namesContainer {
		t.Errorf("Items = %v, want one entry naming the container", plan.Items)
	}
	if namesImageAsRemoved {
		t.Errorf("Items = %v, want no entry claiming the image is removed: Destroy never runs `docker rmi`", plan.Items)
	}
	if !namesManualImageRemoval {
		t.Errorf("Items = %v, want an entry naming `docker rmi shellforge-sandbox` as the manual step to remove the image", plan.Items)
	}
}
