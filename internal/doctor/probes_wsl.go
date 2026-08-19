package doctor

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
)

// sandboxDistroName is the WSL distribution name Shellforge provisions.
// Duplicated here, rather than imported, because internal/doctor may not
// import internal/runtime/wsl: this package only ever reads WSL state, it
// never destroys anything, so the duplication carries none of the risk the
// destructive-safety skill warns about for the real constant.
const sandboxDistroName = "shellforge-sandbox"

const remediationInstallWSL = "Run `wsl --install --no-distribution` in an Administrator PowerShell, then reboot."

// wslInstalledProbe checks whether WSL is installed at all, by the exit
// code of `wsl --status`. Windows only.
type wslInstalledProbe struct {
	baseProbe
	r runner
}

func (p wslInstalledProbe) Run(ctx context.Context) Result {
	if err := ctx.Err(); err != nil {
		return p.interrupted(err)
	}

	_, _, code, err := p.r.run(ctx, []string{"wsl.exe", "--status"}, nil)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return p.interrupted(ctxErr)
	}
	if err != nil {
		return p.result(Fail, "wsl.exe could not be run: "+err.Error(), remediationInstallWSL)
	}
	if code == 0 {
		return p.result(OK, "WSL is installed", "No action needed.")
	}
	return p.result(Fail, "wsl --status exited "+strconv.Itoa(code), remediationInstallWSL)
}

// wslVersion2Probe checks that the Shellforge sandbox distribution, or
// every distribution when the sandbox is absent, is on WSL version 2, by
// reading `wsl -l -v`. Windows only.
type wslVersion2Probe struct {
	baseProbe
	r runner
}

func (p wslVersion2Probe) Run(ctx context.Context) Result {
	if err := ctx.Err(); err != nil {
		return p.interrupted(err)
	}

	stdout, _, code, err := p.r.run(ctx, []string{"wsl.exe", "-l", "-v"}, nil)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return p.interrupted(ctxErr)
	}
	if err != nil {
		return p.result(Warn, "could not list WSL distributions: "+err.Error(),
			"Run `wsl -l -v` yourself and check the output.")
	}
	if code != 0 {
		return p.result(Warn, "wsl -l -v exited "+strconv.Itoa(code),
			"Run `shellforge init` to create the Shellforge sandbox distribution.")
	}

	text := decodeWSLText(stdout)
	switch classifyWSLList(text) {
	case OK:
		return p.result(OK, "the Shellforge sandbox distribution is on WSL version 2", "No action needed.")
	case Fail:
		return p.result(Fail, "a WSL distribution is on WSL version 1",
			"Run `wsl --set-default-version 2`, then `shellforge sandbox rebuild`.")
	default:
		return p.result(Warn, "WSL reports no distributions yet",
			"Run `shellforge init`; this does not change any existing distribution.")
	}
}

// wslKernelProbe checks that the WSL2 Linux kernel is not outdated, by
// reading `wsl --version`. Windows only.
type wslKernelProbe struct {
	baseProbe
	r runner
}

func (p wslKernelProbe) Run(ctx context.Context) Result {
	if err := ctx.Err(); err != nil {
		return p.interrupted(err)
	}

	stdout, _, code, err := p.r.run(ctx, []string{"wsl.exe", "--version"}, nil)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return p.interrupted(ctxErr)
	}
	if err != nil {
		return p.result(Warn, "could not read the WSL kernel version: "+err.Error(),
			"Run `wsl --version` yourself and check the output.")
	}
	if code != 0 {
		return p.result(Warn, "wsl --version exited "+strconv.Itoa(code), "Run `wsl --update`, then run `shellforge doctor` again.")
	}

	text := decodeWSLText(stdout)
	switch classifyWSLVersion(text) {
	case OK:
		return p.result(OK, "the WSL2 kernel is current", "No action needed.")
	case Fail:
		return p.result(Fail, "the WSL2 kernel is outdated", "Run `wsl --update`.")
	default:
		return p.result(Warn, "could not parse the WSL kernel version from `wsl --version`",
			"Run `wsl --update`, then run `shellforge doctor` again.")
	}
}

// decodeWSLText decodes wsl.exe output, which is UTF-16LE, with or without
// a byte order mark. A UTF-8 byte slice, which is not what wsl.exe emits
// but is what a test fixture or a future non-Windows caller might pass,
// passes through unchanged. An empty slice or one with an odd length,
// which cannot be valid UTF-16, decodes to the empty string rather than
// producing garbage.
func decodeWSLText(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	if len(data)%2 != 0 {
		return ""
	}

	body := data
	hasBOM := len(body) >= 2 && body[0] == 0xFF && body[1] == 0xFE
	if hasBOM {
		body = body[2:]
	} else if !looksLikeUTF16LE(body) {
		return string(data)
	}
	if len(body)%2 != 0 {
		return ""
	}

	units := make([]uint16, len(body)/2)
	for i := range units {
		units[i] = uint16(body[2*i]) | uint16(body[2*i+1])<<8
	}
	return string(utf16.Decode(units))
}

// looksLikeUTF16LE reports whether data, an even-length byte slice with no
// byte order mark, is plausibly UTF-16LE text in the ASCII range: at least
// three quarters of its high bytes are zero. Real UTF-8 text does not have
// that shape.
func looksLikeUTF16LE(data []byte) bool {
	pairs := len(data) / 2
	if pairs == 0 {
		return false
	}
	zeros := 0
	for i := 0; i < pairs; i++ {
		if data[2*i+1] == 0 {
			zeros++
		}
	}
	return zeros*4 >= pairs*3
}

// wslListRowVersion is a compiled matcher for one row of `wsl -l -v`
// output: an optional "* " marker, a name, whitespace-separated columns,
// and a trailing version of 1 or 2.
var wslListRowVersion = regexp.MustCompile(`^\*?\s*(\S+)\s+.*\s([12])$`)

// classifyWSLList reads the VERSION column of a decoded `wsl -l -v`
// listing. If the Shellforge sandbox distribution is present, only its row
// matters: OK at version 2, Fail at version 1. Otherwise every listed
// distribution must be at version 2 for OK; any at version 1 is Fail. A
// listing with no distribution rows at all is Warn: that is the normal
// state before `shellforge init` has run, not a broken machine.
func classifyWSLList(text string) Level {
	type row struct {
		name    string
		version string
	}
	var rows []row
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		m := wslListRowVersion.FindStringSubmatch(trimmed)
		if m == nil {
			continue // the header row, or a line this format cannot read
		}
		rows = append(rows, row{name: m[1], version: m[2]})
	}

	if len(rows) == 0 {
		return Warn
	}

	for _, r := range rows {
		if strings.EqualFold(r.name, sandboxDistroName) {
			if r.version == "2" {
				return OK
			}
			return Fail
		}
	}

	for _, r := range rows {
		if r.version != "2" {
			return Fail
		}
	}
	return OK
}

// wslKernelVersionLine matches the "Kernel version: X.Y" line of
// `wsl --version` output, case insensitively.
var wslKernelVersionLine = regexp.MustCompile(`(?i)kernel version:\s*([0-9]+)\.([0-9]+)`)

// minWSLKernelMajor and minWSLKernelMinor are the oldest WSL2 kernel line
// still considered current.
const (
	minWSLKernelMajor = 5
	minWSLKernelMinor = 15
)

// classifyWSLVersion reads the kernel version line of `wsl --version`
// output. An unparseable body is Warn, not Fail: it is not evidence that
// the kernel is outdated.
func classifyWSLVersion(text string) Level {
	m := wslKernelVersionLine.FindStringSubmatch(text)
	if m == nil {
		return Warn
	}
	major, errMajor := strconv.Atoi(m[1])
	minor, errMinor := strconv.Atoi(m[2])
	if errMajor != nil || errMinor != nil {
		return Warn
	}
	if major > minWSLKernelMajor || (major == minWSLKernelMajor && minor >= minWSLKernelMinor) {
		return OK
	}
	return Fail
}
