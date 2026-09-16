package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The documentation command gate.
//
// docs/design/DOCUMENTATION-PLAN.md lists "every command shown in the docs is
// copy-pasteable and correct, run them" as a Day 7 checklist item. A checklist
// item is a person remembering. Five invocations got through anyway, in a
// repository that already gates punctuation, layer dependencies, link targets
// and the existence of this very package:
//
//	shellforge uninstall             docs/06-uninstall.md, never a registered verb
//	shellforge export --format=json  docs/06-uninstall.md, never a registered verb
//	shellforge --ascii               docs/05-troubleshooting.md, --ascii is on map
//	shellforge reset --hard          docs/05-troubleshooting.md, reset takes --yes
//	shellforge author scaffold       four documents, resolves to a stub
//
// So this is the checklist item as a gate. It reads the user-facing
// documentation, pulls out every `shellforge ...` invocation, and resolves each
// one against the real cobra tree that main builds.
//
// What it proves: the verb exists, its flags are flags that command accepts, and
// it is not a stub. What it does not prove: that the command's output is what
// the surrounding prose claims. That half still needs a person at a terminal,
// and docs/design/DOCUMENTATION-PLAN.md still asks for it.

// docSet is the documentation this gate holds to the standard, relative to the
// module root.
//
// docs/design/** is deliberately absent. The design record is a statement of
// intent written before the code, it names verbs that were planned and cut, and
// docs/design/README.md already says that when it disagrees with the code the
// code is right. Holding it to this standard would mean editing the record of
// what was intended to match what was built, which destroys the only thing it
// is for.
//
// PROGRESS.md is absent for the same kind of reason: it is a historical log, and
// a line written in August that correctly said `author scaffold` was a stub is
// not a defect to be fixed in September.
var docSet = []string{
	"README.md",
	"CONTRIBUTING.md",
	"docs/README.md",
	"docs/01-install-windows.md",
	"docs/02-install-linux.md",
	"docs/03-quickstart.md",
	"docs/04-how-it-works.md",
	"docs/05-troubleshooting.md",
	"docs/06-uninstall.md",
	"docs/07-authoring-levels.md",
	"docs/CURRICULUM.md",
	"docs/LEVEL-FORMAT.md",
}

// docInvocation is one `shellforge ...` string found in a document.
type docInvocation struct {
	File   string
	Line   int
	Tokens []string // everything after the word shellforge, in order
}

// String renders the invocation the way it appeared, for a failure message.
func (inv docInvocation) String() string {
	if len(inv.Tokens) == 0 {
		return "shellforge"
	}
	return "shellforge " + strings.Join(inv.Tokens, " ")
}

// TestEveryCommandInTheDocsResolves is the gate itself.
func TestEveryCommandInTheDocsResolves(t *testing.T) {
	root := cliModuleRoot(t)

	var invocations []docInvocation
	for _, rel := range docSet {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		invocations = append(invocations, scanInvocations(rel, body)...)
	}

	// A documentation set that yields nothing means the scanner is broken, not
	// that the documentation is clean. Every one of these files mentions the
	// binary by name.
	if len(invocations) == 0 {
		t.Fatal("no shellforge invocations found in the documentation; scanInvocations is looking in the wrong place")
	}

	cmd := NewRootCommand(VersionInfo{})
	for _, inv := range invocations {
		if err := resolve(cmd, inv); err != nil {
			t.Errorf("%s:%d: `%s`\n  %v", inv.File, inv.Line, inv, err)
		}
	}
}

// scanInvocations pulls every `shellforge ...` invocation out of one document.
//
// Only code counts: a fenced block, or an inline span between backticks.
// Everything this project's style guide calls copy-pasteable is formatted as
// code, and reading prose as if it were a command is how
// "`shellforge author scaffold` generates the skeleton for you" turns into an
// invocation with the words "generates the skeleton for you" as its arguments.
// The trade is that a command written in bare prose goes unchecked, which is
// the right way round: this gate under-reports rather than inventing a failure
// its reader cannot act on.
//
// It is also the escape hatch, and the only one, deliberately. A page
// sometimes has to say that something does not exist, as docs/06-uninstall.md
// does of the export verb. Code formatting in these documents means "you can
// paste this", so a verb that does not exist does not get it. An allowlist
// would have been the other answer and a worse one: the entry outlives the
// reason for it, and the next reader cannot tell an accepted absence from a
// forgotten one.
func scanInvocations(file string, body []byte) []docInvocation {
	var found []docInvocation
	inFence := false

	for i, line := range strings.Split(string(body), "\n") {
		if isFenceDelimiter(line) {
			inFence = !inFence
			continue
		}

		fragments := []string{line}
		if !inFence {
			fragments = inlineCodeSpans(line)
		}
		for _, fragment := range fragments {
			for _, tokens := range invocationsInFragment(fragment) {
				found = append(found, docInvocation{File: file, Line: i + 1, Tokens: tokens})
			}
		}
	}
	return found
}

// isFenceDelimiter reports whether a line opens or closes a fenced code block.
func isFenceDelimiter(line string) bool {
	trimmed := strings.TrimLeft(line, " \t>")
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}

// inlineCodeSpans returns the contents of each backtick-delimited span on a
// line. Splitting on the backtick puts the spans at the odd indices, which is
// exact for the single-backtick spans this repository uses throughout.
func inlineCodeSpans(line string) []string {
	parts := strings.Split(line, "`")
	var spans []string
	for i := 1; i < len(parts); i += 2 {
		spans = append(spans, parts[i])
	}
	return spans
}

const programName = "shellforge"

// invocationsInFragment returns the token list of every invocation in one
// fragment of code.
func invocationsInFragment(fragment string) [][]string {
	var found [][]string
	for offset := 0; ; {
		rest := fragment[offset:]
		at := strings.Index(rest, programName)
		if at < 0 {
			return found
		}
		start := offset + at
		end := start + len(programName)
		offset = end

		if !isWholeWord(fragment, start, end) {
			continue
		}
		found = append(found, commandTokens(fragment[end:]))
	}
}

// isWholeWord reports whether fragment[start:end] stands alone as the program
// name.
//
// The character before must not continue an identifier or a path, which rules
// out `/opt/shellforge` and `my-shellforge`. The character after must not
// continue one either, which rules out `shellforge-sandbox`, `shellforge.exe`
// and `shellforge_v0.1.0_linux_amd64.tar.gz`.
func isWholeWord(fragment string, start, end int) bool {
	const joiners = "/-_."
	if start > 0 {
		prev := fragment[start-1]
		if isWordByte(prev) || strings.IndexByte(joiners, prev) >= 0 {
			return false
		}
	}
	if end < len(fragment) {
		next := fragment[end]
		if isWordByte(next) || strings.IndexByte(joiners, next) >= 0 {
			return false
		}
	}
	return true
}

func isWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

// commandTokens takes the words that follow the program name and stops at the
// first one that is not part of the command being described.
//
// Even inside code, an invocation eventually stops: a `#` comment explaining
// it, a `<placeholder>` standing in for an argument, a pipe or a redirect, or a
// path such as the `./cmd/shellforge` in `go build -o bin/shellforge
// ./cmd/shellforge`. Stopping is always safe, for the same reason
// scanInvocations only reads code.
func commandTokens(rest string) []string {
	var tokens []string
	for _, token := range strings.Fields(rest) {
		token = strings.Trim(token, `"',;.:()[]{}`)
		if token == "" || !isCommandToken(token) {
			return tokens
		}
		tokens = append(tokens, token)
	}
	return tokens
}

// isCommandToken reports whether a token is still part of the invocation. A
// flag has to look like one: `--json.` is a sentence ending in a flag, not a
// flag named "json.".
func isCommandToken(token string) bool {
	body := token
	if strings.HasPrefix(body, "--") {
		body = body[2:]
	} else if strings.HasPrefix(body, "-") {
		body = body[1:]
	}
	if body == "" {
		return false
	}
	// Anything after the first "=" is a flag's value and is not checked.
	if at := strings.IndexByte(body, '='); at >= 0 {
		body = body[:at]
	}
	for i := 0; i < len(body); i++ {
		b := body[i]
		if !isWordByte(b) && b != '-' {
			return false
		}
	}
	return true
}

// resolve walks an invocation's tokens down the command tree and reports the
// first thing wrong with it.
//
// The first non-flag token must name a top-level command. After that each token
// descends if it names a subcommand of the command reached so far, and anything
// else ends the walk and is treated as an argument: `nav-01` in
// `shellforge run nav-01` is an argument, not a missing subcommand.
func resolve(root *cobra.Command, inv docInvocation) error {
	cmd := root
	descending := true

	for i, token := range inv.Tokens {
		if strings.HasPrefix(token, "-") {
			if err := checkFlag(cmd, token); err != nil {
				return err
			}
			continue
		}
		if !descending {
			continue
		}
		child := subcommand(cmd, token)
		if child == nil {
			if cmd == root {
				return fmt.Errorf("`%s` is not a shellforge command. Registered verbs: %s",
					token, strings.Join(verbNames(root), ", "))
			}
			// A token that is not a subcommand is an argument, and so is
			// everything after it. Flags keep being checked against the
			// command reached, which is where cobra would parse them.
			descending = false
			continue
		}
		cmd = child
		_ = i
	}

	if cmd.Annotations[stubAnnotation] == "true" {
		return fmt.Errorf("`%s` is registered but is a stub, so a reader who runs it gets a refusal. "+
			"Documentation for people using Shellforge must not name it", cmd.CommandPath())
	}
	return nil
}

// subcommand returns the named child of cmd, by name or alias.
func subcommand(cmd *cobra.Command, name string) *cobra.Command {
	for _, child := range cmd.Commands() {
		if child.Name() == name {
			return child
		}
		for _, alias := range child.Aliases {
			if alias == name {
				return child
			}
		}
	}
	return nil
}

// checkFlag reports whether cmd accepts the flag as written.
//
// Two rules, because the verbs a learner types inside a level (play, run, check,
// hint, reset) all set DisableFlagParsing and so register none of the flags they
// advertise. For those the usage line is the declaration: `Use: "reset [--yes]"`
// accepts --yes and refuses --hard. For everything else cobra's own flag sets
// are the declaration.
func checkFlag(cmd *cobra.Command, token string) error {
	name := strings.TrimLeft(strings.SplitN(token, "=", 2)[0], "-")
	if name == "" {
		return nil // a bare "--" ends flag parsing and is not a flag
	}

	if cmd.DisableFlagParsing {
		if strings.Contains(cmd.Use, "--"+name) {
			return nil
		}
		return fmt.Errorf("`%s` does not take %s. Its usage line is `%s`", cmd.CommandPath(), token, cmd.Use)
	}

	if cmd.Flags().Lookup(name) != nil || cmd.InheritedFlags().Lookup(name) != nil {
		return nil
	}
	if len(name) == 1 {
		if cmd.Flags().ShorthandLookup(name) != nil || cmd.InheritedFlags().ShorthandLookup(name) != nil {
			return nil
		}
	}
	return fmt.Errorf("`%s` does not take %s", cmd.CommandPath(), token)
}

// verbNames lists the registered top-level verbs, for a failure message that
// tells the writer what they could have meant.
func verbNames(root *cobra.Command) []string {
	names := make([]string, 0, len(root.Commands()))
	for _, child := range root.Commands() {
		names = append(names, child.Name())
	}
	return names
}

// The gate's own tests.
//
// TestEveryCommandInTheDocsResolves reads the real documentation, so on a clean
// tree it passes by finding nothing wrong, which is exactly the shape of test
// that stops failing when the thing underneath it breaks. Everything below
// tests the gate against inputs it controls.

// TestScanInvocationsReadsOnlyCode covers the extractor, including every string
// in the tree today that looks like an invocation and is not one.
func TestScanInvocationsReadsOnlyCode(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want []string
	}{
		{
			name: "an inline span",
			doc:  "Run `shellforge doctor` first.",
			want: []string{"shellforge doctor"},
		},
		{
			name: "two spans on one line",
			doc:  "Either `shellforge play` or `shellforge run nav-01`.",
			want: []string{"shellforge play", "shellforge run nav-01"},
		},
		{
			name: "a fenced block",
			doc:  "```bash\nshellforge init\nshellforge play\n```\n",
			want: []string{"shellforge init", "shellforge play"},
		},
		{
			name: "prose after a span is not an argument",
			doc:  "`shellforge author validate` checks a pack against the format",
			want: []string{"shellforge author validate"},
		},
		{
			name: "prose outside any span is not read at all",
			doc:  "You could run shellforge uninstall here if it existed.",
			want: nil,
		},
		{
			name: "a trailing comment ends the invocation",
			doc:  "```\nshellforge init      # sets up the sandbox\n```\n",
			want: []string{"shellforge init"},
		},
		{
			name: "a placeholder ends the invocation",
			doc:  "`shellforge run <level-id>`",
			want: []string{"shellforge run"},
		},
		{
			name: "a pipe ends the invocation",
			doc:  "```\nshellforge doctor --json | jq .\n```\n",
			want: []string{"shellforge doctor --json"},
		},
		{
			name: "a sentence ending in a flag is not a flag named json dot",
			doc:  "Attach the output of `shellforge doctor --json`.",
			want: []string{"shellforge doctor --json"},
		},
		{
			name: "the sandbox name is not an invocation",
			doc:  "`shellforge-sandbox` must not be listed.",
			want: nil,
		},
		{
			name: "the windows binary is not an invocation",
			doc:  "```\nRemove-Item shellforge.exe\n```\n",
			want: nil,
		},
		{
			name: "an in-sandbox path is not an invocation",
			doc:  "```\n/opt/shellforge/bin/_sf-request brief\n```\n",
			want: nil,
		},
		{
			name: "an install path is not an invocation",
			doc:  "```\nrm -f \"$HOME/.local/bin/shellforge\"\n```\n",
			want: nil,
		},
		{
			name: "a release asset is not an invocation",
			doc:  "`shellforge_v0.1.0_linux_amd64.tar.gz`",
			want: nil,
		},
		{
			name: "the package path in a go build line is not an invocation",
			doc:  "```\ngo build -o bin/shellforge ./cmd/shellforge\n```\n",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, inv := range scanInvocations("test.md", []byte(tt.doc)) {
				got = append(got, inv.String())
			}
			if strings.Join(got, " | ") != strings.Join(tt.want, " | ") {
				t.Errorf("scanInvocations = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestResolveRefusesTheThreeWaysAnInvocationCanBeWrong builds a command tree
// this test owns, so the three failure kinds are exercised whatever the real
// CLI grows into.
func TestResolveRefusesTheThreeWaysAnInvocationCanBeWrong(t *testing.T) {
	newTree := func() *cobra.Command {
		root := &cobra.Command{Use: "shellforge"}

		real := &cobra.Command{Use: "doctor"}
		real.Flags().Bool("json", false, "")

		group := &cobra.Command{Use: "sandbox"}
		group.AddCommand(&cobra.Command{Use: "destroy"})

		unparsed := &cobra.Command{Use: "reset [--yes]", DisableFlagParsing: true}

		stub := &cobra.Command{
			Use:         "scaffold <level-id>",
			Annotations: map[string]string{stubAnnotation: "true"},
		}

		root.AddCommand(real, group, unparsed, stub)
		return root
	}

	tests := []struct {
		name    string
		tokens  []string
		wantErr string
	}{
		{name: "a registered verb", tokens: []string{"doctor"}},
		{name: "a registered flag", tokens: []string{"doctor", "--json"}},
		{name: "a subcommand", tokens: []string{"sandbox", "destroy"}},
		{name: "an argument is not a missing subcommand", tokens: []string{"doctor", "nav-01"}},
		{name: "a flag advertised in the usage line", tokens: []string{"reset", "--yes"}},
		{
			name:    "an unknown verb",
			tokens:  []string{"uninstall"},
			wantErr: "is not a shellforge command",
		},
		{
			name:    "a flag the command does not take",
			tokens:  []string{"doctor", "--ascii"},
			wantErr: "does not take --ascii",
		},
		{
			name:    "a flag not in the usage line of an unparsed command",
			tokens:  []string{"reset", "--hard"},
			wantErr: "does not take --hard",
		},
		{
			name:    "a flag on the root command itself",
			tokens:  []string{"--ascii"},
			wantErr: "does not take --ascii",
		},
		{
			name:    "a stub",
			tokens:  []string{"scaffold"},
			wantErr: "is a stub",
		},
		{
			name:    "a stub with an argument",
			tokens:  []string{"scaffold", "my-level"},
			wantErr: "is a stub",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := resolve(newTree(), docInvocation{File: "test.md", Line: 1, Tokens: tt.tokens})
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("resolve(%q) = %v, want no error", tt.tokens, err)
			case tt.wantErr != "" && err == nil:
				t.Errorf("resolve(%q) = nil, want an error mentioning %q", tt.tokens, tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Errorf("resolve(%q) = %v, want an error mentioning %q", tt.tokens, err, tt.wantErr)
			}
		})
	}
}

// TestTheRealCommandsDocumentedTodayResolve pins the flags the shipped guides
// rely on, both sides of the DisableFlagParsing split, so a refactor that
// registers or unregisters one is caught here rather than by a reader.
func TestTheRealCommandsDocumentedTodayResolve(t *testing.T) {
	root := NewRootCommand(VersionInfo{})

	for _, tokens := range [][]string{
		{"doctor", "--json"},
		{"bug-report", "--journal"},
		{"author", "test", "--all"},
		{"author", "validate"},
		{"map", "--ascii"},
		{"run", "nav-01", "--live-check=off"},
		{"play", "--next"},
		{"hint", "--reveal"},
		{"reset", "--yes"},
		{"sandbox", "destroy", "--yes"},
	} {
		inv := docInvocation{File: "test.md", Line: 1, Tokens: tokens}
		if err := resolve(root, inv); err != nil {
			t.Errorf("`%s` should resolve against the real tree: %v", inv, err)
		}
	}
}
