package main

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/game"
	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
	"github.com/JoottunAtish/ShellForge/internal/store"
)

// playPack is three levels across two acts, chained by prerequisites, so
// every selection case has something to choose between.
func playPack() *content.Pack {
	return &content.Pack{
		ID: "core-linux-basics",
		Acts: []content.Act{
			{ID: "act1", Title: "Orientation", Levels: []string{"nav-01", "nav-02"}},
			{ID: "act2", Title: "Files", Levels: []string{"files-01"}},
		},
		Levels: []content.Level{
			{ID: "nav-01", Act: "act1", Title: "First Contact"},
			{ID: "nav-02", Act: "act1", Title: "Getting Around", Prerequisites: []string{"nav-01"}},
			{ID: "files-01", Act: "act2", Title: "Making Things", Prerequisites: []string{"nav-02"}},
		},
	}
}

func playNodes(t *testing.T, states map[string]store.LevelState) []game.Node {
	t.Helper()
	nodes, err := game.Resolve(playPack(), states)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return nodes
}

func TestPlayOnAFreshInstallStartsAtTheFirstLevel(t *testing.T) {
	level, reason, err := chooseLevel(playPack(), playNodes(t, nil), "")
	if err != nil {
		t.Fatalf("chooseLevel: %v", err)
	}
	if level == nil || level.ID != "nav-01" {
		t.Fatalf("chose %v, want nav-01", level)
	}
	if !strings.Contains(reason, "Orientation") || !strings.Contains(reason, "level 1 of 2") {
		t.Errorf("reason = %q, want it to say where the level sits", reason)
	}
}

func TestPlayResumesAtTheNextUnlockedLevel(t *testing.T) {
	states := map[string]store.LevelState{
		"nav-01": {LevelID: "nav-01", Status: store.StatusPassed},
	}

	level, reason, err := chooseLevel(playPack(), playNodes(t, states), "")
	if err != nil {
		t.Fatalf("chooseLevel: %v", err)
	}
	if level.ID != "nav-02" {
		t.Errorf("chose %q after passing nav-01, want nav-02", level.ID)
	}
	if !strings.Contains(reason, "level 2 of 2") {
		t.Errorf("reason = %q, want it to place the level in its act", reason)
	}
}

// A skipped level unlocks what comes after it, the same way a pass does.
func TestPlayResumesPastASkippedLevel(t *testing.T) {
	states := map[string]store.LevelState{
		"nav-01": {LevelID: "nav-01", Status: store.StatusSkipped},
	}

	level, _, err := chooseLevel(playPack(), playNodes(t, states), "")
	if err != nil {
		t.Fatalf("chooseLevel: %v", err)
	}
	if level.ID != "nav-02" {
		t.Errorf("chose %q after skipping nav-01, want nav-02", level.ID)
	}
}

func TestPlayWithAnExplicitIdReplaysThatLevel(t *testing.T) {
	states := map[string]store.LevelState{
		"nav-01": {LevelID: "nav-01", Status: store.StatusPassed},
	}

	level, reason, err := chooseLevel(playPack(), playNodes(t, states), "nav-01")
	if err != nil {
		t.Fatalf("chooseLevel: %v", err)
	}
	if level.ID != "nav-01" {
		t.Errorf("chose %q, want the level that was named", level.ID)
	}
	if !strings.Contains(reason, "replaying can improve your best score") {
		t.Errorf("reason = %q, want it to say what replaying is worth", reason)
	}
}

func TestPlayRefusesALockedLevelByNamingWhatIsMissing(t *testing.T) {
	_, _, err := chooseLevel(playPack(), playNodes(t, nil), "files-01")
	if err == nil {
		t.Fatal("chooseLevel accepted a locked level")
	}
	assertUserFacing(t, err)
	remediation := remediationOf(t, err)
	if !strings.Contains(remediation, "nav-02") {
		t.Errorf("the refusal does not name the unmet prerequisite: %s", remediation)
	}
	if !strings.Contains(remediation, "locked") {
		t.Errorf("the refusal does not say the level is locked: %s", remediation)
	}
}

func TestPlayRefusesAnUnknownIdByNamingTheOnesThatExist(t *testing.T) {
	_, _, err := chooseLevel(playPack(), playNodes(t, nil), "nav-99")
	if err == nil {
		t.Fatal("chooseLevel accepted an unknown level id")
	}
	assertUserFacing(t, err)
	remediation := remediationOf(t, err)
	for _, want := range []string{"nav-01", "nav-02", "files-01"} {
		if !strings.Contains(remediation, want) {
			t.Errorf("the refusal does not name %q: %s", want, remediation)
		}
	}
}

func TestPlayReportsACompleteCampaignRatherThanErroring(t *testing.T) {
	states := map[string]store.LevelState{
		"nav-01":   {LevelID: "nav-01", Status: store.StatusPassed},
		"nav-02":   {LevelID: "nav-02", Status: store.StatusPassed},
		"files-01": {LevelID: "files-01", Status: store.StatusPassed},
	}

	level, _, err := chooseLevel(playPack(), playNodes(t, states), "")
	if err != nil {
		t.Fatalf("chooseLevel on a complete campaign returned an error: %v", err)
	}
	if level != nil {
		t.Fatalf("chose %q on a complete campaign, want nothing left to choose", level.ID)
	}
}

func TestCampaignCompleteNamesTheFinalRank(t *testing.T) {
	pack := playPack()
	pack.Ranks = []content.Rank{
		{ID: "novice", Title: "Novice", MinXP: 0},
		{ID: "apprentice", Title: "Apprentice", MinXP: 300},
	}
	states := map[string]store.LevelState{
		"nav-01":   {LevelID: "nav-01", Status: store.StatusPassed},
		"nav-02":   {LevelID: "nav-02", Status: store.StatusPassed},
		"files-01": {LevelID: "files-01", Status: store.StatusPassed},
	}

	got := renderCampaignComplete(pack, playNodes(t, states), 400)
	for _, want := range []string{"finished every level", "3 levels", "Apprentice", "400 XP"} {
		if !strings.Contains(got, want) {
			t.Errorf("the ending does not mention %q:\n%s", want, got)
		}
	}
}

// --- argument parsing ---

func TestParsePlayArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		want    playOptions
		wantErr bool
	}{
		{name: "no arguments resumes", args: nil, want: playOptions{Live: true}},
		{name: "a level id", args: []string{"nav-02"}, want: playOptions{LevelID: "nav-02", Live: true}},
		{name: "next", args: []string{"--next"}, want: playOptions{DryRun: true, Live: true}},
		{name: "next with an id", args: []string{"nav-02", "--next"}, want: playOptions{LevelID: "nav-02", DryRun: true, Live: true}},
		{name: "log level joined", args: []string{"--log-level=debug"}, want: playOptions{Debug: true, Live: true}},
		{name: "log level split", args: []string{"--log-level", "debug"}, want: playOptions{Debug: true, Live: true}},
		{name: "log level something else", args: []string{"--log-level", "info"}, want: playOptions{Live: true}},
		{name: "an unknown flag", args: []string{"--turbo"}, wantErr: true},
		{name: "two level ids", args: []string{"nav-01", "nav-02"}, wantErr: true},
		{name: "log level with nothing after it", args: []string{"--log-level"}, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePlayArgs(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parsePlayArgs(%v) succeeded, want an error", tc.args)
				}
				assertUserFacing(t, err)
				return
			}
			if err != nil {
				t.Fatalf("parsePlayArgs(%v): %v", tc.args, err)
			}
			if got != tc.want {
				t.Errorf("parsePlayArgs(%v) = %+v, want %+v", tc.args, got, tc.want)
			}
		})
	}
}

// TestParsePlayArgsLiveCheck is the same table shape TestParseRunArgsLiveCheck
// pins for `run`: cmd_play.go builds the shared runOptions from this parse,
// so without this a learner could not turn live checking off for `play`.
func TestParsePlayArgsLiveCheck(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantLive bool
		wantErr  bool
	}{
		{"no flag defaults to on", nil, true, false},
		{"off with an equals sign", []string{"--live-check=off"}, false, false},
		{"off as two arguments", []string{"--live-check", "off"}, false, false},
		{"on with an equals sign", []string{"--live-check=on"}, true, false},
		{"alongside --next and a level id", []string{"nav-02", "--next", "--live-check=off"}, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePlayArgs(tc.args)
			if err != nil {
				t.Fatalf("parsePlayArgs(%v): %v", tc.args, err)
			}
			if got.Live != tc.wantLive {
				t.Errorf("Live = %v, want %v", got.Live, tc.wantLive)
			}
		})
	}

	t.Run("a bogus value", func(t *testing.T) {
		_, err := parsePlayArgs([]string{"--live-check=sometimes"})
		if err == nil {
			t.Fatal("parsePlayArgs accepted a bogus --live-check value")
		}
		assertUserFacing(t, err)
	})
}

// --next must not provision anything. This asserts it where it can be
// asserted without a Docker daemon: the selection and the dry run return
// happen entirely above openSandbox, which is the only thing in the whole
// flow that touches a runtime.
func TestNextResolvesNoRuntime(t *testing.T) {
	opts, err := parsePlayArgs([]string{"--next"})
	if err != nil {
		t.Fatalf("parsePlayArgs: %v", err)
	}
	if !opts.DryRun {
		t.Fatal("--next did not set DryRun, so runPlay would provision a sandbox")
	}

	// The guarantee itself, held by the source rather than by a fake: the
	// dry run returns before checkInteractiveShellSupported and runLevel,
	// and runLevel is the only caller of openSandbox on this path.
	assertReturnsBefore(t, "cmd_play.go", "if opts.DryRun {", "runLevel(")
}

func TestPlayPrintsTheChoiceBeforeAnythingSlow(t *testing.T) {
	// The reason line is printed before the DryRun return, which is itself
	// before provisioning: a learner who expected a different level can
	// read it and press Ctrl-C rather than wait for a container.
	assertReturnsBefore(t, "cmd_play.go", `fmt.Fprintf(out, "Next:`, "runLevel(")
}

// assertReturnsBefore asserts that first appears before second in a source
// file, which is how the two ordering guarantees above are pinned without a
// Docker daemon to observe them with.
func assertReturnsBefore(t *testing.T, file, first, second string) {
	t.Helper()
	src := readSource(t, file)
	i, j := strings.Index(src, first), strings.Index(src, second)
	if i < 0 {
		t.Fatalf("%s does not contain %q", file, first)
	}
	if j < 0 {
		t.Fatalf("%s does not contain %q", file, second)
	}
	if i > j {
		t.Errorf("%s: %q appears after %q, so the ordering guarantee is gone", file, first, second)
	}
}

// readSource reads a file from this package's own directory, for the two
// ordering assertions above.
func readSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

// remediationOf is the remediation a *ux.Error carries, which is where the
// sentence a learner acts on lives: Error() is only the op and the cause.
func remediationOf(t *testing.T, err error) string {
	t.Helper()
	var uxErr *ux.Error
	if !errors.As(err, &uxErr) {
		t.Fatalf("error is not a *ux.Error: %v", err)
	}
	return uxErr.Remediation
}

// --------------------------------------------------------------------------
// Carrying on to the next level
// --------------------------------------------------------------------------

// TestReadLineLeavesTheRestOfTheStream is the assertion that makes carrying
// on safe, and it is not a style preference.
//
// The stdin this reads from is not finished with: the very next thing to
// read it is internal/pty, handing the learner's keystrokes to the next
// level's shell. A bufio.Scanner reads ahead by up to its whole buffer, so a
// learner who typed the answer and their first command in one go would lose
// the command. readLine stops at the newline and leaves the rest where it
// belongs.
func TestReadLineLeavesTheRestOfTheStream(t *testing.T) {
	in := strings.NewReader("y\nls -la\nexit\n")

	line, ok := readLine(in)
	if !ok || line != "y" {
		t.Fatalf("readLine = %q, %v, want \"y\", true", line, ok)
	}

	rest, err := io.ReadAll(in)
	if err != nil {
		t.Fatalf("read the rest of the stream: %v", err)
	}
	if string(rest) != "ls -la\nexit\n" {
		t.Errorf("readLine swallowed keystrokes meant for the next level's shell: %q left, want %q",
			rest, "ls -la\nexit\n")
	}
}

func TestReadLine(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		wantLine string
		wantOK   bool
	}{
		{"a plain answer", "n\n", "n", true},
		{"an empty line", "\n", "", true},
		{"a CRLF terminal", "y\r\n", "y", true},
		{"no newline before EOF", "y", "y", true},
		{"EOF with nothing typed", "", "", false},
		{"surrounding spaces are left to the caller", "  y  \n", "  y  ", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			line, ok := readLine(strings.NewReader(tc.in))
			if line != tc.wantLine || ok != tc.wantOK {
				t.Errorf("readLine(%q) = %q, %v, want %q, %v", tc.in, line, ok, tc.wantLine, tc.wantOK)
			}
		})
	}
}

func TestOfferNextLevel(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"Enter carries on", "\n", true},
		{"y carries on", "y\n", true},
		{"yes carries on", "yes\n", true},
		{"Y carries on", "Y\n", true},
		{"n stops", "n\n", false},
		{"no stops", "no\n", false},
		{"anything unrecognized stops", "maybe later\n", false},
		{"EOF stops", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder
			got := offerNextLevel(strings.NewReader(tc.in), &out)
			if got != tc.want {
				t.Errorf("offerNextLevel(%q) = %v, want %v", tc.in, got, tc.want)
			}
			if !strings.Contains(out.String(), "?") {
				t.Errorf("no question was asked: %q", out.String())
			}
		})
	}
}

// TestOfferNextLevelFailsClosedOnAnUnreadableAnswer pins the direction the
// uncertainty falls in. Carrying on provisions a container and starts a
// level; a learner whose answer could not be read did not ask for either.
func TestOfferNextLevelFailsClosedOnAnUnreadableAnswer(t *testing.T) {
	var out strings.Builder
	if offerNextLevel(errReader{errors.New("stdin went away")}, &out) {
		t.Error("an unreadable answer was taken as yes")
	}
	if offerNextLevel(nil, &out) {
		t.Error("a nil reader was taken as yes")
	}
}

// errReader is an io.Reader that only ever fails, standing in for a stdin
// that has gone away underneath the question.
type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

// TestCanAskRefusesANonTerminal is what keeps `play` behaving exactly as it
// did before this existed when it is run from a script or in CI: a question
// nobody can answer must not be asked, and must not be treated as answered.
func TestCanAskRefusesANonTerminal(t *testing.T) {
	if canAsk(nil) {
		t.Error("canAsk said yes to a nil reader")
	}
	if canAsk(strings.NewReader("y\n")) {
		t.Error("canAsk said yes to a reader that is not a terminal")
	}

	// A real *os.File that is not a terminal: the case a piped stdin
	// actually produces, which a plain io.Reader does not exercise.
	f, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer f.Close()
	if canAsk(f) {
		t.Error("canAsk said yes to a file that is not a terminal")
	}
}

// TestPlayAsksBeforeProvisioningTheNextLevel pins the ordering that is the
// whole Ctrl-C fix: the question is asked, and only then is the next level
// resolved and provisioned. Asserted against the source, the way the two
// ordering guarantees above it are, because observing it needs a Docker
// daemon and a learner.
func TestPlayAsksBeforeProvisioningTheNextLevel(t *testing.T) {
	assertReturnsBefore(t, "cmd_play.go", "offerNextLevel(opts.In, out)", "levelID = \"\"")
}

// TestPlayNeverReadsAStdinItCannotAsk is the regression test for the review
// finding on this PR: `canAsk` was consulted for the pass banner's wording
// and then dropped, so the guard that decides whether to ask used the raw
// "is this a resume" flag on its own. With a stdin that is not a terminal
// the question was printed where nobody could answer it, and `readLine` took
// a line of somebody else's input on the way past.
//
// Asserted against the source rather than by running a level, for the same
// reason the two ordering guarantees above it are: observing it needs a
// Docker daemon. What is pinned is that the decision is made once, so the
// value handed to runLevel and the value guarding the question cannot drift
// apart again.
func TestPlayNeverReadsAStdinItCannotAsk(t *testing.T) {
	src := readSource(t, "cmd_play.go")

	if !strings.Contains(src, `advance := levelID == "" && canAsk(opts.In)`) {
		t.Error("the advance decision no longer folds canAsk in, so the question can be asked where nobody can answer it")
	}
	if strings.Contains(src, "advance && canAsk(") {
		t.Error("canAsk is being applied to one use of the advance decision and not the other, which is how the two drifted apart before")
	}
}

// TestOfferNextLevelReadsExactlyOneLine is the other half of the same
// finding. Even asked at the right moment, the question must take the answer
// and nothing after it: the next thing to read this stdin is internal/pty,
// handing keystrokes to the next level's shell.
func TestOfferNextLevelReadsExactlyOneLine(t *testing.T) {
	in := strings.NewReader("y\nls -la\n")
	var out strings.Builder

	if !offerNextLevel(in, &out) {
		t.Fatal("offerNextLevel did not read the y")
	}

	rest, err := io.ReadAll(in)
	if err != nil {
		t.Fatalf("read the rest of the stream: %v", err)
	}
	if string(rest) != "ls -la\n" {
		t.Errorf("offerNextLevel swallowed keystrokes meant for the next level's shell: %q left, want %q", rest, "ls -la\n")
	}
}
