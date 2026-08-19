package doctor

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// TestClassifyDockerOutputCoversEveryDockerState is the extra test the
// ticket's own test plan names, including the Windows-container-mode row.
func TestClassifyDockerOutputCoversEveryDockerState(t *testing.T) {
	tests := []struct {
		name        string
		runErr      error
		code        int
		stdout      string
		stderr      string
		wantPresent Level
		wantDaemon  Level
	}{
		{"binary not found", exec.ErrNotFound, -1, "", "", Fail, Warn},
		{"healthy linux daemon", nil, 0, "28.0.1 linux", "", OK, OK},
		{"windows container mode", nil, 0, "28.0.1 windows", "", OK, Warn},
		{"daemon down", nil, 1, "", "Cannot connect to the Docker daemon at unix:///var/run/docker.sock", OK, Fail},
		{"exit 0 empty stdout", nil, 0, "", "", OK, Warn},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := classifyDockerOutput(tt.runErr, tt.code, tt.stdout, tt.stderr)
			if o.Present != tt.wantPresent {
				t.Errorf("docker_present with %s = %v, want %v", tt.name, o.Present, tt.wantPresent)
			}
			if o.Daemon != tt.wantDaemon {
				t.Errorf("docker_daemon_running with %s = %v, want %v", tt.name, o.Daemon, tt.wantDaemon)
			}
		})
	}
}

func TestClassifyDockerOutputDaemonDownIsDistinctFromMissingBinary(t *testing.T) {
	missing := classifyDockerOutput(exec.ErrNotFound, -1, "", "")
	down := classifyDockerOutput(nil, 1, "", "docker: Cannot connect to the Docker daemon")
	if missing.Present == down.Present {
		t.Error("a missing binary and a present-but-unreachable daemon must not classify docker_present the same way")
	}
}

func TestDockerPresentProbeIsFullyPopulated(t *testing.T) {
	p := dockerPresentProbe{baseProbe: baseProbe{id: "docker_present", docAnchor: docAnchorDockerNotFound}, r: newFakeRunner(fakeResponse{stdout: []byte("28.0.1 linux")})}
	res := p.Run(context.Background())
	if res.Status != OK {
		t.Errorf("Status = %v, want OK", res.Status)
	}
	if res.Detail == "" || res.Remediation == "" || res.DocAnchor == "" {
		t.Errorf("docker_present result has an empty required field: %+v", res)
	}
}

func TestDockerDaemonProbeNamesSwitchingToLinuxContainers(t *testing.T) {
	p := dockerDaemonProbe{baseProbe: baseProbe{id: "docker_daemon_running", docAnchor: docAnchorDockerDaemonDown}, r: newFakeRunner(fakeResponse{stdout: []byte("28.0.1 windows")})}
	res := p.Run(context.Background())
	if res.Status != Warn {
		t.Fatalf("Status = %v, want Warn for a Windows-container daemon", res.Status)
	}
	if !strings.Contains(res.Remediation, "Linux containers") {
		t.Errorf("Remediation = %q, want it to name switching to Linux containers", res.Remediation)
	}
}

func TestDockerDaemonProbeFailsWhenBinaryMissing(t *testing.T) {
	p := dockerDaemonProbe{baseProbe: baseProbe{id: "docker_daemon_running", docAnchor: docAnchorDockerDaemonDown}, r: newFakeRunner(fakeResponse{err: exec.ErrNotFound, code: -1})}
	res := p.Run(context.Background())
	if res.Status != Warn {
		t.Errorf("Status = %v, want Warn: nothing to check when the binary is missing", res.Status)
	}
}

func TestDockerServerInfoIsPure(t *testing.T) {
	r := newFakeRunner(fakeResponse{stdout: []byte("28.0.1 linux")})
	o := dockerServerInfo(context.Background(), r)
	if o.Present != OK || o.Daemon != OK {
		t.Errorf("dockerServerInfo = %+v, want (OK, OK)", o)
	}
	if r.callCount() != 1 {
		t.Errorf("dockerServerInfo made %d calls, want exactly 1", r.callCount())
	}
}
