package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/bugreport"
	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
)

// runTestBugReport is a small wrapper around runBugReport with the real
// bugreport.Write, for the tests that do not need to force a Write failure.
func runTestBugReport(t *testing.T, w *bytes.Buffer, prober bugreport.Prober, includeJournal bool, outPath string) error {
	t.Helper()
	return runBugReport(context.Background(), w, VersionInfo{Version: "v0.1.0-test"}, prober, includeJournal, outPath, bugreport.Write)
}

// writtenReportSummary is the slice of report.json this test file actually
// reads back: just Notes. doctor.Level has no UnmarshalJSON (a documented
// v0.2 gap in internal/doctor), so decoding into the full bugreport.Report
// fails the moment Doctor.Results is non-empty, which it always is for a
// real doctor.Run(). A destination struct that omits the Doctor field
// entirely sidesteps that: encoding/json ignores JSON object keys with no
// matching destination field.
type writtenReportSummary struct {
	Notes           []string `json:"notes"`
	JournalIncluded bool     `json:"journal_included"`
}

// readWrittenReportSummary opens the zip file at path and returns the
// Notes and JournalIncluded fields of its report.json entry.
func readWrittenReportSummary(t *testing.T, path string) writtenReportSummary {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open %s as a zip: %v", path, err)
	}
	for _, f := range zr.File {
		if !strings.HasSuffix(f.Name, "/report.json") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		defer rc.Close()
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(rc); err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		var summary writtenReportSummary
		if err := json.Unmarshal(buf.Bytes(), &summary); err != nil {
			t.Fatalf("unmarshal %s: %v", f.Name, err)
		}
		return summary
	}
	t.Fatalf("no report.json entry found in %s", path)
	return writtenReportSummary{}
}

func assertRefusal(t *testing.T, err error) *ux.Error {
	t.Helper()
	if err == nil {
		t.Fatal("runBugReport returned nil, want a refusal")
	}
	var uxErr *ux.Error
	if !errors.As(err, &uxErr) {
		t.Fatalf("error = %v (%T), want *ux.Error", err, err)
	}
	if strings.TrimSpace(uxErr.Remediation) == "" {
		t.Errorf("refusal carries no remediation: %+v", uxErr)
	}
	return uxErr
}

// TestBugReportRefusesAnExistingOut asserts an existing regular file at
// --out is refused, and its content is left untouched.
func TestBugReportRefusesAnExistingOut(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "report.zip")
	want := []byte("not a bug report")
	if err := os.WriteFile(out, want, 0o600); err != nil {
		t.Fatalf("seed existing file: %v", err)
	}

	var stdout bytes.Buffer
	err := runTestBugReport(t, &stdout, nil, false, out)
	assertRefusal(t, err)

	got, readErr := os.ReadFile(out)
	if readErr != nil {
		t.Fatalf("read %s after refusal: %v", out, readErr)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("existing file was modified: got %q, want %q", got, want)
	}
}

// TestBugReportRefusesADirectoryAsOut asserts an existing directory at
// --out is refused with its own remediation naming the problem.
func TestBugReportRefusesADirectoryAsOut(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "already-a-dir")
	if err := os.Mkdir(out, 0o700); err != nil {
		t.Fatalf("seed existing directory: %v", err)
	}

	var stdout bytes.Buffer
	err := runTestBugReport(t, &stdout, nil, false, out)
	uxErr := assertRefusal(t, err)
	if !strings.Contains(uxErr.Remediation, "directory") {
		t.Errorf("remediation does not mention the directory problem: %q", uxErr.Remediation)
	}

	if _, statErr := os.Stat(filepath.Join(out, "report.json")); statErr == nil {
		t.Errorf("something was written inside the directory that should have been refused")
	}
}

// TestBugReportRefusesASymlinkAsOut asserts an existing symlink at --out is
// refused: O_EXCL treats the symlink's own existence as the target already
// being there, whether or not it resolves.
func TestBugReportRefusesASymlinkAsOut(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
		t.Fatalf("seed symlink target: %v", err)
	}
	link := filepath.Join(dir, "report.zip")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("seed symlink: %v", err)
	}

	var stdout bytes.Buffer
	err := runTestBugReport(t, &stdout, nil, false, link)
	assertRefusal(t, err)

	got, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("read symlink target after refusal: %v", readErr)
	}
	if string(got) != "original" {
		t.Errorf("symlink target was modified: got %q", got)
	}
}

// TestBugReportRefusesAMissingParentDirectory asserts a --out whose parent
// directory does not exist is refused rather than creating it.
func TestBugReportRefusesAMissingParentDirectory(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "does-not-exist-yet", "report.zip")

	var stdout bytes.Buffer
	err := runTestBugReport(t, &stdout, nil, false, out)
	assertRefusal(t, err)

	if _, statErr := os.Stat(filepath.Dir(out)); statErr == nil {
		t.Errorf("the missing parent directory was created, want it left absent")
	}
}

// TestBugReportRemovesOnlyThePartialBundleItCreated forces Write to fail
// and asserts the bundle path this invocation created with O_EXCL is
// removed, while a sibling file in the same directory is untouched.
func TestBugReportRemovesOnlyThePartialBundleItCreated(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "report.zip")
	sibling := filepath.Join(dir, "sibling.txt")
	siblingContent := []byte("leave me alone")
	if err := os.WriteFile(sibling, siblingContent, 0o600); err != nil {
		t.Fatalf("seed sibling file: %v", err)
	}

	var stdout bytes.Buffer
	failingWrite := func(_ io.Writer, _ bugreport.Report, _ string) error {
		return errors.New("simulated write failure")
	}
	err := runBugReport(context.Background(), &stdout, VersionInfo{}, nil, false, out, failingWrite)
	assertRefusal(t, err)

	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Errorf("bundle path %s still exists after a Write failure, want it removed", out)
	}
	got, readErr := os.ReadFile(sibling)
	if readErr != nil {
		t.Fatalf("read sibling after refusal: %v", readErr)
	}
	if !bytes.Equal(got, siblingContent) {
		t.Errorf("sibling file was modified: got %q, want %q", got, siblingContent)
	}
}

// TestBugReportDefaultNameMatchesTheDocumentedPattern asserts an empty
// --out produces shellforge-bugreport-<YYYYMMDD-HHMMSS>.zip in the current
// directory.
func TestBugReportDefaultNameMatchesTheDocumentedPattern(t *testing.T) {
	t.Chdir(t.TempDir())

	var stdout bytes.Buffer
	if err := runTestBugReport(t, &stdout, nil, false, ""); err != nil {
		t.Fatalf("runBugReport: %v", err)
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read current directory: %v", err)
	}
	pattern := regexp.MustCompile(`^shellforge-bugreport-\d{8}-\d{6}\.zip$`)
	found := false
	for _, e := range entries {
		if pattern.MatchString(e.Name()) {
			found = true
		}
	}
	if !found {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("no file in %v matches %s", names, pattern.String())
	}
}

// TestBugReportPrintsAnAbsolutePathAndSaysNothingWasUploaded asserts the
// success message names the absolute path and says nothing was uploaded.
func TestBugReportPrintsAnAbsolutePathAndSaysNothingWasUploaded(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "report.zip")

	var stdout bytes.Buffer
	if err := runTestBugReport(t, &stdout, nil, false, out); err != nil {
		t.Fatalf("runBugReport: %v", err)
	}

	got := stdout.String()
	if !strings.Contains(got, out) {
		t.Errorf("output does not name the absolute path %q: %q", out, got)
	}
	if !strings.Contains(got, "Nothing was uploaded") {
		t.Errorf("output does not say nothing was uploaded: %q", got)
	}
}

// TestBugReportSucceedsWithNoSandboxAndNoDatabase simulates the exact
// scenario bug-report exists for: no runtime resolved (nil Prober) and no
// progress database reachable (XDG_DATA_HOME pointed at a regular file, so
// store.Open cannot create its directory). It must still succeed and write
// a bundle carrying a Note for each.
func TestBugReportSucceedsWithNoSandboxAndNoDatabase(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed XDG_DATA_HOME blocker: %v", err)
	}
	t.Setenv("XDG_DATA_HOME", blocker)
	// LOCALAPPDATA as well, because platform.DataDir does not read
	// XDG_DATA_HOME on Windows: it goes through os.UserCacheDir, which reads
	// LOCALAPPDATA. Setting only the XDG variable left the real user
	// database reachable on the Windows runner, so the test asserted a note
	// about a missing database while the database was found. Blocking both
	// creates the same condition on both platforms.
	t.Setenv("LOCALAPPDATA", blocker)

	dir := t.TempDir()
	out := filepath.Join(dir, "report.zip")

	var stdout bytes.Buffer
	if err := runTestBugReport(t, &stdout, nil, false, out); err != nil {
		t.Fatalf("runBugReport: %v", err)
	}

	summary := readWrittenReportSummary(t, out)
	if len(summary.Notes) == 0 {
		t.Fatal("Notes is empty, want at least the sandbox and progress notes")
	}
	if !notesContainSubstring(summary.Notes, "no runtime was resolved") {
		t.Errorf("Notes = %v, want one about the missing runtime", summary.Notes)
	}
	if !notesContainSubstring(summary.Notes, "no progress database was found") {
		t.Errorf("Notes = %v, want one about the missing database", summary.Notes)
	}
}

func notesContainSubstring(notes []string, substr string) bool {
	for _, n := range notes {
		if strings.Contains(n, substr) {
			return true
		}
	}
	return false
}
