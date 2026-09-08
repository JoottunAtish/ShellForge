package bugreport

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/doctor"
)

// bundleTimeLayout is the timestamp shape in a bundle's file name and its
// zip top-level directory: shellforge-bugreport-<YYYYMMDD-HHMMSS>.
const bundleTimeLayout = "20060102-150405"

// truncationMarkerFmt is prepended to a log file Write truncates, naming
// exactly how much of it survived.
const truncationMarkerFmt = "... truncated: only the last %d bytes of this file are included ...\n"

// BundleName returns the zip's base name for a given time, in the
// documented shellforge-bugreport-<YYYYMMDD-HHMMSS>.zip shape.
func BundleName(t time.Time) string {
	return "shellforge-bugreport-" + t.Format(bundleTimeLayout) + ".zip"
}

// logFile is one *.log file Write is considering for the bundle, already
// read and truncated to at most MaxLogBytes.
type logFile struct {
	name string
	data []byte
}

// Write writes the zip to w. It never opens a file itself, so the caller
// owns the refuse-to-overwrite decision.
//
// The layout is exactly four kinds of entry under one top-level directory
// named shellforge-bugreport-<stamp>, stamp derived from r.GeneratedAt:
// README.txt, report.json (r, json.MarshalIndent with two spaces),
// doctor.txt (doctor.RenderTable's output), and logs/, one entry per *.log
// file found in logDir, tail-truncated to MaxLogBytes. logDir missing or
// empty produces no logs/ entry at all and no error. If the truncated logs
// together would push the bundle over MaxZipBytes, every log is dropped
// instead, and a Note in report.json says so.
func Write(w io.Writer, r Report, logDir string) error {
	stamp := r.GeneratedAt.Format(bundleTimeLayout)
	top := "shellforge-bugreport-" + stamp

	readme := []byte(bundleReadme)

	var doctorBuf strings.Builder
	if err := doctor.RenderTable(&doctorBuf, r.Doctor); err != nil {
		return fmt.Errorf("render doctor.txt: %w", err)
	}
	doctorTxt := []byte(doctorBuf.String())

	logs, err := collectLogs(logDir)
	if err != nil {
		return fmt.Errorf("read the log directory: %w", err)
	}

	reportJSON, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report.json: %w", err)
	}

	logsTotal := 0
	for _, l := range logs {
		logsTotal += len(l.data)
	}
	overhead := len(readme) + len(doctorTxt) + len(reportJSON)
	if overhead+logsTotal > MaxZipBytes {
		logs = nil
		r.Notes = append(append([]string(nil), r.Notes...), fmt.Sprintf(noteLogsDroppedFmt, MaxZipBytes))
		reportJSON, err = json.MarshalIndent(r, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal report.json: %w", err)
		}
	}

	zw := zip.NewWriter(w)
	if err := writeZipEntry(zw, top+"/README.txt", readme); err != nil {
		return err
	}
	if err := writeZipEntry(zw, top+"/report.json", reportJSON); err != nil {
		return err
	}
	if err := writeZipEntry(zw, top+"/doctor.txt", doctorTxt); err != nil {
		return err
	}
	for _, l := range logs {
		if err := writeZipEntry(zw, top+"/logs/"+l.name, l.data); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("finish the bug report archive: %w", err)
	}
	return nil
}

// writeZipEntry creates one zip entry and writes data to it.
func writeZipEntry(zw *zip.Writer, name string, data []byte) error {
	f, err := zw.Create(name)
	if err != nil {
		return fmt.Errorf("create zip entry %q: %w", name, err)
	}
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write zip entry %q: %w", name, err)
	}
	return nil
}

// collectLogs reads every *.log file directly inside logDir, tail-truncating
// anything over MaxLogBytes and prepending a marker line when it does. A
// missing or empty logDir returns no entries and no error: nothing in the
// tree writes to platform.LogDir() today, so this is the common case.
func collectLogs(logDir string) ([]logFile, error) {
	if logDir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(logDir)
	if err != nil {
		// Any failure to list the log directory means no logs, never a
		// failed bundle. fs.ErrNotExist is the common case (nothing writes
		// there yet), but it is not the only one: a cache path shadowed by
		// a regular file gives ENOTDIR on Unix, and an unreadable directory
		// gives EACCES. Logs are a nicety on top of the report, so letting
		// one of those abort the whole command would deny a learner the
		// bundle over the least valuable thing in it, which is the opposite
		// of what this package is for.
		return nil, nil
	}

	var out []logFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		// #nosec G304 -- logDir is platform.LogDir(), a Shellforge-resolved
		// path under CacheDir(), and e.Name() is a single directory entry
		// name that os.ReadDir just returned for that same directory, so it
		// carries no separator and cannot traverse. Neither half comes from
		// a level pack, a flag, or anything else a learner controls.
		data, err := os.ReadFile(filepath.Join(logDir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read log %q: %w", e.Name(), err)
		}
		if len(data) > MaxLogBytes {
			tail := data[len(data)-MaxLogBytes:]
			marker := fmt.Sprintf(truncationMarkerFmt, MaxLogBytes)
			data = append([]byte(marker), tail...)
		}
		out = append(out, logFile{name: e.Name(), data: data})
	}
	return out, nil
}

// bundleReadme is README.txt, the same for every bundle: what is in here,
// what is not, and that nothing was uploaded.
const bundleReadme = `This is a Shellforge bug report bundle.

What is in here:
  report.json   machine-readable diagnostics: the build you are running,
                the doctor report, what could be learned about your
                sandbox, your content pack's identity, a count-only summary
                of your progress, and your command history if you passed
                --journal.
  doctor.txt    the same table "shellforge doctor" prints, as text.
  logs/         the tail of any debug log files Shellforge had written on
                this machine, if any existed.

What is never in here:
  command output, your environment variables, and the progress database
  file itself. Your progress shows up only as counts, never as the
  database.

Command text is included only when you ran bug-report with --journal. When
it is included, known secret shapes are redacted, but the redaction is a
closed list, not a guarantee: a secret typed in a shape the list does not
know about can survive. Read this bundle yourself before you attach it to
an issue.

Nothing here was uploaded anywhere. This zip file sits on your machine
until you attach it somewhere yourself. Shellforge has no telemetry.
`
