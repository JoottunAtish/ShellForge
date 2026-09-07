package main

import (
	"errors"
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
		{name: "no arguments resumes", args: nil, want: playOptions{}},
		{name: "a level id", args: []string{"nav-02"}, want: playOptions{LevelID: "nav-02"}},
		{name: "next", args: []string{"--next"}, want: playOptions{DryRun: true}},
		{name: "next with an id", args: []string{"nav-02", "--next"}, want: playOptions{LevelID: "nav-02", DryRun: true}},
		{name: "log level joined", args: []string{"--log-level=debug"}, want: playOptions{Debug: true}},
		{name: "log level split", args: []string{"--log-level", "debug"}, want: playOptions{Debug: true}},
		{name: "log level something else", args: []string{"--log-level", "info"}, want: playOptions{}},
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
