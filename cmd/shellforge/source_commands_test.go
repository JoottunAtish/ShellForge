package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestEveryCommandInASourceStringResolves is TestEveryCommandInTheDocsResolves
// pointed at the other place this project tells somebody what to type: the
// remediation line of a ux.Fail.
//
// The documentation gate landed with #171 and found five wrong commands. It
// scans Markdown, so it could not see that
// internal/content/setup.remediationLevelStateCorrupted said
// "Run `shellforge reset --hard`", which is the same defect the gate had just
// finished removing from docs/05-troubleshooting.md, in the same words, and
// doubly wrong besides: `reset` takes --yes rather than --hard, and typed on
// the host it refuses outright because it is an in-level command.
//
// A remediation is the worst place in the product for this. The reader is
// already stuck, it is the one line telling them how to get unstuck, and a
// command that does not exist spends the little patience they have left.
//
// Only code counts, the same rule and the same scanner the documentation gate
// uses, and only string literals are read. Comments are invisible to it on
// purpose: render_pass.go's own docstring quotes `shellforge next` while
// explaining that a learner tried it and got command-not-found, and a gate
// that cannot tell a quotation from an instruction would force that sentence
// to be deleted to stay green.
func TestEveryCommandInASourceStringResolves(t *testing.T) {
	root := cliModuleRoot(t)
	invocations := scanSourceInvocations(t, root)

	// Zero means the scanner broke, not that the tree is clean. There are
	// dozens of these strings and several are asserted on by name elsewhere.
	if len(invocations) == 0 {
		t.Fatal("no shellforge invocations found in any string literal; scanSourceInvocations is looking in the wrong place")
	}

	cmd := NewRootCommand(VersionInfo{})
	// cobra generates `help` on demand, so it is absent from Commands()
	// until this is called and a string naming it would not resolve. Six
	// remediations name it, which is how this was found.
	cmd.InitDefaultHelpCmd()
	for _, inv := range invocations {
		if err := resolve(cmd, inv); err != nil {
			t.Errorf("%s:%d: `%s`\n  %v", inv.File, inv.Line, inv, err)
		}
	}
}

// scanSourceInvocations returns every `shellforge ...` invocation written
// inside a string literal anywhere in the module.
//
// Test files are skipped. A test may legitimately hold a wrong command as its
// fixture: TestResolveRefusesTheThreeWaysAnInvocationCanBeWrong is built out
// of them, and docs_commands_test.go's own doc comment lists all five of the
// originals by name.
func scanSourceInvocations(t *testing.T, root string) []docInvocation {
	t.Helper()

	var found []docInvocation
	fset := token.NewFileSet()

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Nothing under these is compiled into the binary, and bin/
			// holds a build of it that .gitignore already excludes.
			switch d.Name() {
			case ".git", "bin", "docs", "packs", "images":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)

		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				// A raw string literal is delimited by backticks, so it
				// cannot carry a code span in the first place.
				return true
			}
			line := fset.Position(lit.Pos()).Line
			for _, span := range inlineCodeSpans(value) {
				for _, tokens := range invocationsInFragment(span) {
					found = append(found, docInvocation{File: rel, Line: line, Tokens: tokens})
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return found
}

// TestScanSourceInvocationsReadsStringsAndNotComments pins the two halves of
// the rule the gate above rests on, because both of them fail silently. A
// scanner blind to string literals reports a clean tree by finding nothing,
// and one that reads comments fails on a sentence that was correct.
func TestScanSourceInvocationsReadsStringsAndNotComments(t *testing.T) {
	dir := t.TempDir()
	src := "package p\n" +
		"\n" +
		"// A comment naming `shellforge frobnicate` must not be read.\n" +
		"const a = \"Run `shellforge doctor` and try again.\"\n" +
		"\n" +
		"// Deliberately wrong, and deliberately inside a string.\n" +
		"const b = \"Run `shellforge reset --hard`.\"\n" +
		"\n" +
		"const c = `a raw literal cannot hold a code span`\n"

	if err := os.WriteFile(filepath.Join(dir, "sample.go"), []byte(src), 0o600); err != nil {
		t.Fatalf("write sample: %v", err)
	}

	got := scanSourceInvocations(t, dir)

	var rendered []string
	for _, inv := range got {
		rendered = append(rendered, inv.String())
	}
	joined := strings.Join(rendered, "\n")

	if len(got) != 2 {
		t.Fatalf("found %d invocations, want 2 (doctor and reset):\n%s", len(got), joined)
	}
	if !strings.Contains(joined, "shellforge doctor") {
		t.Errorf("the string literal was not read: %s", joined)
	}
	if !strings.Contains(joined, "shellforge reset --hard") {
		t.Errorf("the wrong command inside a string was not read: %s", joined)
	}
	if strings.Contains(joined, "frobnicate") {
		t.Errorf("a command named in a comment was read as an instruction: %s", joined)
	}
}

// TestScanSourceInvocationsSkipsTestFiles pins the exemption. Without it this
// gate fails on the fixtures the documentation gate is built out of, and the
// only way to make it pass would be to weaken those.
func TestScanSourceInvocationsSkipsTestFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "real.go"),
		[]byte("package p\n\nconst a = \"Run `shellforge doctor`.\"\n"), 0o600); err != nil {
		t.Fatalf("write real: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fixture_test.go"),
		[]byte("package p\n\nconst b = \"Run `shellforge frobnicate`.\"\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	for _, inv := range scanSourceInvocations(t, dir) {
		if strings.Contains(inv.String(), "frobnicate") {
			t.Fatalf("a _test.go fixture was scanned: %s", inv)
		}
	}
}
