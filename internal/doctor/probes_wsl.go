package doctor

import (
	"context"
	"fmt"
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
	outcome := classifyWSLList(text)
	switch outcome.Level {
	case OK:
		return p.result(OK, "the Shellforge sandbox distribution is on WSL version 2", "No action needed.")
	case Fail:
		return p.result(Fail, "the Shellforge sandbox distribution is on WSL version 1",
			"Run `wsl --set-default-version 2`, then `shellforge sandbox rebuild`.")
	default:
		if outcome.OtherVersion1Name != "" {
			return p.result(Warn, fmt.Sprintf("%s is on WSL version 1; this only matters if the Shellforge sandbox lands there", outcome.OtherVersion1Name),
				"No action needed. If `shellforge init` has trouble provisioning the sandbox, run `wsl --set-default-version 2` first.")
		}
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
// passes through unchanged, odd length included: "Kernel version:
// 5.15.146.1\n" is 27 bytes and must come back exactly as written, not as
// the empty string. Odd length only means invalid input when the data is
// UTF-16 shaped in the first place, checked by the BOM and the
// looks-like-UTF16LE heuristic below, since real UTF-16LE text can never
// have an odd byte count.
func decodeWSLText(data []byte) string {
	if len(data) == 0 {
		return ""
	}

	hasBOM := len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE
	if hasBOM {
		body := data[2:]
		if len(body)%2 != 0 {
			return ""
		}
		return decodeUTF16LEUnits(body)
	}

	if looksLikeUTF16LE(data) {
		if len(data)%2 != 0 {
			return ""
		}
		return decodeUTF16LEUnits(data)
	}

	return string(data)
}

// decodeUTF16LEUnits decodes body, an even-length byte slice with no byte
// order mark, as a sequence of little-endian UTF-16 code units.
func decodeUTF16LEUnits(body []byte) string {
	units := make([]uint16, len(body)/2)
	for i := range units {
		units[i] = uint16(body[2*i]) | uint16(body[2*i+1])<<8
	}
	return string(utf16.Decode(units))
}

// looksLikeUTF16LE reports whether data, a byte slice with no byte order
// mark, is plausibly UTF-16LE text in the ASCII range: at least three
// quarters of its high bytes are zero. Real UTF-8 text does not have that
// shape. data may have an odd length; the trailing byte is simply excluded
// from both the pair count and the scan, since it cannot be part of a
// complete UTF-16 code unit either way.
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

// wslListOutcome is what classifyWSLList determined from one `wsl -l -v`
// listing: the overall Level, plus, only when Level is Warn because some
// other distribution (not the Shellforge sandbox) is on WSL version 1, the
// name of that distribution, so the caller can say which one and why it
// only matters if the sandbox lands there.
type wslListOutcome struct {
	Level             Level
	OtherVersion1Name string
}

// classifyWSLList reads the VERSION column of a decoded `wsl -l -v`
// listing. If the Shellforge sandbox distribution is present, only its row
// matters: OK at version 2, Fail at version 1, because that is a real
// blocker. If it is absent, a learner's own WSL 1 distribution existing
// alongside it is not a blocker: Shellforge imports its own WSL 2
// distribution regardless of what else is installed, so that case is Warn,
// naming the distribution, not Fail. A listing with no distribution rows at
// all is also Warn: that is the normal state before `shellforge init` has
// run, not a broken machine.
func classifyWSLList(text string) wslListOutcome {
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
		return wslListOutcome{Level: Warn}
	}

	for _, r := range rows {
		if strings.EqualFold(r.name, sandboxDistroName) {
			if r.version == "2" {
				return wslListOutcome{Level: OK}
			}
			return wslListOutcome{Level: Fail}
		}
	}

	for _, r := range rows {
		if r.version != "2" {
			return wslListOutcome{Level: Warn, OtherVersion1Name: r.name}
		}
	}
	return wslListOutcome{Level: OK}
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
