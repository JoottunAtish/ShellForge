package setup

import (
	"path"
	"strconv"
	"strings"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/content"
)

// TestFilesTwoExpectedValuesMatchTheGenerator keeps files-02's answers tied to
// the file the learner actually gets.
//
// That level asks for the first line, the last three lines, and the line count
// of a four thousand line log, and the log comes from the seeded loglines
// generator. The three expected values are constants in the level YAML, and
// nothing but this test connects them to the generator that produces the file.
// Change the seed, the line count, or the generator's output format, and the
// level becomes unsolvable in a way no amount of reading the YAML would reveal.
//
// It lives in this package because Generate does. internal/content cannot run
// a generator without importing this package, which is the wrong direction.
func TestFilesTwoExpectedValuesMatchTheGenerator(t *testing.T) {
	pack, err := content.Embedded()
	if err != nil {
		t.Fatalf("load the embedded pack: %v", err)
	}
	lvl, ok := pack.Level("files-02")
	if !ok {
		t.Fatal("files-02 is not in the shipped pack")
	}

	target := path.Join(lvl.Setup.Root, "deliveries.log")

	var spec *content.GenerateSpec
	for i := range lvl.Setup.Files {
		f := lvl.Setup.Files[i]
		if f.Generate != nil && path.Join(lvl.Setup.Root, f.Path) == target {
			spec = f.Generate
		}
	}
	if spec == nil {
		t.Fatalf("files-02 no longer generates %s, so this test proves nothing", target)
	}

	data, err := Generate(spec.Kind, spec.Seed, spec.Lines)
	if err != nil {
		t.Fatalf("generate %s: %v", target, err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")

	want := map[string]string{
		"first-line": lines[0],
		"last-lines": strings.Join(lines[len(lines)-3:], "\n"),
		"line-count": strconv.Itoa(len(lines)),
	}

	for _, c := range lvl.Checks {
		expected, named := want[c.ID]
		if !named {
			continue
		}
		got, _ := c.Params["value"].(string)
		if got != expected {
			t.Errorf("files-02 check %q expects\n  %q\nbut the generator produces\n  %q\n"+
				"The level's answers and its generated asset have drifted. Update the expected value and bump the level's version.",
				c.ID, got, expected)
		}
		delete(want, c.ID)
	}

	for id := range want {
		t.Errorf("files-02 no longer has a check with id %q, so its expected value is unverified", id)
	}
}
