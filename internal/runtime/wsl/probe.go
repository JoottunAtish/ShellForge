package wsl

import (
	"context"
	"os/exec"
	goruntime "runtime"
)

// Version2Available reports whether this host can currently provide a WSL2
// distribution for Shellforge to import its own into. It is read-only: it
// never registers, starts, stops, or removes a distribution, and it takes
// no distribution name and constructs no wslRuntime. It exists so
// internal/sandbox can tell "Windows with WSL2" apart from "Windows with
// WSL1 only" without a second UTF-16LE decoder outside this package; see
// list.go's decodeUTF16LE and parseList, which this reuses rather than
// duplicates.
//
// This is a destructive-path package (see wsl.go's sandboxDistro comment),
// so this export is deliberately narrow, on purpose, for a reviewer to
// check: it holds no destroy target, and on any GOOS other than "windows"
// it runs nothing at all, not even exec.LookPath, before reporting false.
func Version2Available(ctx context.Context) (ok bool, detail string) {
	return version2Available(ctx, goruntime.GOOS, realVersion2Probe)
}

// version2ProbeFunc runs `wsl -l -v` and returns the distribution rows it
// reports, or an error only when the check itself could not be performed.
// "no distributions registered" is reported as a nil error and zero rows,
// not as an error: it is the honest, common answer on a clean Windows
// machine, not a failure.
type version2ProbeFunc func(ctx context.Context) ([]distro, error)

// version2Available is the seam probe_test.go drives directly: goos and
// probe are ordinary parameters, not package variables, so the "runs no
// subprocess outside Windows" guarantee is provable with a recording probe
// and no global mutable state.
func version2Available(ctx context.Context, goos string, probe version2ProbeFunc) (ok bool, detail string) {
	if goos != "windows" {
		return false, "the WSL backend runs only on Windows"
	}

	rows, err := probe(ctx)
	if err != nil {
		return false, "could not check WSL2 availability: " + err.Error()
	}
	return classifyVersion2(rows)
}

// realVersion2Probe is the real, on-host probe: it resolves wsl.exe on
// PATH and hands off to probeVersion2 for the rest.
func realVersion2Probe(ctx context.Context) ([]distro, error) {
	bin, err := exec.LookPath("wsl.exe")
	if err != nil {
		return nil, err
	}
	return probeVersion2(ctx, execRunner{bin: bin})
}

// probeVersion2 is the seam probe_test.go drives directly: r is an
// ordinary parameter, not a package variable, in the same style
// version2Available's own probe parameter already uses, so a fake runner
// can exercise every exit shape with no subprocess and no global mutable
// state.
//
// It runs `wsl -l -v` and parses the result with the package's existing
// decoder. A non-zero exit is classified through recognizedNonZeroExit, the
// same helper listRows uses in wsl.go: a recognized failure signature on
// stdout or stderr is a genuine error, and anything else non-zero is read
// as "no distributions registered", not a failure, because there is
// nothing importing yet for this probe to have broken.
func probeVersion2(ctx context.Context, r runner) ([]distro, error) {
	stdout, stderr, code, err := r.run(ctx, []string{"wsl.exe", "-l", "-v"}, nil)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		if failErr := recognizedNonZeroExit(code, stdout, stderr); failErr != nil {
			return nil, failErr
		}
		return nil, nil
	}
	return parseList(stdout)
}

// classifyVersion2 reports whether rows describe a host WSL2 is usable on.
//
// Zero rows means no distribution is registered at all, which is not a
// refusal: Provision imports our own at version 2, so a clean machine is
// available, not unavailable. One or more rows that are all version 1 means
// WSL2 itself is unavailable, since Shellforge requires WSL2. A mix of
// version 1 and version 2 rows, or any version 2 row on its own, is
// available.
func classifyVersion2(rows []distro) (ok bool, detail string) {
	if len(rows) == 0 {
		return true, ""
	}
	for _, row := range rows {
		if row.Version == 2 {
			return true, ""
		}
	}
	return false, "every registered distribution reports WSL version 1, not WSL version 2"
}
