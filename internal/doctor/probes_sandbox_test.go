package doctor

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/platform"
)

// createDataDir points platform.DataDir at a fresh directory under root and
// creates it, so a test exercising the prober-classification branches of
// sandboxHealthProbe.Run does not instead hit the new missing-data-directory
// branch that finding 4 added.
func createDataDir(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	setDataDirEnv(t, root)
	dataDir, err := platform.DataDir()
	if err != nil {
		t.Fatalf("platform.DataDir(): %v", err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("create the data directory: %v", err)
	}
}

func TestSandboxHealthProbeWithNilProber(t *testing.T) {
	createDataDir(t)
	p := sandboxHealthProbe{baseProbe: baseProbe{id: "sandbox_health", docAnchor: docAnchorSandboxUnhealthy}, prober: nil}
	res := p.Run(context.Background())
	if res.Status != Warn {
		t.Errorf("Status = %v, want Warn when no runtime was supplied", res.Status)
	}
	if res.Detail == "" || res.Remediation == "" || res.DocAnchor == "" {
		t.Errorf("sandbox_health result has an empty required field: %+v", res)
	}
	if res.Fixable {
		t.Errorf("Fixable = true with the data directory already present, want false")
	}
}

func TestSandboxHealthProbeClassifiesPingOutcomes(t *testing.T) {
	tests := []struct {
		name        string
		provisioned bool
		running     bool
		err         error
		want        Level
	}{
		{"not provisioned", false, false, nil, Warn},
		{"provisioned but not running", true, false, nil, Warn},
		{"provisioned and running", true, true, nil, OK},
		{"ping error", true, true, errors.New("connection refused"), Fail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			createDataDir(t)
			p := sandboxHealthProbe{
				baseProbe: baseProbe{id: "sandbox_health", docAnchor: docAnchorSandboxUnhealthy},
				prober:    fakeProber{provisioned: tt.provisioned, running: tt.running, detail: "detail", err: tt.err},
			}
			res := p.Run(context.Background())
			if res.Status != tt.want {
				t.Errorf("Status = %v, want %v", res.Status, tt.want)
			}
		})
	}
}

func TestSandboxHealthProbeReportsInterruptedOnACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := sandboxHealthProbe{baseProbe: baseProbe{id: "sandbox_health", docAnchor: docAnchorSandboxUnhealthy}, prober: fakeProber{provisioned: true, running: true}}
	if res := p.Run(ctx); res.Status != Warn {
		t.Errorf("Status = %v, want Warn on a cancelled context", res.Status)
	}
}

// TestSandboxHealthProbeIsFixableOnlyWhenDataDirMissing pins the finding-4
// move: this is now the one probe in the package where Fixable is ever
// true, and it is true purely because the data directory is missing,
// independent of what the prober reports.
func TestSandboxHealthProbeIsFixableOnlyWhenDataDirMissing(t *testing.T) {
	root := t.TempDir()
	setDataDirEnv(t, root)

	p := sandboxHealthProbe{
		baseProbe: baseProbe{id: "sandbox_health", docAnchor: docAnchorSandboxUnhealthy},
		prober:    fakeProber{provisioned: true, running: true},
	}
	res := p.Run(context.Background())
	if res.Status != Warn {
		t.Errorf("Status = %v, want Warn when the data directory is missing", res.Status)
	}
	if !res.Fixable {
		t.Error("Fixable = false with the data directory missing, want true")
	}

	dataDir, err := platform.DataDir()
	if err != nil {
		t.Fatalf("platform.DataDir(): %v", err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("create the data directory: %v", err)
	}

	res2 := p.Run(context.Background())
	if res2.Fixable {
		t.Error("Fixable = true once the data directory already exists, want false")
	}
	if res2.Status != OK {
		t.Errorf("Status = %v once the data directory exists and the prober reports healthy, want OK", res2.Status)
	}
}
