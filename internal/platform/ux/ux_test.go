package ux

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestErrorWrapsCause(t *testing.T) {
	cause := errors.New("exit status 1")
	err := Fail("provision the sandbox", cause, "run `shellforge doctor`", "docker-daemon-down")

	if got, want := err.Error(), "provision the sandbox: exit status 1"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(err, cause) {
		t.Error("errors.Is could not find the wrapped cause")
	}

	var target *Error
	if !errors.As(err, &target) {
		t.Fatal("errors.As could not recover the *Error")
	}
	if target.DocAnchor != "docker-daemon-down" {
		t.Errorf("DocAnchor = %q, want %q", target.DocAnchor, "docker-daemon-down")
	}
}

func TestErrorWithoutCause(t *testing.T) {
	err := Fail("that level is locked", nil, "run `shellforge map` to see what is available", "")
	if got, want := err.Error(), "that level is locked"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// TestRenderAlwaysOffersANextStep is the machine-checkable half of
// non-negotiable 6 in CLAUDE.md: every user-facing error says what failed, why,
// and the next command to run.
func TestRenderAlwaysOffersANextStep(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		wantContains []string
	}{
		{
			name: "full user facing error",
			err:  Fail("import the WSL distribution", errors.New("access denied"), "run `shellforge doctor`", "wsl-import-blocked"),
			wantContains: []string{
				"import the WSL distribution",
				"access denied",
				"Try this:",
				"shellforge doctor",
				"wsl-import-blocked",
			},
		},
		{
			name:         "bare error still tells the user what to do",
			err:          errors.New("something went sideways"),
			wantContains: []string{"something went sideways", "shellforge bug-report"},
		},
		{
			name:         "no remediation still names the failure",
			err:          Fail("that level is locked", nil, "", ""),
			wantContains: []string{"that level is locked"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			Render(&buf, tt.err)
			got := buf.String()
			for _, want := range tt.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("output missing %q\ngot:\n%s", want, got)
				}
			}
		})
	}
}

// TestDocURLOmitsTheFragmentForAnEmptyAnchor pins DocURL against ux.Render
// drifting: Render must print no "More detail:" line when DocAnchor is
// empty, and this is the same rule stated as a direct assertion on the
// function outside callers need to use.
func TestDocURLOmitsTheFragmentForAnEmptyAnchor(t *testing.T) {
	if got := DocURL(""); got != "" {
		t.Errorf("DocURL(\"\") = %q, want empty", got)
	}
	got := DocURL("wsl-not-installed")
	if !strings.Contains(got, "docs/05-troubleshooting.md#wsl-not-installed") {
		t.Errorf("DocURL(%q) = %q, want it to end in the anchor fragment", "wsl-not-installed", got)
	}
	if strings.Contains(got, "##") {
		t.Errorf("DocURL(%q) = %q, want exactly one '#' before the anchor", "wsl-not-installed", got)
	}
}

func TestRenderNilIsSilent(t *testing.T) {
	var buf bytes.Buffer
	Render(&buf, nil)
	if buf.Len() != 0 {
		t.Errorf("Render(nil) wrote %q, want nothing", buf.String())
	}
}

// TestRenderToNonTerminalHasNoEscapeCodes matters because CI logs, pipes, and
// `--json` consumers all get a non-terminal writer.
func TestRenderToNonTerminalHasNoEscapeCodes(t *testing.T) {
	var buf bytes.Buffer
	Render(&buf, Fail("do the thing", errors.New("nope"), "try again", "anchor"))
	if strings.Contains(buf.String(), "\x1b[") {
		t.Errorf("escape codes leaked into a non-terminal writer:\n%q", buf.String())
	}
}

// TestColorEnabledHonoursEverySuppressionRule covers the three ways a learner
// or their environment says no to colour. Before ColorEnabled was exported
// these were only reachable through Render, so each rule was asserted only by
// the absence of escape codes in one rendered error. A caller outside this
// package now depends on the answer directly, which is worth asserting
// directly.
//
// Every case sets both NO_COLOR and TERM with t.Setenv, so a case never
// inherits the value the previous one wanted, and so the suite behaves the
// same on a developer's coloured terminal as on a CI runner.
func TestColorEnabledHonoursEverySuppressionRule(t *testing.T) {
	tests := []struct {
		name    string
		noColor string
		setNo   bool
		term    string
		want    bool
	}{
		{name: "no-color set to empty still suppresses", noColor: "", setNo: true, term: "xterm-256color", want: false},
		{name: "no-color set to a value suppresses", noColor: "1", setNo: true, term: "xterm-256color", want: false},
		{name: "term dumb suppresses", term: "dumb", want: false},
		{name: "term DUMB suppresses, case insensitively", term: "DUMB", want: false},
		{name: "a plain buffer is not a character device", term: "xterm-256color", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setNo {
				t.Setenv("NO_COLOR", tt.noColor)
			} else {
				os.Unsetenv("NO_COLOR")
			}
			t.Setenv("TERM", tt.term)

			if got := ColorEnabled(&bytes.Buffer{}); got != tt.want {
				t.Errorf("ColorEnabled = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestColorEnabledRefusesADevNullFile pins the character-device rule against a
// real *os.File rather than a buffer, because the buffer cases above pass for
// the wrong reason: they fail the *os.File type assertion before the mode is
// ever consulted. A regular file on disk is a *os.File and is not a character
// device, which is the case a learner produces with `shellforge run > log`.
func TestColorEnabledRefusesARegularFile(t *testing.T) {
	os.Unsetenv("NO_COLOR")
	t.Setenv("TERM", "xterm-256color")

	path := filepath.Join(t.TempDir(), "redirected")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create the redirect target: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })

	if ColorEnabled(f) {
		t.Error("ColorEnabled = true for a regular file, want false: a redirect must not receive escape codes")
	}
}
