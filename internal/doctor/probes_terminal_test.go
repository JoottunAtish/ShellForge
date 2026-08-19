package doctor

import (
	"context"
	"testing"
)

// TestTerminalVTProbeIsFullyPopulated exercises the real
// platform.SupportsVirtualTerminal path: on every platform other than
// Windows it always reports OK, since ANSI needs no console mode change
// there.
func TestTerminalVTProbeIsFullyPopulated(t *testing.T) {
	p := terminalVTProbe{baseProbe: baseProbe{id: "terminal_vt_support", docAnchor: docAnchorTerminalNoVT}}
	res := p.Run(context.Background())
	if res.ID != "terminal_vt_support" {
		t.Errorf("ID = %q, want terminal_vt_support", res.ID)
	}
	if res.Detail == "" || res.Remediation == "" || res.DocAnchor == "" {
		t.Errorf("terminal_vt_support result has an empty required field: %+v", res)
	}
}

// TestWindowsTerminalProbeIsFullyPopulated exercises the real
// platform.IsWindowsTerminal path, which reports false with a detail on
// every non-Windows platform, never an error.
func TestWindowsTerminalProbeIsFullyPopulated(t *testing.T) {
	p := windowsTerminalProbe{baseProbe: baseProbe{id: "windows_terminal", docAnchor: docAnchorTerminalNotWT, windowsOnly: true}}
	res := p.Run(context.Background())
	if res.ID != "windows_terminal" {
		t.Errorf("ID = %q, want windows_terminal", res.ID)
	}
	if res.Detail == "" || res.Remediation == "" || res.DocAnchor == "" {
		t.Errorf("windows_terminal result has an empty required field: %+v", res)
	}
	// This probe never fails: a learner not using Windows Terminal is
	// missing convenience, not correctness.
	if res.Status == Fail {
		t.Errorf("Status = Fail, want OK or Warn: not using Windows Terminal is never a hard failure")
	}
}

func TestTerminalProbesReportInterruptedOnACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	vt := terminalVTProbe{baseProbe: baseProbe{id: "terminal_vt_support", docAnchor: docAnchorTerminalNoVT}}
	if res := vt.Run(ctx); res.Status != Warn {
		t.Errorf("terminal_vt_support on a cancelled context = %v, want Warn", res.Status)
	}

	wt := windowsTerminalProbe{baseProbe: baseProbe{id: "windows_terminal", docAnchor: docAnchorTerminalNotWT, windowsOnly: true}}
	if res := wt.Run(ctx); res.Status != Warn {
		t.Errorf("windows_terminal on a cancelled context = %v, want Warn", res.Status)
	}
}
