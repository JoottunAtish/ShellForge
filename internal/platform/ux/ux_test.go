package ux

import (
	"bytes"
	"errors"
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
