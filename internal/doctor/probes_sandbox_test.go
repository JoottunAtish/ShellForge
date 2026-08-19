package doctor

import (
	"context"
	"errors"
	"testing"
)

func TestSandboxHealthProbeWithNilProber(t *testing.T) {
	p := sandboxHealthProbe{baseProbe: baseProbe{id: "sandbox_health", docAnchor: docAnchorSandboxUnhealthy}, prober: nil}
	res := p.Run(context.Background())
	if res.Status != Warn {
		t.Errorf("Status = %v, want Warn when no runtime was supplied", res.Status)
	}
	if res.Detail == "" || res.Remediation == "" || res.DocAnchor == "" {
		t.Errorf("sandbox_health result has an empty required field: %+v", res)
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
