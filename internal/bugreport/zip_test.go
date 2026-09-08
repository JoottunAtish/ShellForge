package bugreport

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// readZip opens data as a zip archive and returns its entries keyed by name,
// with content read fully into memory: the bundles this test builds are
// small enough that this is simpler than streaming.
func readZip(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	out := make(map[string][]byte, len(zr.File))
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open entry %q: %v", f.Name, err)
		}
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(rc); err != nil {
			t.Fatalf("read entry %q: %v", f.Name, err)
		}
		rc.Close()
		out[f.Name] = buf.Bytes()
	}
	return out
}

func testReport(t time.Time) Report {
	return Report{
		GeneratedAt: t,
		Build:       Build{Version: "v0.1.0-test"},
	}
}

// TestWriteProducesTheFourEntriesUnderOneDirectory asserts the zip layout:
// README.txt, report.json, and doctor.txt under one shared top-level
// directory derived from GeneratedAt, with no ".." and no absolute name.
func TestWriteProducesTheFourEntriesUnderOneDirectory(t *testing.T) {
	stamp := time.Date(2026, 9, 8, 12, 30, 0, 0, time.UTC)
	r := testReport(stamp)

	var buf bytes.Buffer
	if err := Write(&buf, r, t.TempDir()); err != nil {
		t.Fatalf("Write: %v", err)
	}

	entries := readZip(t, buf.Bytes())
	wantPrefix := BundleName(stamp)
	wantPrefix = strings.TrimSuffix(wantPrefix, ".zip") + "/"

	wantNames := []string{wantPrefix + "README.txt", wantPrefix + "report.json", wantPrefix + "doctor.txt"}
	for _, name := range wantNames {
		if _, ok := entries[name]; !ok {
			t.Errorf("zip is missing entry %q; got %v", name, keys(entries))
		}
	}
	for name := range entries {
		if !strings.HasPrefix(name, wantPrefix) {
			t.Errorf("entry %q does not share the top-level prefix %q", name, wantPrefix)
		}
		if strings.Contains(name, "..") {
			t.Errorf("entry %q contains a .. segment", name)
		}
		if filepath.IsAbs(name) || strings.HasPrefix(name, "/") {
			t.Errorf("entry %q is an absolute name", name)
		}
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestWriteReportJSONParsesAndCarriesJournalIncludedFalse asserts report.json
// round trips through encoding/json and carries journal_included=false for a
// Report built without --journal.
func TestWriteReportJSONParsesAndCarriesJournalIncludedFalse(t *testing.T) {
	stamp := time.Date(2026, 9, 8, 12, 30, 0, 0, time.UTC)
	r := testReport(stamp)
	r.Notes = []string{"the command journal was not included. Run bug-report --journal to include it, with secrets redacted."}

	var buf bytes.Buffer
	if err := Write(&buf, r, ""); err != nil {
		t.Fatalf("Write: %v", err)
	}
	entries := readZip(t, buf.Bytes())

	prefix := strings.TrimSuffix(BundleName(stamp), ".zip") + "/"
	body, ok := entries[prefix+"report.json"]
	if !ok {
		t.Fatalf("report.json missing from %v", keys(entries))
	}

	var decoded Report
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("report.json does not parse: %v", err)
	}
	if decoded.JournalIncluded {
		t.Errorf("journal_included = true, want false")
	}
}

// TestWriteToleratesAMissingLogDir asserts a nonexistent logDir produces no
// error and no logs/ entry at all.
func TestWriteToleratesAMissingLogDir(t *testing.T) {
	stamp := time.Date(2026, 9, 8, 12, 30, 0, 0, time.UTC)
	r := testReport(stamp)

	var buf bytes.Buffer
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if err := Write(&buf, r, missing); err != nil {
		t.Fatalf("Write: %v", err)
	}

	entries := readZip(t, buf.Bytes())
	prefix := strings.TrimSuffix(BundleName(stamp), ".zip") + "/"
	for name := range entries {
		if strings.HasPrefix(name, prefix+"logs/") {
			t.Errorf("unexpected logs/ entry %q for a missing log directory", name)
		}
	}
}

// TestWriteTruncatesALargeLog asserts a log over MaxLogBytes is carried as
// exactly MaxLogBytes plus the truncation marker line, not the whole file.
func TestWriteTruncatesALargeLog(t *testing.T) {
	stamp := time.Date(2026, 9, 8, 12, 30, 0, 0, time.UTC)
	r := testReport(stamp)

	logDir := t.TempDir()
	big := bytes.Repeat([]byte("x"), 2*MaxLogBytes)
	if err := os.WriteFile(filepath.Join(logDir, "shellforge.log"), big, 0o600); err != nil {
		t.Fatalf("write fixture log: %v", err)
	}

	var buf bytes.Buffer
	if err := Write(&buf, r, logDir); err != nil {
		t.Fatalf("Write: %v", err)
	}

	entries := readZip(t, buf.Bytes())
	prefix := strings.TrimSuffix(BundleName(stamp), ".zip") + "/"
	body, ok := entries[prefix+"logs/shellforge.log"]
	if !ok {
		t.Fatalf("logs/shellforge.log missing from %v", keys(entries))
	}
	if !strings.Contains(string(body[:200]), "truncated") {
		t.Errorf("truncated log does not start with a truncation marker: %q", string(body[:200]))
	}
	if len(body) <= MaxLogBytes || len(body) >= len(big) {
		t.Errorf("truncated log length = %d, want roughly MaxLogBytes (%d) plus a short marker", len(body), MaxLogBytes)
	}
}

// TestWriteDropsLogsToStayUnderTheCap asserts logs that would push the whole
// bundle over MaxZipBytes are dropped entirely, with a Note added to
// report.json saying so, rather than a bundle silently exceeding the cap.
func TestWriteDropsLogsToStayUnderTheCap(t *testing.T) {
	stamp := time.Date(2026, 9, 8, 12, 30, 0, 0, time.UTC)
	r := testReport(stamp)

	logDir := t.TempDir()
	// Two logs, each at the per-file cap, comfortably exceed MaxZipBytes
	// together once README, report.json and doctor.txt are added too.
	huge := bytes.Repeat([]byte("y"), MaxLogBytes)
	for _, name := range []string{"a.log", "b.log", "c.log", "d.log", "e.log", "f.log"} {
		if err := os.WriteFile(filepath.Join(logDir, name), huge, 0o600); err != nil {
			t.Fatalf("write fixture log %s: %v", name, err)
		}
	}

	var buf bytes.Buffer
	if err := Write(&buf, r, logDir); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if buf.Len() > MaxZipBytes {
		t.Fatalf("bundle is %d bytes, want under MaxZipBytes (%d)", buf.Len(), MaxZipBytes)
	}

	entries := readZip(t, buf.Bytes())
	prefix := strings.TrimSuffix(BundleName(stamp), ".zip") + "/"
	for name := range entries {
		if strings.HasPrefix(name, prefix+"logs/") {
			t.Errorf("expected logs to be dropped, found %q", name)
		}
	}

	var decoded Report
	if err := json.Unmarshal(entries[prefix+"report.json"], &decoded); err != nil {
		t.Fatalf("report.json does not parse: %v", err)
	}
	if !notesContain(decoded.Notes, "dropped") {
		t.Errorf("Notes = %v, want one saying the logs were dropped", decoded.Notes)
	}
}
