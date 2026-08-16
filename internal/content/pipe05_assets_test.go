package content

import (
	"bytes"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/JoottunAtish/ShellForge/packs"
)

// pipe-05 is the first level in the pack whose world comes from committed asset
// files rather than from inline content or a generator, and its checks assert
// two exact answers about those bytes: a total of 147 lines matching ERROR, and
// the distinct codes E401, E500 and E503.
//
// Nothing else connects the assets to the answers. Editing a log file, or
// regenerating it with a different seed, would leave a level that cannot be
// passed, and the only way to find out would be to play it against a real
// container. That is exactly the failure the level authoring rules exist to
// prevent: "if the level says the answer is 147, the log file here must actually
// contain 147 matching lines. Verify by running the level's solution, not by
// adjusting the check."
//
// These tests are that verification, done the way the level's own solution does
// it, against the committed bytes, with no Docker. They run on the Windows matrix
// leg too, which the golden test cannot.

// pipe05Assets are the three logs, read straight out of the embedded pack.
func pipe05Assets(t *testing.T) map[string][]byte {
	t.Helper()

	names := []string{"assets/app-1.log", "assets/app-2.log", "assets/billing.log"}
	out := make(map[string][]byte, len(names))
	for _, name := range names {
		data, err := packs.FS.ReadFile(packs.CoreLinuxBasics + "/" + name)
		if err != nil {
			t.Fatalf("read %s from the embedded pack: %v", name, err)
		}
		if len(data) == 0 {
			t.Fatalf("%s is empty", name)
		}
		out[name] = data
	}
	return out
}

// pipe05Level returns the level, failing the test if it is missing.
func pipe05Level(t *testing.T) *Level {
	t.Helper()

	pack, err := Embedded()
	if err != nil {
		t.Fatalf("load the embedded pack: %v", err)
	}
	lvl, ok := pack.Level("pipe-05")
	if !ok {
		t.Fatal("pipe-05 is not in the embedded pack")
	}
	return lvl
}

// checkValue returns the `value` param of the named check.
func checkValue(t *testing.T, lvl *Level, id string) string {
	t.Helper()

	for _, c := range lvl.Checks {
		if c.ID != id {
			continue
		}
		raw, ok := c.Params["value"]
		if !ok {
			t.Fatalf("check %q has no value param", id)
		}
		s, ok := raw.(string)
		if !ok {
			t.Fatalf("check %q value param is %T, want a string", id, raw)
		}
		return s
	}
	t.Fatalf("pipe-05 has no check with id %q", id)
	return ""
}

// TestPipe05ErrorCountMatchesTheCommittedAssets counts the way the level's own
// solution counts, `grep -ri ERROR`, and asserts the check agrees.
func TestPipe05ErrorCountMatchesTheCommittedAssets(t *testing.T) {
	assets := pipe05Assets(t)

	// grep -i is a case-insensitive substring match on each line, so that is
	// what this does. Counting matching LINES rather than occurrences is what
	// `grep | wc -l` produces, and a line with two matches still counts once.
	matcher := regexp.MustCompile(`(?i)error`)

	total := 0
	for name, data := range assets {
		perFile := 0
		for _, line := range bytes.Split(bytes.TrimRight(data, "\n"), []byte{'\n'}) {
			if matcher.Match(line) {
				perFile++
			}
		}
		if perFile == 0 {
			t.Errorf("%s contains no ERROR lines at all, which cannot be right for this level", name)
		}
		total += perFile
	}

	want := checkValue(t, pipe05Level(t), "obj1")
	if got := itoa(total); got != want {
		t.Errorf("the committed assets contain %s ERROR lines, but pipe-05's obj1 check asserts %q.\n"+
			"Do NOT change the check to match the assets. Either the assets were edited or regenerated, "+
			"in which case restore them, or the answer genuinely changed, in which case the level's "+
			"briefing and hints need rereading too.", got, want)
	}
}

// TestPipe05ErrorCodesMatchTheCommittedAssets extracts codes the way the
// solution does, `grep -rhoE 'E[0-9]{3}' | sort -u`, and asserts obj2 agrees.
//
// This is the assertion that catches an asset edit introducing a fourth code. A
// stray E404 would make obj2 unsolvable, because the learner's own sort -u would
// list four codes where the check expects three, and the level would be
// unwinnable with no indication why.
func TestPipe05ErrorCodesMatchTheCommittedAssets(t *testing.T) {
	assets := pipe05Assets(t)
	pattern := regexp.MustCompile(`E[0-9]{3}`)

	seen := map[string]bool{}
	for _, data := range assets {
		for _, match := range pattern.FindAll(data, -1) {
			seen[string(match)] = true
		}
	}

	codes := make([]string, 0, len(seen))
	for code := range seen {
		codes = append(codes, code)
	}
	sort.Strings(codes)

	got := strings.Join(codes, "\n")
	want := checkValue(t, pipe05Level(t), "obj2")

	if got != want {
		t.Errorf("the committed assets contain the distinct codes %q, but pipe-05's obj2 check asserts %q.\n"+
			"Do NOT change the check to match the assets. A code appearing in a log and not in the check, "+
			"or the other way round, makes this objective impossible to satisfy.",
			strings.ReplaceAll(got, "\n", ","), strings.ReplaceAll(want, "\n", ","))
	}
}

// TestPipe05AssetsHaveNoStrayErrorSpellings guards the trap that makes the count
// wrong in the most confusing possible way.
//
// The solution greps case-insensitively, so any line containing "error" in any
// case counts, whatever its severity field says. A noise line reading
// "connection errors retried" would add to the total while looking like ordinary
// log chatter, and the level's answer would be quietly wrong. Every matching line
// must be an actual ERROR record.
func TestPipe05AssetsHaveNoStrayErrorSpellings(t *testing.T) {
	assets := pipe05Assets(t)
	matcher := regexp.MustCompile(`(?i)error`)

	for name, data := range assets {
		for i, line := range bytes.Split(bytes.TrimRight(data, "\n"), []byte{'\n'}) {
			if !matcher.Match(line) {
				continue
			}
			// The generator's format is: timestamp service LEVEL message. A
			// line that matches case-insensitively must carry the literal
			// severity ERROR, not the word buried in prose.
			if !bytes.Contains(line, []byte(" ERROR ")) {
				t.Errorf("%s line %d matches a case-insensitive search for \"error\" but is not an ERROR record, "+
					"so it inflates the level's answer while looking like ordinary log noise:\n%s",
					name, i+1, line)
			}
		}
	}
}

// TestPipe05AssetsAreLFOnly holds the rule .gitattributes exists for.
//
// A CRLF log file would change every line's bytes, so a hash-based check would
// break, and more subtly it would put a carriage return inside the count the
// learner writes into report.txt. The setup runner strips CR when it materializes
// a file, but an asset that needs stripping is one that should not have been
// committed that way.
func TestPipe05AssetsAreLFOnly(t *testing.T) {
	for name, data := range pipe05Assets(t) {
		if bytes.Contains(data, []byte{'\r'}) {
			t.Errorf("%s contains a carriage return; level assets are LF only", name)
		}
		if !bytes.HasSuffix(data, []byte{'\n'}) {
			t.Errorf("%s does not end in a newline, so its last line would join the next file's first under a recursive grep", name)
		}
	}
}

// TestPipe05DeclaresItsAssetsFromThePack asserts the level reads the committed
// files rather than generating them, and that it does not do both.
//
// packs/core-linux-basics/assets/README.md forbids carrying a committed fixture
// AND a generate block for the same file: one of them would be dead, and nobody
// would know which one the level actually used.
func TestPipe05DeclaresItsAssetsFromThePack(t *testing.T) {
	lvl := pipe05Level(t)

	if len(lvl.Setup.Files) != 3 {
		t.Fatalf("pipe-05 declares %d files, want 3", len(lvl.Setup.Files))
	}
	for _, f := range lvl.Setup.Files {
		if f.Source == "" {
			t.Errorf("pipe-05 file %q has no source; its logs come from committed assets", f.Path)
		}
		if f.Generate != nil {
			t.Errorf("pipe-05 file %q sets both source and generate; assets/README.md forbids that because one of them is dead", f.Path)
		}
		if f.ContentSet {
			t.Errorf("pipe-05 file %q sets both source and inline content", f.Path)
		}
		if !strings.HasPrefix(f.Source, "assets/") {
			t.Errorf("pipe-05 file %q reads %q, which is not under assets/", f.Path, f.Source)
		}
	}
}

// itoa avoids importing strconv for one call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
