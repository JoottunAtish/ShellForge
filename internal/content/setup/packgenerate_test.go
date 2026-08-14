package setup

import (
	"bytes"
	"path"
	"strconv"
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

	want := generatorAnswers(t, data)
	data = nil

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

// generatorAnswers computes the three values files-02 asks the learner for:
// the first line, the last three lines, and the line count.
//
// It scans rather than splitting the whole file into a slice. Four thousand
// lines is four thousand string headers, and under the race detector the
// shadow memory for that is enough to push this package past what
// ThreadSanitizer will reserve on a Windows host. The scan holds three lines
// and a counter.
func generatorAnswers(t *testing.T, data []byte) map[string]string {
	t.Helper()

	body := bytes.TrimRight(data, "\n")
	if len(body) == 0 {
		t.Fatal("the generator produced nothing")
	}

	count := bytes.Count(body, []byte("\n")) + 1

	first := body
	if i := bytes.IndexByte(body, '\n'); i >= 0 {
		first = body[:i]
	}

	// Walk back over three newlines. What follows the third one from the end
	// is the last three lines; fewer than three newlines means the whole
	// body is the answer.
	tail, cut := body, len(body)
	for i := 0; i < 3; i++ {
		j := bytes.LastIndexByte(body[:cut], '\n')
		if j < 0 {
			cut = -1
			break
		}
		cut = j
	}
	if cut >= 0 {
		tail = body[cut+1:]
	}

	return map[string]string{
		"first-line": string(first),
		"last-lines": string(tail),
		"line-count": strconv.Itoa(count),
	}
}
