package doctor

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var troubleshootingHeadingLine = regexp.MustCompile(`^#{1,4}\s+(.*)$`)

// troubleshootingHeadings reads every heading in docs/05-troubleshooting.md,
// trimmed, as an exact-match set: "wsl-version-1" must not be satisfied by a
// hypothetical "wsl-version-10" heading.
func troubleshootingHeadings(t *testing.T) map[string]bool {
	t.Helper()
	root := doctorModuleRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "docs", "05-troubleshooting.md"))
	if err != nil {
		t.Fatalf("read docs/05-troubleshooting.md: %v", err)
	}
	set := map[string]bool{}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if m := troubleshootingHeadingLine.FindStringSubmatch(line); m != nil {
			set[strings.TrimSpace(m[1])] = true
		}
	}
	if len(set) == 0 {
		t.Fatal("docs/05-troubleshooting.md has no headings; the parse is wrong")
	}
	return set
}

// doctorModuleRoot walks up from the test's working directory until it
// finds go.mod, matching the same helper cmd/shellforge/docanchor_test.go
// uses.
func doctorModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}

// TestEveryProbeAnchorHasAHeading is AC4's literal requirement.
func TestEveryProbeAnchorHasAHeading(t *testing.T) {
	headings := troubleshootingHeadings(t)
	probes := newProbes(nil, newFakeRunner())
	if len(probes) != 12 {
		t.Fatalf("newProbes returned %d probes, want 12", len(probes))
	}
	for _, p := range probes {
		a, ok := p.(anchored)
		if !ok {
			t.Errorf("%s does not implement anchored", p.ID())
			continue
		}
		anchor := a.anchor()
		if anchor == "" {
			t.Errorf("%s has an empty anchor", p.ID())
			continue
		}
		if !headings[anchor] {
			t.Errorf("%s anchor %q has no heading in docs/05-troubleshooting.md", p.ID(), anchor)
		}
	}
}

// TestEveryResultAnchorHasAHeading is the stronger form: it drives every
// probe's Run for both goos values, including the interrupted path, which
// is where a probe is likeliest to forget to carry its anchor.
func TestEveryResultAnchorHasAHeading(t *testing.T) {
	headings := troubleshootingHeadings(t)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	for _, goos := range []string{"linux", "windows"} {
		for _, ctx := range []context.Context{context.Background(), cancelled} {
			for _, p := range applicableProbes(goos, fakeProber{provisioned: true, running: true}, newFakeRunner()) {
				res := p.Run(ctx)
				if res.DocAnchor == "" {
					t.Errorf("goos=%s: %s result has an empty DocAnchor", goos, res.ID)
					continue
				}
				if !headings[res.DocAnchor] {
					t.Errorf("goos=%s: %s DocAnchor %q has no heading in docs/05-troubleshooting.md", goos, res.ID, res.DocAnchor)
				}
			}
		}
	}
}
