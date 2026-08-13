package journal

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// ReadTSV parses the four field TSV format images/rc/instrument.bash writes
// on every PROMPT_COMMAND invocation: EPOCHREALTIME, exit code, PWD, then
// the command line, tab separated, one record per line.
//
// A malformed line is skipped, not fatal, and there are two different
// forgiveness rules:
//
//   - Truncated final line: any final line not terminated by '\n' is
//     skipped, even if it happens to hold four well formed fields. printf
//     writes a line and its newline in one write, so a missing terminator
//     means the write was cut short mid-record, which is what a crash
//     mid-write produces.
//   - Malformed line anywhere: a line with fewer than four fields, an
//     unparsable timestamp, or an unparsable exit code is skipped wherever
//     it sits in the file, and the lines around it are still returned.
//
// Seq is the 1-based position among accepted lines, not among file lines.
// LevelID is left empty, and UsedTab and UsedHistory stay false, since the
// TSV carries none of them; the caller stamps LevelID.
//
// The returned error is only ever a read error from r that is not io.EOF.
// No malformed line ever fails the whole read.
func ReadTSV(r io.Reader) ([]Entry, error) {
	br := bufio.NewReader(r)
	var entries []Entry
	var seq int64

	for {
		line, err := br.ReadString('\n')
		terminated := err == nil

		if terminated {
			line = strings.TrimSuffix(line, "\n")
			line = strings.TrimSuffix(line, "\r")

			if e, ok := parseTSVLine(line); ok {
				seq++
				e.Seq = seq
				entries = append(entries, e)
			}
		}
		// An unterminated final line (err == io.EOF with leftover data) is
		// dropped unconditionally here: it is an incomplete record by the
		// completeness rule described above, regardless of whether its
		// content would otherwise have parsed.

		if err == io.EOF {
			break
		}
		if err != nil {
			return entries, fmt.Errorf("read journal line: %w", err)
		}
	}

	return entries, nil
}

// parseTSVLine parses one already unterminated TSV line into an Entry. ok
// is false when the line is malformed in shape: fewer than four fields, an
// unparsable timestamp, or an unparsable exit code.
func parseTSVLine(line string) (Entry, bool) {
	if line == "" {
		return Entry{}, false
	}

	fields := strings.SplitN(line, "\t", 4)
	if len(fields) < 4 {
		return Entry{}, false
	}

	ts, ok := parseEpochRealtime(fields[0])
	if !ok {
		return Entry{}, false
	}

	exit, err := strconv.Atoi(fields[1])
	if err != nil {
		return Entry{}, false
	}

	return Entry{
		TS:   ts,
		Cwd:  fields[2],
		Raw:  fields[3],
		Exit: exit,
	}, true
}

// parseEpochRealtime parses bash's EPOCHREALTIME format: integer seconds,
// optionally followed by a radix character and a fractional part. Bash
// emits the locale's own radix character, which is a comma rather than a
// dot under a comma-decimal locale, so both are accepted. There is no
// float arithmetic on the way in: the fraction is zero padded or truncated
// to six digits and parsed as an integer number of microseconds, so there
// is no rounding surprise, and a long fraction truncates rather than
// rounds.
func parseEpochRealtime(s string) (time.Time, bool) {
	intPart, fracPart := s, ""
	if idx := strings.IndexAny(s, ".,"); idx >= 0 {
		intPart, fracPart = s[:idx], s[idx+1:]
	}

	sec, err := strconv.ParseInt(intPart, 10, 64)
	if err != nil {
		return time.Time{}, false
	}

	switch {
	case len(fracPart) > 6:
		fracPart = fracPart[:6]
	case len(fracPart) < 6:
		fracPart += strings.Repeat("0", 6-len(fracPart))
	}
	micros, err := strconv.ParseInt(fracPart, 10, 64)
	if err != nil {
		return time.Time{}, false
	}

	return time.Unix(sec, micros*1000).UTC(), true
}
