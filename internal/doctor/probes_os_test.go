package doctor

import (
	"context"
	"testing"
)

func TestClassifyKernelRelease(t *testing.T) {
	tests := []struct {
		release string
		want    Level
	}{
		{"5.15.0-91-generic", OK},
		{"4.19.0", OK},
		{"4.18.0", Fail},
		{"3.10.0-1160.el7.x86_64", Fail},
		{"6.6.30-linuxkit", OK},
		{"not-a-kernel-string", Warn},
		{"", Warn},
	}
	for _, tt := range tests {
		t.Run(tt.release, func(t *testing.T) {
			if got := classifyKernelRelease(tt.release); got != tt.want {
				t.Errorf("classifyKernelRelease(%q) = %v, want %v", tt.release, got, tt.want)
			}
		})
	}
}

// TestClassifyWindowsBuild is tested on the ubuntu leg, per the plan's
// design: the pure classifier for Windows behaviour is unit tested on
// Linux, since only the syscall wrapper that gathers the build number sits
// behind a build tag.
func TestClassifyWindowsBuild(t *testing.T) {
	tests := []struct {
		build uint32
		want  Level
	}{
		{19040, Fail},
		{19041, Warn},
		{19044, Warn},
		{19045, OK},
		{22631, OK},
		{10240, Fail},
	}
	for _, tt := range tests {
		if got := classifyWindowsBuild(tt.build); got != tt.want {
			t.Errorf("classifyWindowsBuild(%d) = %v, want %v", tt.build, got, tt.want)
		}
	}
}

func TestClassifyCPUFlags(t *testing.T) {
	tests := []struct {
		name    string
		cpuinfo string
		want    Level
	}{
		{"vmx present", "flags\t\t: fpu vme de pse vmx tsc", OK},
		{"svm present", "flags\t\t: fpu vme de pse svm tsc", OK},
		{"neither present", "flags\t\t: fpu vme de pse tsc", Fail},
		{"empty", "", Fail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyCPUFlags(tt.cpuinfo); got != tt.want {
				t.Errorf("classifyCPUFlags(%q) = %v, want %v", tt.cpuinfo, got, tt.want)
			}
		})
	}
}

// TestOSVersionProbeIsFullyPopulated exercises osVersionProbe.Run directly,
// against a fake runner, on whichever platform this binary is compiled for.
func TestOSVersionProbeIsFullyPopulated(t *testing.T) {
	p := osVersionProbe{baseProbe: baseProbe{id: "os_version", docAnchor: docAnchorOSTooOld}, r: newFakeRunner(fakeResponse{stdout: []byte("5.15.0-91-generic\n")})}
	res := p.Run(context.Background())
	if res.ID != "os_version" {
		t.Errorf("ID = %q, want os_version", res.ID)
	}
	if res.Detail == "" || res.Remediation == "" || res.DocAnchor == "" {
		t.Errorf("os_version result has an empty required field: %+v", res)
	}
}

// TestCPUVirtualizationProbeIsFullyPopulated exercises the probe body,
// which reads /proc/cpuinfo directly on non-Windows and shells out on
// Windows; either way the result must be fully populated.
func TestCPUVirtualizationProbeIsFullyPopulated(t *testing.T) {
	p := cpuVirtualizationProbe{baseProbe: baseProbe{id: "cpu_virtualization", docAnchor: docAnchorVirtDisabled}, r: newFakeRunner(fakeResponse{stdout: []byte("True\n")})}
	res := p.Run(context.Background())
	if res.Detail == "" || res.Remediation == "" || res.DocAnchor == "" {
		t.Errorf("cpu_virtualization result has an empty required field: %+v", res)
	}
}

func TestVMPlatformProbeClassifiesDismState(t *testing.T) {
	tests := []struct {
		name   string
		stdout string
		code   int
		err    error
		want   Level
	}{
		{"enabled", "Enabled\n", 0, nil, OK},
		{"disabled", "Disabled\n", 0, nil, Fail},
		{"powershell failed", "", 1, nil, Warn},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := vmPlatformProbe{baseProbe: baseProbe{id: "vm_platform_feature", docAnchor: docAnchorVMPlatformMissing, windowsOnly: true}, r: newFakeRunner(fakeResponse{stdout: []byte(tt.stdout), code: tt.code, err: tt.err})}
			res := p.Run(context.Background())
			if res.Status != tt.want {
				t.Errorf("Status = %v, want %v (detail: %s)", res.Status, tt.want, res.Detail)
			}
		})
	}
}
