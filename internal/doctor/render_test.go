package doctor

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func threeRowReport() Report {
	return Report{
		Platform: "linux",
		Version:  "0.1.0-test",
		Results: []Result{
			{ID: "disk_free", Status: OK, Detail: "12.0 GiB free", Remediation: "No action needed.", DocAnchor: "disk-space-low"},
			{ID: "windows_terminal", Status: Warn, Detail: "not running under Windows Terminal", Remediation: "Install Windows Terminal.", DocAnchor: "terminal-not-windows-terminal"},
			{ID: "wsl_installed", Status: Fail, Detail: "wsl.exe was not found", Remediation: "Run `wsl --install --no-distribution`.", DocAnchor: "wsl-not-installed"},
		},
	}
}

// TestRenderTablePrintsEveryRowWithAMarker is part of AC1.
func TestRenderTablePrintsEveryRowWithAMarker(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var buf bytes.Buffer
	if err := RenderTable(&buf, threeRowReport()); err != nil {
		t.Fatalf("RenderTable: %v", err)
	}
	out := buf.String()
	if out == "" {
		t.Fatal("table output is empty, want 3 rows")
	}

	wantMarkers := []struct{ marker, id string }{
		{"ok", "disk_free"},
		{"warn", "windows_terminal"},
		{"fail", "wsl_installed"},
	}
	for _, w := range wantMarkers {
		found := false
		for _, line := range strings.Split(out, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, w.marker) && strings.Contains(trimmed, w.id) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no row begins with marker %q and names id %q\ngot:\n%s", w.marker, w.id, out)
		}
	}
}

// TestRenderTablePrintsAFixAndADocsLinkForEveryNonOKRow is part of AC1.
func TestRenderTablePrintsAFixAndADocsLinkForEveryNonOKRow(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var buf bytes.Buffer
	if err := RenderTable(&buf, threeRowReport()); err != nil {
		t.Fatalf("RenderTable: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "Fix: Install Windows Terminal.") {
		t.Errorf("missing Fix line for the warn row:\n%s", out)
	}
	if !strings.Contains(out, "Fix: Run `wsl --install --no-distribution`.") {
		t.Errorf("missing Fix line for the fail row:\n%s", out)
	}
	if !strings.Contains(out, "terminal-not-windows-terminal") {
		t.Errorf("missing docs link for the warn row:\n%s", out)
	}
	if !strings.Contains(out, "wsl-not-installed") {
		t.Errorf("missing docs link for the fail row:\n%s", out)
	}
	if strings.Contains(out, "Fix: No action needed.") {
		t.Error("the ok row printed a Fix line, want none")
	}
	if strings.Contains(out, "disk-space-low") {
		t.Error("the ok row printed a docs link, want none")
	}
}

// TestRenderJSONCarriesEveryRequiredFieldOnEveryRow is AC2's mechanical half.
func TestRenderJSONCarriesEveryRequiredFieldOnEveryRow(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderJSON(&buf, threeRowReport()); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}

	var doc struct {
		OK       bool   `json:"ok"`
		Platform string `json:"platform"`
		Version  string `json:"version"`
		Results  []struct {
			ID          string `json:"id"`
			Status      string `json:"status"`
			Detail      string `json:"detail"`
			Remediation string `json:"remediation"`
			DocAnchor   string `json:"doc_anchor"`
		} `json:"results"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("json.Unmarshal: %v\noutput:\n%s", err, buf.String())
	}
	if doc.OK {
		t.Error("ok = true for a report with a failing row")
	}
	if len(doc.Results) != 3 {
		t.Fatalf("got %d results, want 3", len(doc.Results))
	}
	for _, r := range doc.Results {
		if r.ID == "" || r.Status == "" || r.Detail == "" || r.Remediation == "" || r.DocAnchor == "" {
			t.Errorf("result %+v has an empty required field", r)
		}
	}
}

func TestRenderJSONResultsIsNeverNull(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderJSON(&buf, Report{Platform: "linux", Version: "dev"}); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	if strings.Contains(buf.String(), `"results":null`) {
		t.Errorf("results encoded as null for an empty report:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), `"results":[]`) && !strings.Contains(buf.String(), `"results": []`) {
		t.Errorf("results is not an empty array for an empty report:\n%s", buf.String())
	}
}

func TestRenderJSONOmitsFixedAndRefusedWhenNotFixing(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderJSON(&buf, threeRowReport()); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	if !json.Valid(buf.Bytes()) {
		t.Fatalf("RenderJSON did not write valid JSON: %q", buf.String())
	}
	if strings.Contains(buf.String(), `"fixed"`) || strings.Contains(buf.String(), `"refused"`) {
		t.Errorf("fixed/refused present without --fix, want them omitted:\n%s", buf.String())
	}
}

func TestRenderJSONWithFixOutcomeCarriesFixedAndRefused(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderJSONWithFixOutcome(&buf, threeRowReport(), []string{"disk_free: created the data directory"}, []string{"wsl_installed: run wsl --install"}); err != nil {
		t.Fatalf("RenderJSONWithFixOutcome: %v", err)
	}
	var doc struct {
		Fixed   []string `json:"fixed"`
		Refused []string `json:"refused"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(doc.Fixed) != 1 || len(doc.Refused) != 1 {
		t.Errorf("doc = %+v, want one fixed and one refused entry", doc)
	}
}

// TestRenderTableAlignsTheIDColumnToTheWidestID.
func TestRenderTableAlignsTheIDColumnToTheWidestID(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	report := Report{
		Platform: "linux",
		Version:  "dev",
		Results: []Result{
			{ID: "os", Status: OK, Detail: "short id detail", Remediation: "No action needed.", DocAnchor: "os-too-old"},
			{ID: "a_much_longer_probe_identifier", Status: OK, Detail: "long id detail", Remediation: "No action needed.", DocAnchor: "os-too-old"},
		},
	}
	var buf bytes.Buffer
	if err := RenderTable(&buf, report); err != nil {
		t.Fatalf("RenderTable: %v", err)
	}

	var detailOffsets []int
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, "short id detail") {
			detailOffsets = append(detailOffsets, strings.Index(line, "short id detail"))
		}
		if strings.Contains(line, "long id detail") {
			detailOffsets = append(detailOffsets, strings.Index(line, "long id detail"))
		}
	}
	if len(detailOffsets) != 2 {
		t.Fatalf("found %d detail lines, want 2", len(detailOffsets))
	}
	if detailOffsets[0] != detailOffsets[1] {
		t.Errorf("detail columns start at offsets %v, want them equal", detailOffsets)
	}
}

// TestRenderTableGolden is the byte-for-byte check. ux.ColorEnabled returns
// false for a bytes.Buffer because it is not a character device, so this
// golden needs no environment setup; NO_COLOR is set anyway to say so out
// loud.
func TestRenderTableGolden(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	report := Report{
		Platform: "linux",
		Version:  "0.1.0-golden",
		Results: []Result{
			{ID: "disk_free", Status: OK, Detail: "12.0 GiB free at /home/user/.local/share/shellforge", Remediation: "No action needed.", DocAnchor: "disk-space-low"},
			{ID: "windows_terminal", Status: Warn, Detail: "not running under Windows Terminal", Remediation: "Install Windows Terminal from the Microsoft Store.", DocAnchor: "terminal-not-windows-terminal"},
			{ID: "wsl_installed", Status: Fail, Detail: "wsl.exe was not found", Remediation: "Run `wsl --install --no-distribution` in an Administrator PowerShell, then reboot.", DocAnchor: "wsl-not-installed"},
		},
	}
	var buf bytes.Buffer
	if err := RenderTable(&buf, report); err != nil {
		t.Fatalf("RenderTable: %v", err)
	}
	if buf.String() != wantGoldenTable {
		t.Errorf("RenderTable golden mismatch.\ngot:\n%s\nwant:\n%s", buf.String(), wantGoldenTable)
	}
}

const wantGoldenTable = `shellforge doctor: linux, 0.1.0-golden

ok     disk_free          12.0 GiB free at /home/user/.local/share/shellforge
warn   windows_terminal   not running under Windows Terminal
    Fix: Install Windows Terminal from the Microsoft Store.
    More detail: https://github.com/JoottunAtish/ShellForge/blob/main/docs/05-troubleshooting.md#terminal-not-windows-terminal
fail   wsl_installed      wsl.exe was not found
    Fix: Run ` + "`wsl --install --no-distribution`" + ` in an Administrator PowerShell, then reboot.
    More detail: https://github.com/JoottunAtish/ShellForge/blob/main/docs/05-troubleshooting.md#wsl-not-installed

1 ok, 1 warn, 1 fail
`
