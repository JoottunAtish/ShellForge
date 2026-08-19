package doctor

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// osVersionProbe checks the host OS version: the Linux kernel release on
// Linux and macOS, the Windows build number on Windows.
type osVersionProbe struct {
	baseProbe
	r runner
}

func (p osVersionProbe) Run(ctx context.Context) Result {
	if err := ctx.Err(); err != nil {
		return p.interrupted(err)
	}

	raw, err := osVersion(ctx, p.r)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return p.interrupted(ctxErr)
	}
	if err != nil {
		return p.result(Warn, "could not determine the OS version: "+err.Error(),
			"Run `shellforge doctor` again; if this keeps happening, open an issue with the output of `shellforge doctor --json`.")
	}

	if runtime.GOOS == "windows" {
		build, convErr := strconv.ParseUint(raw, 10, 32)
		if convErr != nil {
			return p.result(Warn, "could not parse the Windows build number from "+raw,
				"Run `winver` and confirm you are on build 19041 or newer.")
		}
		switch classifyWindowsBuild(uint32(build)) {
		case OK:
			return p.result(OK, fmt.Sprintf("Windows build %d", build), "No action needed.")
		case Warn:
			return p.result(Warn, fmt.Sprintf("Windows build %d is supported by WSL2 but out of support", build),
				"Run Windows Update, then run `shellforge doctor` again.")
		default:
			return p.result(Fail, fmt.Sprintf("Windows build %d is too old for WSL2", build),
				"Run Windows Update until `winver` reports build 19041 or newer.")
		}
	}

	switch classifyKernelRelease(raw) {
	case OK:
		return p.result(OK, fmt.Sprintf("kernel %s", raw), "No action needed.")
	case Warn:
		return p.result(Warn, "could not parse the kernel release "+raw,
			"Run `shellforge doctor` again; if this keeps happening, open an issue with the output of `shellforge doctor --json`.")
	default:
		return p.result(Fail, fmt.Sprintf("kernel %s is older than 4.19", raw),
			"Update your kernel through your distribution's package manager, then run `shellforge doctor` again.")
	}
}

// cpuVirtualizationProbe checks whether the CPU exposes hardware
// virtualization support and whether it is enabled in firmware.
type cpuVirtualizationProbe struct {
	baseProbe
	r runner
}

// cpuVirtWindowsArgv is a compile-time constant with no interpolation of
// any kind, so this is not the build-a-string-and-hand-it-to-a-shell
// pattern the security skill forbids: nothing variable ever enters it.
var cpuVirtWindowsArgv = []string{
	"powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
	"(Get-ComputerInfo).HyperVRequirementVirtualizationFirmwareEnabled",
}

const remediationEnableVirtualization = "Enable virtualization (Intel VT-x, AMD-V, or SVM Mode) in your firmware setup, then restart."

func (p cpuVirtualizationProbe) Run(ctx context.Context) Result {
	if err := ctx.Err(); err != nil {
		return p.interrupted(err)
	}

	if runtime.GOOS == "windows" {
		stdout, _, code, err := p.r.run(ctx, cpuVirtWindowsArgv, nil)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return p.interrupted(ctxErr)
		}
		if err != nil || code != 0 {
			return p.result(Warn, "could not determine the virtualization firmware state",
				"Check virtualization in your firmware setup, then run `shellforge doctor` again.")
		}
		out := strings.TrimSpace(string(stdout))
		switch {
		case strings.EqualFold(out, "True"):
			return p.result(OK, "virtualization is enabled in firmware", "No action needed.")
		case strings.EqualFold(out, "False"):
			return p.result(Fail, "virtualization is disabled in firmware", remediationEnableVirtualization)
		default:
			return p.result(Warn, "could not read the virtualization firmware state",
				"Check virtualization in your firmware setup, then run `shellforge doctor` again.")
		}
	}

	if runtime.GOARCH != "amd64" && runtime.GOARCH != "386" {
		return p.result(Warn, fmt.Sprintf("the vmx and svm flag check applies to x86 only; this machine is %s", runtime.GOARCH),
			"No action needed; virtualization support cannot be read from /proc/cpuinfo on this architecture.")
	}

	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return p.result(Warn, "could not read /proc/cpuinfo: "+err.Error(),
			"Check virtualization in your firmware setup if Docker fails to start.")
	}

	switch classifyCPUFlags(string(data)) {
	case OK:
		return p.result(OK, "virtualization flags (vmx or svm) are present in /proc/cpuinfo", "No action needed.")
	default:
		return p.result(Fail, "no vmx or svm flag found in /proc/cpuinfo", remediationEnableVirtualization)
	}
}

// vmPlatformProbe checks whether the Windows Virtual Machine Platform
// optional feature is enabled. Windows only.
type vmPlatformProbe struct {
	baseProbe
	r runner
}

var vmPlatformArgv = []string{
	"powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
	"(Get-WindowsOptionalFeature -Online -FeatureName VirtualMachinePlatform).State",
}

const remediationEnableVMPlatform = "Run `dism.exe /online /enable-feature /featurename:VirtualMachinePlatform /all /norestart` in an Administrator PowerShell, then reboot."

func (p vmPlatformProbe) Run(ctx context.Context) Result {
	if err := ctx.Err(); err != nil {
		return p.interrupted(err)
	}

	stdout, _, code, err := p.r.run(ctx, vmPlatformArgv, nil)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return p.interrupted(ctxErr)
	}
	if err != nil || code != 0 {
		return p.result(Warn, "could not determine whether Virtual Machine Platform is enabled", remediationEnableVMPlatform)
	}

	out := strings.TrimSpace(string(stdout))
	if strings.EqualFold(out, "Enabled") {
		return p.result(OK, "Virtual Machine Platform is enabled", "No action needed.")
	}
	return p.result(Fail, fmt.Sprintf("Virtual Machine Platform state is %q", out), remediationEnableVMPlatform)
}

// parseMajorMinor parses the leading "major.minor" out of a kernel release
// string such as "5.15.0-91-generic".
func parseMajorMinor(release string) (major, minor int, ok bool) {
	fields := strings.SplitN(release, ".", 3)
	if len(fields) < 2 {
		return 0, 0, false
	}
	major, errMajor := strconv.Atoi(fields[0])
	minor, errMinor := strconv.Atoi(fields[1])
	if errMajor != nil || errMinor != nil {
		return 0, 0, false
	}
	return major, minor, true
}

// classifyKernelRelease classifies a `uname -r` release string. A kernel
// older than 4.19 is Fail, because that is where overlay2 and cgroup v2
// become dependable; 4.19 or newer is OK. A release string this cannot
// parse is Warn, not Fail: an unparseable answer is not evidence of an old
// kernel.
func classifyKernelRelease(release string) Level {
	major, minor, ok := parseMajorMinor(release)
	if !ok {
		return Warn
	}
	if major > 4 || (major == 4 && minor >= 19) {
		return OK
	}
	return Fail
}

// classifyWindowsBuild classifies a Windows build number. Below 19041 is
// Fail, because WSL2 requires Windows 10 version 2004 (build 19041); 19041
// to 19044 is Warn, because WSL2 works but the build is out of support;
// 19045 and above is OK.
func classifyWindowsBuild(build uint32) Level {
	switch {
	case build < 19041:
		return Fail
	case build <= 19044:
		return Warn
	default:
		return OK
	}
}

// classifyCPUFlags classifies a /proc/cpuinfo body by the presence of the
// vmx (Intel VT-x) or svm (AMD-V) flag.
func classifyCPUFlags(cpuinfo string) Level {
	if strings.Contains(cpuinfo, "vmx") || strings.Contains(cpuinfo, "svm") {
		return OK
	}
	return Fail
}
