package journal

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// maxTSVLine bounds how many bytes ReadTSV accumulates for one line before
// giving up on it and treating it as malformed. bufio.Reader.ReadString
// otherwise grows its result without limit, so a journal.tsv whose last
// line never terminates, or one a learner filled with `yes >> journal.tsv`,
// would be read entirely into a single Go string and could exhaust host
// memory. 64 KiB is generously larger than any real command line: even a
// pathological one built from this game's own levels does not come close,
// and a legitimate record this long has nothing left to teach by being
// accepted.
const maxTSVLine = 64 * 1024

// ReadTSV parses the four field TSV format images/rc/instrument.bash writes
// on every PROMPT_COMMAND invocation: EPOCHREALTIME, exit code, PWD, then
// the command line, tab separated, one record per line.
//
// A tab inside the command (field four) is always safe: see parseTSVLine's
// doc comment for exactly why, and for the one case that is not safe, a tab
// inside PWD (field three).
//
// A malformed line is skipped, not fatal, and there are three different
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
//   - Over-long line anywhere: a line exceeding maxTSVLine bytes before its
//     terminator is skipped the same way a malformed line is, and reading
//     resumes at the next '\n' rather than growing the buffer further.
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
		line, overLong, err := readBoundedLine(br)
		terminated := err == nil

		if terminated && !overLong {
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
		// content would otherwise have parsed. An over-long line is
		// dropped the same way a malformed line is, per the rule above.

		if err == io.EOF {
			break
		}
		if err != nil {
			return entries, fmt.Errorf("read journal line: %w", err)
		}
	}

	return entries, nil
}

// readBoundedLine reads one line from br the way ReadTSV needs: like
// br.ReadString('\n'), except that it never accumulates more than
// maxTSVLine bytes for a single line.
//
// br.ReadString('\n') cannot be used directly for this, even with a
// post-hoc length check: it keeps reading from the underlying io.Reader
// until it finds the delimiter, so a single call on an adversarial line
// with no '\n' anywhere would already have pulled the whole thing into one
// Go string, memory bound violated, before this function ever saw it. This
// reads with br.ReadSlice('\n') instead, which returns bufio.ErrBufferFull
// once its own internal buffer (a few KiB) fills without finding the
// delimiter, so this can decide it is over budget and stop copying
// fragments into line long before an over-long line is ever fully in
// memory at once; it still has to keep asking br for more fragments until
// the delimiter or EOF, because the alternative is trusting an unverified
// byte count in the file to tell it where to resume, which is exactly the
// kind of file content this function must not trust.
//
// The returned line is only meaningful when overLong is false; when it is
// true, the caller treats the record as malformed and moves on, exactly
// per ReadTSV's over-long forgiveness rule.
func readBoundedLine(br *bufio.Reader) (line string, overLong bool, err error) {
	var buf []byte
	for {
		frag, e := br.ReadSlice('\n')
		if !overLong {
			if len(buf)+len(frag) > maxTSVLine {
				overLong = true
				buf = nil // already over budget; nothing more to accumulate
			} else {
				buf = append(buf, frag...)
			}
		}

		if e == nil {
			// frag ended in '\n': the line, terminated, is fully read.
			break
		}
		if e == bufio.ErrBufferFull {
			// No '\n' in this fragment yet, but this is only an internal
			// buffer boundary, not the end of input: keep reading more of
			// the same line.
			continue
		}
		// Any other error, io.EOF or a genuine read error, ends the line
		// here whether or not it reached a terminator.
		err = e
		break
	}

	if overLong {
		return "", true, err
	}
	return string(buf), false, err
}

// parseTSVLine parses one already unterminated TSV line into an Entry. ok
// is false when the line is malformed in shape: fewer than four fields, an
// unparsable timestamp, or an unparsable exit code.
//
// The SplitN bound to four fields is what makes a tab inside the command
// (field four) safe: however many tabs the command contains, they all stay
// inside fields[3]. It does not make a tab inside PWD (field three) safe.
// instrument.bash emits PWD unescaped as field three, so a working
// directory containing a literal tab, from something like
// `mkdir $'a\tb' && cd $_`, splits wrong: the part of the path after the
// tab is pushed into fields[3] ahead of the real command, so the returned
// Cwd is truncated and Raw carries a garbage prefix. This is not corrected
// here, because the fix belongs in the producer (percent-encoding PWD the
// way instrument.bash already does for the OSC 7 marker) and that change is
// tracked separately; this function has no way to tell a tab that belongs
// in PWD apart from one that legitimately starts the command.
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
