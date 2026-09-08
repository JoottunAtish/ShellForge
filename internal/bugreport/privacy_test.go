package bugreport

import (
	"bytes"
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bundleAllText concatenates every zip entry name and content into one
// string, so a privacy assertion can grep the whole bundle at once.
func bundleAllText(t *testing.T, data []byte) string {
	t.Helper()
	entries := readZip(t, data)
	var b strings.Builder
	for name, content := range entries {
		b.WriteString(name)
		b.WriteByte('\n')
		b.Write(content)
		b.WriteByte('\n')
	}
	return b.String()
}

// secretFixtures seeds five events, one per closed-list rule, each carrying
// a distinct secret substring that must never survive into a bundle, with
// or without --journal.
var secretFixtures = []struct {
	raw    string
	secret string
}{
	{raw: "export PASSWORD=hunter2xyz", secret: "hunter2xyz"},
	{raw: "curl --token=deadbeef00 https://example.com", secret: "deadbeef00"},
	{raw: "mysqldump -pSuperSecretPw db > backup.sql", secret: "SuperSecretPw"},
	{raw: "curl -H 'Authorization: Bearer aaabbbcccddd' https://example.com", secret: "aaabbbcccddd"},
	{raw: "cat id_rsa: -----BEGIN RSA PRIVATE KEY-----\nMIIBSECRETMATERIAL\n-----END RSA PRIVATE KEY-----", secret: "MIIBSECRETMATERIAL"},
}

// TestBundleNeverContainsCommandTextByDefault seeds five secret shapes, runs
// Collect and Write without --journal, and asserts none of the five
// plaintext secrets and none of the raw command text appears anywhere in
// the bundle.
func TestBundleNeverContainsCommandTextByDefault(t *testing.T) {
	s := newTestStore(t)
	for i, f := range secretFixtures {
		seedEvent(t, s, int64(i+1), "nav-01", "/home/learner", f.raw, 0, 5)
	}

	report, err := Collect(context.Background(), Sources{Store: s})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	var buf bytes.Buffer
	if err := Write(&buf, report, ""); err != nil {
		t.Fatalf("Write: %v", err)
	}

	text := bundleAllText(t, buf.Bytes())
	for _, f := range secretFixtures {
		if strings.Contains(text, f.secret) {
			t.Errorf("bundle without --journal leaked secret %q", f.secret)
		}
		if strings.Contains(text, f.raw) {
			t.Errorf("bundle without --journal leaked command text %q", f.raw)
		}
	}
}

// TestBundleRedactsEveryClosedListShapeWithJournal seeds the same five
// shapes, runs Collect and Write with --journal, and asserts none of the
// five secrets survives while journal_redacted equals 5.
func TestBundleRedactsEveryClosedListShapeWithJournal(t *testing.T) {
	s := newTestStore(t)
	for i, f := range secretFixtures {
		seedEvent(t, s, int64(i+1), "nav-01", "/home/learner", f.raw, 0, 5)
	}

	report, err := Collect(context.Background(), Sources{Store: s, IncludeJournal: true})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if report.JournalRedacted != len(secretFixtures) {
		t.Fatalf("JournalRedacted = %d, want %d", report.JournalRedacted, len(secretFixtures))
	}

	var buf bytes.Buffer
	if err := Write(&buf, report, ""); err != nil {
		t.Fatalf("Write: %v", err)
	}

	text := bundleAllText(t, buf.Bytes())
	for _, f := range secretFixtures {
		if strings.Contains(text, f.secret) {
			t.Errorf("bundle with --journal leaked secret %q", f.secret)
		}
	}
}

// TestBundleNeverContainsTheEnvironmentOrTheDatabase asserts the bundle
// never carries an environment snapshot or the progress database, by
// setting a distinctive environment variable this package never reads and
// asserting neither its name nor its value shows up anywhere in the bundle,
// alongside the two literal filenames a snapshot or the database would use.
func TestBundleNeverContainsTheEnvironmentOrTheDatabase(t *testing.T) {
	const envKey = "SHELLFORGE_BUGREPORT_PRIVACY_TEST_MARKER"
	const envValue = "should-never-leave-the-process-env-block-9f8e7d"
	t.Setenv(envKey, envValue)

	s := newTestStore(t)
	seedEvent(t, s, 1, "nav-01", "/home/learner", "echo hi", 0, 5)

	report, err := Collect(context.Background(), Sources{Store: s, IncludeJournal: true})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	var buf bytes.Buffer
	if err := Write(&buf, report, ""); err != nil {
		t.Fatalf("Write: %v", err)
	}

	text := bundleAllText(t, buf.Bytes())
	for _, forbidden := range []string{envKey, envValue, "env.snapshot", "progress.db"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("bundle contains forbidden text %q", forbidden)
		}
	}
}

// TestBundleRewritesTheHostHomePrefix seeds a command whose cwd sits under
// the real host home directory, and a sandbox probe failure whose error
// text also names the home directory, then asserts the bundle contains "~"
// in their place and never the raw home directory path.
func TestBundleRewritesTheHostHomePrefix(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || len(home) <= 1 {
		t.Skip("no usable home directory on this host")
	}

	s := newTestStore(t)
	cwd := filepath.Join(home, "quest")
	seedEvent(t, s, 1, "nav-01", cwd, "pwd", 0, 5)

	prober := fakeProber{err: fmt.Errorf("no sandbox marker at %s/.shellforge", home)}

	report, err := Collect(context.Background(), Sources{Store: s, Prober: prober, IncludeJournal: true})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	var buf bytes.Buffer
	if err := Write(&buf, report, ""); err != nil {
		t.Fatalf("Write: %v", err)
	}

	text := bundleAllText(t, buf.Bytes())
	if strings.Contains(text, home) {
		t.Errorf("bundle contains the raw host home directory %q", home)
	}
	// The scrubbed cwd is asserted on the Report, not by grepping the
	// serialized bundle, and the reason is worth writing down because the
	// obvious version of this assertion is wrong on Windows in a way that
	// looks right on Linux.
	//
	// scrubHome replaces the home prefix and deliberately does not rewrite
	// separators: a bundle that reported a Windows path with forward slashes
	// would misrepresent the machine to whoever reads it, which is the one
	// thing a bug report must not do. So on Windows the scrubbed value is
	// `~\quest`. But report.json goes through json.Marshal, which escapes a
	// backslash, so the bytes in the bundle are `~\\quest` and a
	// strings.Contains for `~\quest` can never match there. Both a hardcoded
	// "~/quest" and a filepath.Join("~", "quest") fail on Windows, for two
	// different reasons.
	//
	// Checking the struct field tests what this package actually promises,
	// and leaves JSON's own escaping to encoding/json where it belongs. The
	// raw-home grep above still covers the whole serialized bundle, which is
	// the half that matters for privacy.
	wantCwd := filepath.Join("~", "quest")
	if len(report.Journal) != 1 {
		t.Fatalf("len(report.Journal) = %d, want 1", len(report.Journal))
	}
	if got := report.Journal[0].Cwd; got != wantCwd {
		t.Errorf("Journal[0].Cwd = %q, want %q", got, wantCwd)
	}
	// The note keeps its literal forward slash: the fake prober's error text
	// hardcodes one above, so scrubbing only replaces the home prefix and
	// this assertion is separator independent.
	if !strings.Contains(text, "~/.shellforge") {
		t.Errorf("bundle does not contain the scrubbed note path ~/.shellforge: %s", text)
	}
}

// bugreportProductionFiles lists this package's own non-test .go files.
func bugreportProductionFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob package directory: %v", err)
	}
	var out []string
	for _, f := range files {
		if !strings.HasSuffix(f, "_test.go") {
			out = append(out, f)
		}
	}
	return out
}

// forbiddenImports is what internal/bugreport must never import: any
// runtime backend or the Runtime interface itself, the sandbox package,
// the game orchestrator, cmd/shellforge, or a network client.
var forbiddenImportPrefixes = []string{
	"github.com/JoottunAtish/ShellForge/internal/runtime",
	"github.com/JoottunAtish/ShellForge/internal/sandbox",
	"github.com/JoottunAtish/ShellForge/internal/game",
	"github.com/JoottunAtish/ShellForge/cmd/shellforge",
}

var forbiddenImportsExact = map[string]bool{
	"net":      true,
	"net/http": true,
	"os/exec":  true,
}

// TestBugReportPackageImportsNoRuntimeOrNetwork is an AST scan over this
// package's own production source, asserting the layer rule directly: no
// import of net, net/http, os/exec, internal/runtime or a backend,
// internal/sandbox, internal/game, or cmd/shellforge.
func TestBugReportPackageImportsNoRuntimeOrNetwork(t *testing.T) {
	fset := token.NewFileSet()
	for _, name := range bugreportProductionFiles(t) {
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if forbiddenImportsExact[path] {
				t.Errorf("%s imports %q, which this package must never import", name, path)
				continue
			}
			for _, prefix := range forbiddenImportPrefixes {
				if path == prefix || strings.HasPrefix(path, prefix+"/") {
					t.Errorf("%s imports %q, which this package must never import", name, path)
				}
			}
		}
	}
}
