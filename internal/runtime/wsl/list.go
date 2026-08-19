package wsl

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"
)

// distro is one row of `wsl -l -v` output, decoded and parsed. A distro
// value is only ever compared, counted, and displayed: it is never turned
// into an argv element. Every argv element naming a distribution comes from
// a compile-time constant in this package instead. See decision 14 in the
// Phase 1 plan and the destructive-safety skill: a garbled UTF-16 decode
// must fail safe, and a garbled name compared against a constant fails
// closed, but a garbled name passed into a command does not.
type distro struct {
	Name    string
	State   string
	Version int
	Default bool
}

// decodeUTF16LE decodes b as UTF-16LE, the encoding wsl.exe uses for its
// own stdout. A leading 0xFF 0xFE BOM is stripped if present. A leading
// 0xFE 0xFF BOM (big endian) is refused rather than silently decoded as
// byte-swapped garbage: wsl.exe never emits one, so seeing one means
// something upstream already went wrong, and continuing would produce a
// distribution name that looks plausible but is not what wsl.exe actually
// printed.
func decodeUTF16LE(b []byte) (string, error) {
	if len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF {
		return "", fmt.Errorf("wsl: refusing to decode a big-endian UTF-16 BOM (0xFE 0xFF); wsl.exe always writes little-endian")
	}
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		b = b[2:]
	}
	if len(b)%2 != 0 {
		return "", fmt.Errorf("wsl: UTF-16LE body has an odd number of bytes (%d), which cannot be a whole number of code units", len(b))
	}

	units := make([]uint16, len(b)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(b[i*2 : i*2+2])
	}
	return string(utf16.Decode(units)), nil
}

// splitLines splits decoded wsl.exe output on "\n" and strips a trailing
// "\r" from each line, so a CRLF-terminated line and a bare LF-terminated
// one are handled the same way.
func splitLines(s string) []string {
	raw := strings.Split(s, "\n")
	lines := make([]string, len(raw))
	for i, l := range raw {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}

// parseList decodes and parses `wsl -l -v` output.
//
// The first non-empty line is the localized header row and is dropped
// unconditionally rather than matched against expected text, because its
// wording changes with the Windows display language. Every remaining
// non-empty line is a distribution row: a leading "*" marks the default
// distribution, the last whitespace-separated field is the WSL version, the
// second-to-last is the state, and everything before that, rejoined with a
// single space, is the name. Taking the version and state from the
// right-hand end, rather than splitting on the first run of whitespace, is
// what lets a name containing a space (a real WSL name, since a user may
// register one that way) parse correctly.
//
// A row with fewer than three fields, or whose last field does not parse as
// an integer, is a refusal naming the offending row, not a skipped line: a
// row deep in the middle of a distribution list is either garbled input
// this package must not build an argv from, or a shape this parser does not
// yet understand, and neither is safe to silently drop.
func parseList(output []byte) ([]distro, error) {
	text, err := decodeUTF16LE(output)
	if err != nil {
		return nil, fmt.Errorf("wsl: parse `wsl -l -v` output: %w", err)
	}

	var rows []distro
	headerSeen := false
	for _, line := range splitLines(text) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !headerSeen {
			headerSeen = true
			continue
		}
		row, err := parseListRow(line)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// parseListRow parses one distribution row of `wsl -l -v` output. See
// parseList for the field-order rule.
func parseListRow(line string) (distro, error) {
	def := strings.HasPrefix(line, "*")
	body := strings.TrimSpace(strings.TrimPrefix(line, "*"))
	fields := strings.Fields(body)
	if len(fields) < 3 {
		return distro{}, fmt.Errorf("wsl: could not parse distribution row %q: want at least a name, a state, and a version", line)
	}

	versionField := fields[len(fields)-1]
	version, err := strconv.Atoi(versionField)
	if err != nil {
		return distro{}, fmt.Errorf("wsl: could not parse distribution row %q: version field %q is not an integer", line, versionField)
	}

	return distro{
		Name:    strings.Join(fields[:len(fields)-2], " "),
		State:   fields[len(fields)-2],
		Version: version,
		Default: def,
	}, nil
}

// parseQuietList decodes and parses `wsl -l -q` output, which is one
// distribution name per line and no header. Blank lines are dropped.
func parseQuietList(output []byte) ([]string, error) {
	text, err := decodeUTF16LE(output)
	if err != nil {
		return nil, fmt.Errorf("wsl: parse `wsl -l -q` output: %w", err)
	}

	var names []string
	for _, line := range splitLines(text) {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	return names, nil
}
