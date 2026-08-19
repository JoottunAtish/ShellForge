package doctor

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/platform"
)

// fakeCall is one recorded invocation of fakeRunner.run.
type fakeCall struct {
	argv []string
}

// fakeResponse is one canned reply fakeRunner hands back. When block is
// true, run instead waits for ctx to finish and returns its error, which is
// what the cancellation and per-probe-budget tests need.
type fakeResponse struct {
	stdout []byte
	stderr []byte
	code   int
	err    error
	block  bool
}

// fakeRunner is a scriptable runner: each call consumes the next queued
// response in order, or a default clean success once the queue is empty, so
// a probe list that calls it more times than a test bothered to script does
// not panic.
type fakeRunner struct {
	mu        sync.Mutex
	calls     []fakeCall
	responses []fakeResponse
	next      int
}

func newFakeRunner(responses ...fakeResponse) *fakeRunner {
	return &fakeRunner{responses: responses}
}

func (f *fakeRunner) run(ctx context.Context, argv []string, stdin []byte) ([]byte, []byte, int, error) {
	f.mu.Lock()
	f.calls = append(f.calls, fakeCall{argv: append([]string(nil), argv...)})
	var resp fakeResponse
	if f.next < len(f.responses) {
		resp = f.responses[f.next]
		f.next++
	}
	f.mu.Unlock()

	if resp.block {
		<-ctx.Done()
		return nil, nil, -1, ctx.Err()
	}
	return resp.stdout, resp.stderr, resp.code, resp.err
}

func (f *fakeRunner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// fakeProber is a scriptable SandboxProber.
type fakeProber struct {
	provisioned, running bool
	detail               string
	err                  error
}

func (f fakeProber) Ping(ctx context.Context) (bool, bool, string, error) {
	return f.provisioned, f.running, f.detail, f.err
}

// TestApplicableProbesOmitsWindowsOnlyProbesElsewhere is AC6.
func TestApplicableProbesOmitsWindowsOnlyProbesElsewhere(t *testing.T) {
	crossPlatform := []string{
		"os_version", "cpu_virtualization", "docker_present",
		"docker_daemon_running", "disk_free", "terminal_vt_support",
		"sandbox_health",
	}
	allTwelve := []string{
		"os_version", "cpu_virtualization", "vm_platform_feature",
		"wsl_installed", "wsl_version_2", "wsl_kernel_current",
		"docker_present", "docker_daemon_running", "disk_free",
		"terminal_vt_support", "windows_terminal", "sandbox_health",
	}

	tests := []struct {
		goos string
		want []string
	}{
		{"linux", crossPlatform},
		{"darwin", crossPlatform},
		{"windows", allTwelve},
	}

	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			probes := applicableProbes(tt.goos, nil, newFakeRunner())
			var ids []string
			for _, p := range probes {
				ids = append(ids, p.ID())
			}
			if len(ids) != len(tt.want) {
				t.Fatalf("applicableProbes(%q) = %d ids, want %d\ngot:  %v\nwant: %v", tt.goos, len(ids), len(tt.want), ids, tt.want)
			}
			for i := range ids {
				if ids[i] != tt.want[i] {
					t.Errorf("applicableProbes(%q)[%d] = %q, want %q", tt.goos, i, ids[i], tt.want[i])
				}
			}
		})
	}
}

// TestEveryProbeReturnsAFullyPopulatedResult is part of AC2.
func TestEveryProbeReturnsAFullyPopulatedResult(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			probes := applicableProbes(goos, fakeProber{provisioned: true, running: true, detail: "ok"}, newFakeRunner())
			for _, p := range probes {
				res := p.Run(context.Background())
				if res.ID != p.ID() {
					t.Errorf("%s: Result.ID = %q, want %q", p.ID(), res.ID, p.ID())
				}
				if res.Detail == "" {
					t.Errorf("%s: Detail is empty", p.ID())
				}
				if res.Remediation == "" {
					t.Errorf("%s: Remediation is empty", p.ID())
				}
				if res.DocAnchor == "" {
					t.Errorf("%s: DocAnchor is empty", p.ID())
				}
			}
		})
	}
}

// TestRunOnACancelledContextReportsInsteadOfHanging is AC8's first test.
func TestRunOnACancelledContextReportsInsteadOfHanging(t *testing.T) {
	fr := newFakeRunner()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan Report, 1)
	go func() { done <- run(ctx, nil, fr, time.Second) }()

	select {
	case report := <-done:
		want := len(applicableProbes(reportGOOS(report), nil, fr))
		if len(report.Results) != want {
			t.Fatalf("run returned %d rows, want %d", len(report.Results), want)
		}
		for _, res := range report.Results {
			if res.Status != Warn {
				t.Errorf("%s: Status = %v on a cancelled context, want Warn", res.ID, res.Status)
			}
			if res.DocAnchor == "" {
				t.Errorf("%s: DocAnchor is empty on the interrupted path", res.ID)
			}
		}
		if calls := fr.callCount(); calls != 0 {
			t.Errorf("fake runner recorded %d calls, want 0: a probe spawned something after its context was already done", calls)
		}
	case <-time.After(time.Second):
		t.Fatal("run did not return within one second on a cancelled context")
	}
}

// reportGOOS recovers the goos a Report was gathered under, from its own
// Platform field, so the test above does not have to import runtime just to
// compute the same expectation run() itself would.
func reportGOOS(r Report) string { return r.Platform }

// TestRunBoundsEachProbeIndividually is AC8's second test.
func TestRunBoundsEachProbeIndividually(t *testing.T) {
	fr := newFakeRunner(
		fakeResponse{block: true}, fakeResponse{block: true}, fakeResponse{block: true},
		fakeResponse{block: true}, fakeResponse{block: true}, fakeResponse{block: true},
		fakeResponse{block: true}, fakeResponse{block: true}, fakeResponse{block: true},
		fakeResponse{block: true}, fakeResponse{block: true}, fakeResponse{block: true},
	)
	budget := 10 * time.Millisecond

	done := make(chan Report, 1)
	go func() { done <- run(context.Background(), nil, fr, budget) }()

	select {
	case report := <-done:
		alwaysRunnerBased := map[string]bool{
			"docker_present":        true,
			"docker_daemon_running": true,
			"wsl_installed":         true,
			"wsl_version_2":         true,
			"wsl_kernel_current":    true,
			"vm_platform_feature":   true,
		}
		if report.Platform != "windows" {
			alwaysRunnerBased["os_version"] = true
		} else {
			alwaysRunnerBased["cpu_virtualization"] = true
		}
		found := 0
		for _, res := range report.Results {
			if res.ID == "" {
				t.Errorf("a result has an empty ID; every probe must set its own id on every Result it returns")
				continue
			}
			if alwaysRunnerBased[res.ID] {
				found++
				if res.Status != Warn {
					t.Errorf("%s: Status = %v, want Warn: its runner call should have hit the per-probe budget", res.ID, res.Status)
				}
			}
		}
		if found == 0 {
			t.Fatal("no runner-dependent probe was found in the report; the id list or the probe wiring is wrong")
		}
	case <-time.After(time.Second):
		t.Fatal("run did not bound a hung probe to its own budget")
	}
}

// TestFixPerformsOnlyAllowlistedFixableActions is AC5.
func TestFixPerformsOnlyAllowlistedFixableActions(t *testing.T) {
	var recorded []string
	actions := map[string]fixAction{
		"disk_free": {
			describe: "created the data directory",
			apply:    func(ctx context.Context) error { recorded = append(recorded, "disk_free"); return nil },
		},
	}

	report := Report{Results: []Result{
		{ID: "disk_free", Status: Warn, Remediation: "create it", Fixable: true},
		{ID: "wsl_installed", Status: Fail, Remediation: "install wsl", Fixable: true},
		{ID: "docker_daemon_running", Status: Fail, Remediation: "start docker", Fixable: false},
		{ID: "terminal_vt_support", Status: OK, Remediation: "No action needed."},
	}}

	done, refused, err := fix(context.Background(), report, actions)
	if err != nil {
		t.Fatalf("fix returned an error: %v", err)
	}
	if len(recorded) != 1 || recorded[0] != "disk_free" {
		t.Fatalf("recorder saw %v, want exactly one call for disk_free", recorded)
	}
	if len(done) != 1 || done[0] == "" {
		t.Fatalf("done = %v, want one entry for disk_free", done)
	}
	if len(refused) != 2 {
		t.Fatalf("refused = %d entries, want 2 entries, one each for wsl_installed and docker_daemon_running: %v", len(refused), refused)
	}
	for _, id := range []string{"disk_free"} {
		for _, r := range refused {
			if containsString(r, id) {
				t.Errorf("refused contains the fixed id %q: %v", id, refused)
			}
		}
	}
}

func containsString(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || (len(needle) > 0 && indexOf(haystack, needle) >= 0))
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

// TestFixExecutesNothingWhenNoFailureIsFixable is refusal test one.
func TestFixExecutesNothingWhenNoFailureIsFixable(t *testing.T) {
	var calls int
	actions := map[string]fixAction{
		"disk_free": {apply: func(ctx context.Context) error { calls++; return nil }},
	}
	report := Report{Results: []Result{
		{ID: "wsl_installed", Status: Fail, Remediation: "install wsl", Fixable: false},
		{ID: "docker_daemon_running", Status: Fail, Remediation: "start docker", Fixable: false},
		{ID: "terminal_vt_support", Status: Warn, Remediation: "install windows terminal", Fixable: false},
	}}

	done, refused, err := fix(context.Background(), report, actions)
	if err != nil {
		t.Fatalf("fix returned an error: %v", err)
	}
	if len(done) != 0 {
		t.Fatalf("done = %v, want empty", done)
	}
	if len(refused) != 3 {
		t.Fatalf("refused = %d entries, want 3", len(refused))
	}
	if calls != 0 {
		t.Fatalf("recorder recorded %d calls, want 0", calls)
	}
}

// TestFixRefusesAFixableClaimItDoesNotOwn is refusal test two.
func TestFixRefusesAFixableClaimItDoesNotOwn(t *testing.T) {
	report := Report{Results: []Result{
		{ID: "wsl_installed", Status: Fail, Fixable: true, Remediation: "run wsl --install"},
	}}

	done, refused, err := fix(context.Background(), report, fixActions())
	if err != nil {
		t.Fatalf("fix returned an error: %v", err)
	}
	if len(done) != 0 {
		t.Fatalf("done = %v, want empty: fix must never perform an action outside its own allowlist", done)
	}
	if len(refused) != 1 {
		t.Fatalf("refused = %d entries, want 1", len(refused))
	}
}

// TestProbesNeverInvokeAFixAction is refusal test three, the doctor-package
// half. No probe holds a reference to a fixAction at all: Probe.Run takes
// only a context, so the only way to observe this is the same observable
// side effect the purity test checks, scoped here to exactly the one
// directory --fix is ever allowed to create.
func TestProbesNeverInvokeAFixAction(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_DATA_HOME", root)
	t.Setenv("LocalAppData", root)

	for _, goos := range []string{"linux", "windows"} {
		fr := newFakeRunner()
		for _, p := range applicableProbes(goos, fakeProber{provisioned: true, running: true}, fr) {
			_ = p.Run(context.Background())
		}
	}

	dataDir, err := platform.DataDir()
	if err != nil {
		t.Fatalf("platform.DataDir(): %v", err)
	}
	if _, statErr := os.Stat(dataDir); statErr == nil {
		t.Fatalf("platform.DataDir() (%s) exists after probes ran without --fix; a probe must never invoke a fix action", dataDir)
	}
}

// TestEveryProbeAppliesToItsOwnPlatformCorrectly is a small sanity check
// that the windowsOnly flag on each probe matches the ticket's table,
// independent of the ID-list test above.
func TestEveryProbeAppliesToItsOwnPlatformCorrectly(t *testing.T) {
	windowsOnly := map[string]bool{
		"vm_platform_feature": true,
		"wsl_installed":       true,
		"wsl_version_2":       true,
		"wsl_kernel_current":  true,
		"windows_terminal":    true,
	}
	for _, p := range newProbes(nil, newFakeRunner()) {
		want := windowsOnly[p.ID()]
		if got := p.AppliesTo("linux"); got == want {
			t.Errorf("%s.AppliesTo(linux) = %v, want %v", p.ID(), got, !want)
		}
		if !p.AppliesTo("windows") {
			t.Errorf("%s.AppliesTo(windows) = false, want true: every probe applies on windows", p.ID())
		}
	}
}

// TestFailedIsTrueOnlyWithAFailingRow pins the exit-status half of AC3 at
// the Report level; the cmd/shellforge half is in cmd_doctor_test.go.
func TestFailedIsTrueOnlyWithAFailingRow(t *testing.T) {
	tests := []struct {
		name string
		r    Report
		want bool
	}{
		{"all ok", Report{Results: []Result{{Status: OK}, {Status: OK}}}, false},
		{"warn only", Report{Results: []Result{{Status: OK}, {Status: Warn}}}, false},
		{"one fail", Report{Results: []Result{{Status: OK}, {Status: Fail}}}, true},
		{"empty", Report{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.r.Failed(); got != tt.want {
				t.Errorf("Failed() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestLevelStringAndJSON pins the lowercase-word contract.
func TestLevelStringAndJSON(t *testing.T) {
	tests := []struct {
		l    Level
		want string
	}{
		{OK, "ok"},
		{Warn, "warn"},
		{Fail, "fail"},
	}
	for _, tt := range tests {
		if got := tt.l.String(); got != tt.want {
			t.Errorf("%v.String() = %q, want %q", tt.l, got, tt.want)
		}
		b, err := tt.l.MarshalJSON()
		if err != nil {
			t.Fatalf("MarshalJSON: %v", err)
		}
		want := `"` + tt.want + `"`
		if string(b) != want {
			t.Errorf("%v.MarshalJSON() = %s, want %s", tt.l, b, want)
		}
	}
}
