package wsl

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/platform"
	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
	shellforgeruntime "github.com/JoottunAtish/ShellForge/internal/runtime"
)

// fakeRunner records every argv it was asked to run and returns canned
// results in order. It never spawns a process, so the tests using it run
// with no wsl.exe on PATH and no Windows.
type fakeRunner struct {
	results []fakeResult
	calls   [][]string
}

type fakeResult struct {
	stdout []byte
	stderr []byte
	code   int
	err    error
}

func (f *fakeRunner) run(_ context.Context, argv []string, _ []byte) ([]byte, []byte, int, error) {
	f.calls = append(f.calls, append([]string(nil), argv...))
	if len(f.results) == 0 {
		return nil, nil, 0, nil
	}
	r := f.results[0]
	f.results = f.results[1:]
	return r.stdout, r.stderr, r.code, r.err
}

// fakeFetcher records every fetch it was asked to perform and never dials
// out. write, when set, is written to dest so a digest check downstream has
// real bytes to hash.
type fakeFetcher struct {
	calls []struct{ url, dest string }
	write []byte
	err   error
}

func (f *fakeFetcher) fetch(_ context.Context, url, dest string) error {
	f.calls = append(f.calls, struct{ url, dest string }{url, dest})
	if f.err != nil {
		return f.err
	}
	if f.write != nil {
		return os.WriteFile(dest, f.write, 0o600)
	}
	return nil
}

func assertArgvSequence(t *testing.T, op string, got, want [][]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s ran %d wsl.exe invocation(s), want %d\ngot:  %v\nwant: %v", op, len(got), len(want), got, want)
	}
	for i := range want {
		if !equalArgv(got[i], want[i]) {
			t.Errorf("%s invocation %d:\ngot:  %v\nwant: %v", op, i, got[i], want[i])
		}
	}
}

func equalArgv(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// verboseRow is one row of a synthetic `wsl -l -v` fixture.
type verboseRow struct {
	name    string
	state   string
	version int
	def     bool
}

func verboseListFixture(rows ...verboseRow) []byte {
	var b strings.Builder
	b.WriteString("  NAME                   STATE           VERSION\r\n")
	for _, r := range rows {
		if r.def {
			b.WriteString("* ")
		} else {
			b.WriteString("  ")
		}
		fmt.Fprintf(&b, "%-22s %-15s %d\r\n", r.name, r.state, r.version)
	}
	return encodeUTF16LE(true, b.String())
}

func quietListFixture(names ...string) []byte {
	var b strings.Builder
	for _, n := range names {
		b.WriteString(n)
		b.WriteString("\r\n")
	}
	return encodeUTF16LE(true, b.String())
}

// setTestDataDirs points platform.DataDir and platform.CacheDir at two
// temporary directories, so paths.go's real filesystem calls (EnsureDir,
// RemoveAll, ReadDir) run against a t.TempDir() rather than a developer's
// or CI runner's real Shellforge state.
func setTestDataDirs(t *testing.T, dataDir, cacheDir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		// Windows derives both from %LocalAppData%, nested one level, so a
		// single env var cannot separate them; this matches paths.go's own
		// real Windows behavior rather than fighting it.
		t.Setenv("LOCALAPPDATA", dataDir)
		return
	}
	t.Setenv("XDG_DATA_HOME", dataDir)
	t.Setenv("XDG_CACHE_HOME", cacheDir)
}

func TestNewRefusesAnIdentifierValidIdentifierRejects(t *testing.T) {
	cases := []string{"", "sand box", "sandbox;rm", "-f", "--force", "sand/box"}
	for _, v := range cases {
		t.Run(v, func(t *testing.T) {
			_, err := New(v)
			if err == nil {
				t.Fatalf("New(%q) = nil error, want a refusal", v)
			}
			if !strings.Contains(err.Error(), v) {
				t.Errorf("New(%q) error = %q, want it to name the offending value", v, err.Error())
			}
		})
	}
}

func TestNewRefusesANameThatIsNotAShellforgeConstant(t *testing.T) {
	for _, v := range []string{"Debian", "Ubuntu"} {
		t.Run(v, func(t *testing.T) {
			_, err := New(v)
			if err == nil {
				t.Fatalf("New(%q) = nil error, want a refusal", v)
			}
			if !strings.Contains(err.Error(), v) {
				t.Errorf("New(%q) error = %q, want it to name the given value", v, err.Error())
			}
			if !strings.Contains(err.Error(), sandboxDistro) || !strings.Contains(err.Error(), contractDistro) {
				t.Errorf("New(%q) error = %q, want it to name both permitted constants", v, err.Error())
			}
		})
	}
}

func TestDestroyRefusesADistributionNameThatIsNotThePackageConstant(t *testing.T) {
	fake := &fakeRunner{}
	rt := &wslRuntime{distro: "Debian", run: fake}
	err := rt.Destroy(context.Background())
	if err == nil {
		t.Fatal("Destroy with distro \"Debian\" = nil error, want a refusal")
	}
	if !strings.Contains(err.Error(), "Debian") || !strings.Contains(err.Error(), sandboxDistro) {
		t.Errorf("Destroy error = %q, want it to name both the given and the permitted values", err.Error())
	}
	if len(fake.calls) != 0 {
		t.Errorf("Destroy with a non-constant distro invoked wsl.exe %d time(s), want zero: %v", len(fake.calls), fake.calls)
	}
}

func TestDestroyRefusesAnEmptyDistributionName(t *testing.T) {
	fake := &fakeRunner{}
	rt := &wslRuntime{distro: "", run: fake}
	if err := rt.Destroy(context.Background()); err == nil {
		t.Fatal("Destroy with an empty distro = nil error, want a refusal")
	}
	if len(fake.calls) != 0 {
		t.Errorf("Destroy with an empty distro invoked wsl.exe %d time(s), want zero", len(fake.calls))
	}
}

// TestDestroyReportsAMarkerReadFailureDistinctFromNoMarker covers the case
// where hasSandboxMarker's own wsl.exe invocation could not run at all, as
// opposed to running and reporting no marker. Before this was split, both
// were reported as wsl-name-collision, whose remediation tells the learner
// to run `wsl --unregister shellforge-sandbox` by hand: exactly the
// destructive command that removes a healthy sandbox when the real cause is
// wsl.exe not answering.
func TestDestroyReportsAMarkerReadFailureDistinctFromNoMarker(t *testing.T) {
	readErr := errors.New("exec: \"wsl.exe\": file already closed")
	fake := &fakeRunner{results: []fakeResult{
		{stdout: quietListFixture(sandboxDistro), code: 0}, // enumerate: present
		{err: readErr}, // marker read: the invocation itself failed
	}}
	rt := &wslRuntime{distro: sandboxDistro, run: fake}

	err := rt.Destroy(context.Background())
	if err == nil {
		t.Fatal("Destroy with a failed marker read = nil error, want a refusal")
	}
	if hasDocAnchor(err, "wsl-name-collision") {
		t.Errorf("Destroy error = %v, want it not to claim a name collision when the marker read itself failed", err)
	}
	if !errors.Is(err, readErr) {
		t.Errorf("Destroy error = %v, want it to wrap the underlying marker-read failure so the cause is not lost", err)
	}
	for _, call := range fake.calls {
		if containsArg(call, "--unregister") {
			t.Errorf("Destroy with a failed marker read ran %v, want no --unregister at all", call)
		}
	}
}

func TestDestroyRefusesADistributionWithoutTheSandboxMarker(t *testing.T) {
	fake := &fakeRunner{results: []fakeResult{
		{stdout: quietListFixture(sandboxDistro), code: 0}, // enumerate: present
		{code: 1}, // marker read: fails
	}}
	rt := &wslRuntime{distro: sandboxDistro, run: fake}

	err := rt.Destroy(context.Background())
	if err == nil {
		t.Fatal("Destroy without the sandbox marker = nil error, want a refusal")
	}
	if !hasDocAnchor(err, "wsl-name-collision") {
		t.Errorf("Destroy error = %v, want DocAnchor wsl-name-collision", err)
	}
	for _, call := range fake.calls {
		if containsArg(call, "--unregister") {
			t.Errorf("Destroy without a marker ran %v, want no --unregister at all", call)
		}
	}
}

func TestDestroyRefusesAMarkerWithTheWrongContent(t *testing.T) {
	fake := &fakeRunner{results: []fakeResult{
		{stdout: quietListFixture(sandboxDistro), code: 0},
		{stdout: []byte("not-the-marker\n"), code: 0},
	}}
	rt := &wslRuntime{distro: sandboxDistro, run: fake}

	err := rt.Destroy(context.Background())
	if err == nil {
		t.Fatal("Destroy with a wrong marker = nil error, want a refusal")
	}
	if !hasDocAnchor(err, "wsl-name-collision") {
		t.Errorf("Destroy error = %v, want DocAnchor wsl-name-collision", err)
	}
	for _, call := range fake.calls {
		if containsArg(call, "--unregister") {
			t.Errorf("Destroy with a wrong marker ran %v, want no --unregister at all", call)
		}
	}
}

func TestDestroyReturnsNilWhenNothingIsProvisioned(t *testing.T) {
	// The not-present branch now also resolves and removes the install
	// directory, so this needs an isolated DataDir/CacheDir like every other
	// test that exercises paths.go's real filesystem calls; without it,
	// Destroy would resolve a real path on the machine running the test.
	setTestDataDirs(t, t.TempDir(), t.TempDir())
	fake := &fakeRunner{results: []fakeResult{
		{stdout: quietListFixture("Ubuntu", "Debian"), code: 0},
	}}
	rt := &wslRuntime{distro: sandboxDistro, run: fake}

	if err := rt.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy with nothing provisioned = %v, want nil (idempotent)", err)
	}
	for _, call := range fake.calls {
		if containsArg(call, "--unregister") {
			t.Errorf("Destroy with nothing provisioned ran %v, want no --unregister at all", call)
		}
	}
}

// TestDestroyRemovesAnOrphanedInstallDirectoryWhenNothingIsRegistered covers
// a retried Destroy after an earlier one unregistered the distribution but
// then failed to remove its install directory, for instance because Windows
// still held ext4.vhdx open. Before this, the not-present branch returned
// nil as soon as `wsl -l -q` no longer listed the distribution, without ever
// touching the install directory, so the second Destroy reported success
// while the 2 GB backing file stayed on disk forever.
func TestDestroyRemovesAnOrphanedInstallDirectoryWhenNothingIsRegistered(t *testing.T) {
	dataDir := t.TempDir()
	setTestDataDirs(t, dataDir, t.TempDir())
	resolvedDataDir, err := platform.DataDir()
	if err != nil {
		t.Fatalf("platform.DataDir: %v", err)
	}

	dir := installDirFor(resolvedDataDir, sandboxDistro)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ext4.vhdx"), []byte("orphaned by an earlier failed Destroy"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	fake := &fakeRunner{results: []fakeResult{
		{stdout: quietListFixture("Ubuntu", "Debian"), code: 0}, // enumerate: sandboxDistro already gone
	}}
	rt := &wslRuntime{distro: sandboxDistro, run: fake}

	if err := rt.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy with nothing registered but a leftover install directory = %v, want nil (finishing the earlier cleanup)", err)
	}
	if _, statErr := os.Stat(dir); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("orphaned install directory %s still exists after Destroy, want it gone", dir)
	}
	for _, call := range fake.calls {
		if containsArg(call, "--unregister") || containsArg(call, "--terminate") {
			t.Errorf("Destroy with nothing registered ran %v, want no terminate or unregister at all", call)
		}
	}
}

func TestDestroyRefusesWhenMoreThanOneDistributionDisappears(t *testing.T) {
	tmp := t.TempDir()
	setTestDataDirs(t, tmp, t.TempDir())

	fake := &fakeRunner{results: []fakeResult{
		{stdout: quietListFixture(sandboxDistro, "Ubuntu", "Debian"), code: 0}, // enumerate before
		{stdout: []byte(markerContent), code: 0},                               // marker read
		{code: 0},                                                              // terminate
		{code: 0},                                                              // unregister
		{stdout: quietListFixture(), code: 0},                                  // enumerate after: everything gone
	}}
	rt := &wslRuntime{distro: sandboxDistro, run: fake}

	err := rt.Destroy(context.Background())
	if err == nil {
		t.Fatal("Destroy that removed more than one distribution = nil error, want a refusal")
	}
	if !strings.Contains(err.Error(), "Ubuntu") || !strings.Contains(err.Error(), "Debian") {
		t.Errorf("Destroy error = %q, want it to name both distributions that disappeared", err.Error())
	}
}

// TestDestroyRefusesWhenTheUnregisteredNameStillAppears covers the
// len(gone) == 0 branch: `wsl --unregister` reported success, but
// `wsl -l -q` still lists the distribution afterward. This is the case a
// caller trusting the exit code alone, rather than diffing before and
// after, would silently report as a successful Destroy.
func TestDestroyRefusesWhenTheUnregisteredNameStillAppears(t *testing.T) {
	tmp := t.TempDir()
	setTestDataDirs(t, tmp, t.TempDir())

	fake := &fakeRunner{results: []fakeResult{
		{stdout: quietListFixture(sandboxDistro), code: 0}, // enumerate before
		{stdout: []byte(markerContent), code: 0},           // marker read
		{code: 0},                                          // terminate
		{code: 0},                                          // unregister
		{stdout: quietListFixture(sandboxDistro), code: 0}, // enumerate after: unchanged
	}}
	rt := &wslRuntime{distro: sandboxDistro, run: fake}

	err := rt.Destroy(context.Background())
	if err == nil {
		t.Fatal("Destroy where the distribution still appears after unregister = nil error, want a refusal")
	}
	if !strings.Contains(err.Error(), sandboxDistro) {
		t.Errorf("Destroy error = %q, want it to name the distribution that did not disappear", err.Error())
	}
}

// TestDestroyRefusesWhenADifferentDistributionDisappears covers the
// gone[0] != rt.distro branch: `wsl --unregister shellforge-sandbox`
// reported success, exactly one distribution vanished from `wsl -l -q`, but
// it was not the one this call asked to remove. This is the worst real
// failure this package can produce, and is the one branch of the
// enumerate-and-diff guard that had no test at all.
func TestDestroyRefusesWhenADifferentDistributionDisappears(t *testing.T) {
	tmp := t.TempDir()
	setTestDataDirs(t, tmp, t.TempDir())

	fake := &fakeRunner{results: []fakeResult{
		{stdout: quietListFixture(sandboxDistro, "Debian"), code: 0}, // enumerate before
		{stdout: []byte(markerContent), code: 0},                     // marker read
		{code: 0},                                                    // terminate
		{code: 0},                                                    // unregister
		{stdout: quietListFixture(sandboxDistro), code: 0},           // enumerate after: Debian vanished instead
	}}
	rt := &wslRuntime{distro: sandboxDistro, run: fake}

	err := rt.Destroy(context.Background())
	if err == nil {
		t.Fatal("Destroy where a different distribution disappeared = nil error, want a refusal")
	}
	if !strings.Contains(err.Error(), sandboxDistro) || !strings.Contains(err.Error(), "Debian") {
		t.Errorf("Destroy error = %q, want it to name both the requested and the actually-removed distribution", err.Error())
	}
}

func TestDestroyRemovesTheInstallDirectoryAndNamesThePath(t *testing.T) {
	dataDir := t.TempDir()
	setTestDataDirs(t, dataDir, t.TempDir())
	resolvedDataDir, err := platform.DataDir()
	if err != nil {
		t.Fatalf("platform.DataDir: %v", err)
	}

	dir := installDirFor(resolvedDataDir, sandboxDistro)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ext4.vhdx"), []byte("fake vhdx"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	fake := &fakeRunner{results: []fakeResult{
		{stdout: quietListFixture(sandboxDistro), code: 0},
		{stdout: []byte(markerContent), code: 0},
		{code: 0},                             // terminate
		{code: 0},                             // unregister
		{stdout: quietListFixture(), code: 0}, // enumerate after: gone
	}}
	rt := &wslRuntime{distro: sandboxDistro, run: fake}

	if err := rt.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("install directory %s still exists after Destroy, want it gone", dir)
	}

	// The caller (the CLI at L5) is what prints the path to the user; this
	// package's contribution is that the path is derivable and correct
	// after a successful Destroy, which installDir still reports even
	// though the directory itself is now gone.
	got, err := rt.installDir()
	if err != nil {
		t.Fatalf("installDir: %v", err)
	}
	if got != dir {
		t.Errorf("installDir() after Destroy = %q, want %q so the caller can tell the user where to check", got, dir)
	}
}

func TestDestroyRefusesToRemoveAnInstallDirectoryWithUnexpectedEntries(t *testing.T) {
	dataDir := t.TempDir()
	setTestDataDirs(t, dataDir, t.TempDir())
	resolvedDataDir, err := platform.DataDir()
	if err != nil {
		t.Fatalf("platform.DataDir: %v", err)
	}

	dir := installDirFor(resolvedDataDir, sandboxDistro)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	surprise := filepath.Join(dir, "not-a-vhdx.txt")
	if err := os.WriteFile(surprise, []byte("surprise"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	fake := &fakeRunner{results: []fakeResult{
		{stdout: quietListFixture(sandboxDistro), code: 0},
		{stdout: []byte(markerContent), code: 0},
	}}
	rt := &wslRuntime{distro: sandboxDistro, run: fake}

	err = rt.Destroy(context.Background())
	if err == nil {
		t.Fatal("Destroy of an install directory with unexpected contents = nil error, want a refusal")
	}
	if _, statErr := os.Stat(surprise); statErr != nil {
		t.Errorf("unexpected file %s was removed, want it left alone: %v", surprise, statErr)
	}
	for _, call := range fake.calls {
		if containsArg(call, "--unregister") || containsArg(call, "--terminate") {
			t.Errorf("Destroy with unexpected install directory contents ran %v, want no terminate or unregister", call)
		}
	}
}

func TestProvisionRefusesAURLReferenceWithAnEmptySHA256(t *testing.T) {
	fake := &fakeRunner{results: []fakeResult{
		{stdout: verboseListFixture(), code: 0}, // findRow: absent
	}}
	fetch := &fakeFetcher{}
	rt := &wslRuntime{distro: sandboxDistro, run: fake, fetch: fetch}

	err := rt.Provision(context.Background(), shellforgeruntime.ImageSpec{
		Reference: "https://example.invalid/rootfs.tar.gz",
		SHA256:    "",
	})
	if err == nil {
		t.Fatal("Provision with an empty SHA256 for a URL reference = nil error, want a refusal")
	}
	if !hasDocAnchor(err, "rootfs-checksum-mismatch") {
		t.Errorf("Provision error = %v, want DocAnchor rootfs-checksum-mismatch", err)
	}
	if len(fetch.calls) != 0 {
		t.Errorf("Provision with an empty SHA256 fetched %d time(s), want zero", len(fetch.calls))
	}
	for _, call := range fake.calls {
		if containsArg(call, "--import") {
			t.Errorf("Provision with an empty SHA256 ran %v, want no --import", call)
		}
	}
}

func TestProvisionRefusesADigestMismatchAndDeletesTheDownload(t *testing.T) {
	setTestDataDirs(t, t.TempDir(), t.TempDir())

	fake := &fakeRunner{results: []fakeResult{
		{stdout: verboseListFixture(), code: 0}, // findRow: absent
	}}
	fetch := &fakeFetcher{write: []byte("not the real rootfs")}
	rt := &wslRuntime{distro: sandboxDistro, run: fake, fetch: fetch}

	err := rt.Provision(context.Background(), shellforgeruntime.ImageSpec{
		Reference: "https://example.invalid/rootfs.tar.gz",
		SHA256:    strings.Repeat("a", 64),
	})
	if err == nil {
		t.Fatal("Provision with a digest mismatch = nil error, want a refusal")
	}
	if !hasDocAnchor(err, "rootfs-checksum-mismatch") {
		t.Errorf("Provision error = %v, want DocAnchor rootfs-checksum-mismatch", err)
	}
	if len(fetch.calls) != 1 {
		t.Fatalf("Provision fetched %d time(s), want exactly 1", len(fetch.calls))
	}
	if _, statErr := os.Stat(fetch.calls[0].dest); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("downloaded file %s still exists after a digest mismatch, want it deleted", fetch.calls[0].dest)
	}
	for _, call := range fake.calls {
		if containsArg(call, "--import") {
			t.Errorf("Provision with a digest mismatch ran %v, want no --import", call)
		}
	}
}

func TestProvisionKeepsALocalRootfsOnADigestMismatch(t *testing.T) {
	tmp := t.TempDir()
	localRootfs := filepath.Join(tmp, "my-rootfs.tar.gz")
	if err := os.WriteFile(localRootfs, []byte("a locally built rootfs"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	fake := &fakeRunner{results: []fakeResult{
		{stdout: verboseListFixture(), code: 0},
	}}
	rt := &wslRuntime{distro: sandboxDistro, run: fake, fetch: &fakeFetcher{}}

	err := rt.Provision(context.Background(), shellforgeruntime.ImageSpec{
		Reference: localRootfs,
		SHA256:    strings.Repeat("b", 64),
	})
	if err == nil {
		t.Fatal("Provision with a local-path digest mismatch = nil error, want a refusal")
	}
	if _, statErr := os.Stat(localRootfs); statErr != nil {
		t.Errorf("local rootfs %s was removed, want it left alone (Shellforge did not create it): %v", localRootfs, statErr)
	}
}

func TestVerifyDigestRefusesAMalformedDigest(t *testing.T) {
	cases := []string{"", "not-hex", strings.Repeat("A", 64), strings.Repeat("a", 63)}
	for _, digest := range cases {
		t.Run(digest, func(t *testing.T) {
			if err := verifyDigest("/this/path/is/never/opened", digest); err == nil {
				t.Fatalf("verifyDigest(path, %q) = nil error, want a refusal before opening anything", digest)
			}
		})
	}
}

func TestProvisionRefusesANameCollisionWithoutTheMarker(t *testing.T) {
	fake := &fakeRunner{results: []fakeResult{
		{stdout: verboseListFixture(verboseRow{name: sandboxDistro, state: "Running", version: 2}), code: 0},
		{code: 1}, // marker read fails
	}}
	rt := &wslRuntime{distro: sandboxDistro, run: fake, fetch: &fakeFetcher{}}

	err := rt.Provision(context.Background(), shellforgeruntime.ImageSpec{})
	if err == nil {
		t.Fatal("Provision with a name collision = nil error, want a refusal")
	}
	if !hasDocAnchor(err, "wsl-name-collision") {
		t.Errorf("Provision error = %v, want DocAnchor wsl-name-collision", err)
	}
	for _, call := range fake.calls {
		if containsArg(call, "--import") {
			t.Errorf("Provision with a name collision ran %v, want no --import", call)
		}
	}
}

func TestProvisionRefusesToReuseAWSL1Distribution(t *testing.T) {
	fake := &fakeRunner{results: []fakeResult{
		{stdout: verboseListFixture(verboseRow{name: sandboxDistro, state: "Stopped", version: 1}), code: 0},
	}}
	rt := &wslRuntime{distro: sandboxDistro, run: fake, fetch: &fakeFetcher{}}

	err := rt.Provision(context.Background(), shellforgeruntime.ImageSpec{})
	if err == nil {
		t.Fatal("Provision reusing a WSL1 distribution = nil error, want a refusal")
	}
	if !hasDocAnchor(err, "wsl-version-1") {
		t.Errorf("Provision error = %v, want DocAnchor wsl-version-1", err)
	}
	for _, call := range fake.calls {
		if containsArg(call, "--import") {
			t.Errorf("Provision reusing a WSL1 distribution ran %v, want no --import", call)
		}
	}
}

// provisionCleanImportResults returns the fake results for a clean import,
// in the order Provision is expected to issue them: enumerate (absent),
// import, write /etc/wsl.conf, read it back matching, terminate, start,
// then the three hardening probes all succeeding.
func provisionCleanImportResults() []fakeResult {
	return []fakeResult{
		{stdout: verboseListFixture(), code: 0}, // 0: findRow, absent
		{code: 0},                               // 1: --import
		{code: 0},                               // 2: write /etc/wsl.conf
		{stdout: []byte(wslConf), code: 0},      // 3: read /etc/wsl.conf back
		{code: 0},                               // 4: --terminate
		{code: 0},                               // 5: start (/bin/true)
		{code: 1},                               // 6: probe /mnt/c absent (test -e exits nonzero)
		{stdout: []byte("/usr/bin:/bin\n"), code: 0}, // 7: probe clean $PATH
		{stdout: []byte("learner\n"), code: 0},       // 8: probe default user
	}
}

func TestProvisionArgvConstruction(t *testing.T) {
	tmp := t.TempDir()
	localRootfs := filepath.Join(tmp, "rootfs.tar.gz")
	if err := os.WriteFile(localRootfs, []byte("a rootfs"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	setTestDataDirs(t, t.TempDir(), t.TempDir())

	fake := &fakeRunner{results: provisionCleanImportResults()}
	rt := &wslRuntime{distro: sandboxDistro, run: fake, fetch: &fakeFetcher{}}

	if err := rt.Provision(context.Background(), shellforgeruntime.ImageSpec{Reference: localRootfs}); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	dir, err := rt.installDir()
	if err != nil {
		t.Fatalf("installDir: %v", err)
	}

	want := [][]string{
		{"wsl.exe", "-l", "-v"},
		{"wsl.exe", "--import", sandboxDistro, dir, localRootfs, "--version", "2"},
		{"wsl.exe", "-d", sandboxDistro, "-u", "root", "--exec", "/bin/sh", "-c", "cat > /etc/wsl.conf"},
		{"wsl.exe", "-d", sandboxDistro, "-u", "root", "--exec", "/bin/cat", "--", "/etc/wsl.conf"},
		{"wsl.exe", "--terminate", sandboxDistro},
		{"wsl.exe", "-d", sandboxDistro, "-u", "root", "--exec", "/bin/true"},
		{"wsl.exe", "-d", sandboxDistro, "-u", "learner", "--exec", "/bin/sh", "-c", "test -e /mnt/c"},
		{"wsl.exe", "-d", sandboxDistro, "-u", "learner", "--exec", "/bin/sh", "-c", "echo $PATH"},
		{"wsl.exe", "-d", sandboxDistro, "-u", "learner", "--exec", "/bin/sh", "-c", "id -un"},
	}
	assertArgvSequence(t, "Provision", fake.calls, want)
}

func TestProvisionTerminatesAfterWritingWslConf(t *testing.T) {
	tmp := t.TempDir()
	localRootfs := filepath.Join(tmp, "rootfs.tar.gz")
	if err := os.WriteFile(localRootfs, []byte("a rootfs"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	setTestDataDirs(t, t.TempDir(), t.TempDir())

	fake := &fakeRunner{results: provisionCleanImportResults()}
	rt := &wslRuntime{distro: sandboxDistro, run: fake, fetch: &fakeFetcher{}}

	if err := rt.Provision(context.Background(), shellforgeruntime.ImageSpec{Reference: localRootfs}); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	writeIdx, terminateIdx, firstProbeIdx := -1, -1, -1
	for i, call := range fake.calls {
		if containsArg(call, "cat > /etc/wsl.conf") {
			writeIdx = i
		}
		if containsArg(call, "--terminate") {
			terminateIdx = i
		}
		if containsArg(call, "test -e /mnt/c") && firstProbeIdx < 0 {
			firstProbeIdx = i
		}
	}
	if writeIdx < 0 || terminateIdx < 0 || firstProbeIdx < 0 {
		t.Fatalf("could not find all three steps in %v", fake.calls)
	}
	if !(writeIdx < terminateIdx && terminateIdx < firstProbeIdx) {
		t.Errorf("order = write:%d terminate:%d firstProbe:%d, want write < terminate < firstProbe", writeIdx, terminateIdx, firstProbeIdx)
	}
}

func TestProvisionRefusesWhenMntCExistsAfterImport(t *testing.T) {
	tmp := t.TempDir()
	localRootfs := filepath.Join(tmp, "rootfs.tar.gz")
	if err := os.WriteFile(localRootfs, []byte("a rootfs"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	setTestDataDirs(t, t.TempDir(), t.TempDir())

	results := provisionCleanImportResults()
	results[6] = fakeResult{code: 0} // /mnt/c probe: test -e succeeds, meaning it exists
	fake := &fakeRunner{results: results}
	rt := &wslRuntime{distro: sandboxDistro, run: fake, fetch: &fakeFetcher{}}

	err := rt.Provision(context.Background(), shellforgeruntime.ImageSpec{Reference: localRootfs})
	if err == nil {
		t.Fatal("Provision with /mnt/c present = nil error, want a refusal")
	}
	if !hasDocAnchor(err, "sandbox-unhealthy") {
		t.Errorf("Provision error = %v, want DocAnchor sandbox-unhealthy", err)
	}
}

func TestProvisionRefusesWhenPathContainsAWindowsElement(t *testing.T) {
	tmp := t.TempDir()
	localRootfs := filepath.Join(tmp, "rootfs.tar.gz")
	if err := os.WriteFile(localRootfs, []byte("a rootfs"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	setTestDataDirs(t, t.TempDir(), t.TempDir())

	results := provisionCleanImportResults()
	results[7] = fakeResult{stdout: []byte("/usr/bin:/mnt/c/Windows/System32\n"), code: 0}
	fake := &fakeRunner{results: results}
	rt := &wslRuntime{distro: sandboxDistro, run: fake, fetch: &fakeFetcher{}}

	err := rt.Provision(context.Background(), shellforgeruntime.ImageSpec{Reference: localRootfs})
	if err == nil {
		t.Fatal("Provision with a /mnt/ element in $PATH = nil error, want a refusal")
	}
	if !hasDocAnchor(err, "sandbox-unhealthy") {
		t.Errorf("Provision error = %v, want DocAnchor sandbox-unhealthy", err)
	}
}

func TestProvisionRefusesWhenTheDefaultUserIsNotLearner(t *testing.T) {
	tmp := t.TempDir()
	localRootfs := filepath.Join(tmp, "rootfs.tar.gz")
	if err := os.WriteFile(localRootfs, []byte("a rootfs"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	setTestDataDirs(t, t.TempDir(), t.TempDir())

	results := provisionCleanImportResults()
	results[8] = fakeResult{stdout: []byte("root\n"), code: 0}
	fake := &fakeRunner{results: results}
	rt := &wslRuntime{distro: sandboxDistro, run: fake, fetch: &fakeFetcher{}}

	err := rt.Provision(context.Background(), shellforgeruntime.ImageSpec{Reference: localRootfs})
	if err == nil {
		t.Fatal("Provision with a non-learner default user = nil error, want a refusal")
	}
	if !hasDocAnchor(err, "sandbox-unhealthy") {
		t.Errorf("Provision error = %v, want DocAnchor sandbox-unhealthy", err)
	}
}

func TestProvisionRefusesWhenWslConfReadbackDiffers(t *testing.T) {
	tmp := t.TempDir()
	localRootfs := filepath.Join(tmp, "rootfs.tar.gz")
	if err := os.WriteFile(localRootfs, []byte("a rootfs"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	setTestDataDirs(t, t.TempDir(), t.TempDir())

	results := provisionCleanImportResults()
	results[3] = fakeResult{stdout: []byte("[automount]\nenabled = true\n"), code: 0}
	fake := &fakeRunner{results: results}
	rt := &wslRuntime{distro: sandboxDistro, run: fake, fetch: &fakeFetcher{}}

	err := rt.Provision(context.Background(), shellforgeruntime.ImageSpec{Reference: localRootfs})
	if err == nil {
		t.Fatal("Provision with a differing wsl.conf readback = nil error, want a refusal")
	}
	if !hasDocAnchor(err, "sandbox-unhealthy") {
		t.Errorf("Provision error = %v, want DocAnchor sandbox-unhealthy", err)
	}
	for _, call := range fake.calls {
		if containsArg(call, "--terminate") {
			t.Errorf("Provision with a bad readback ran %v, want no --terminate", call)
		}
	}
}

func TestProvisionIsIdempotentAgainstAnAlreadyMarkedDistribution(t *testing.T) {
	fake := &fakeRunner{results: []fakeResult{
		{stdout: verboseListFixture(verboseRow{name: sandboxDistro, state: "Running", version: 2}), code: 0}, // findRow
		{stdout: []byte(markerContent), code: 0},     // marker read
		{stdout: []byte(wslConf), code: 0},           // readWslConf: matches
		{code: 1},                                    // probe /mnt/c absent
		{stdout: []byte("/usr/bin:/bin\n"), code: 0}, // probe clean PATH
		{stdout: []byte("learner\n"), code: 0},       // probe default user
	}}
	rt := &wslRuntime{distro: sandboxDistro, run: fake, fetch: &fakeFetcher{}}

	if err := rt.Provision(context.Background(), shellforgeruntime.ImageSpec{}); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	for _, call := range fake.calls {
		if containsArg(call, "--import") {
			t.Errorf("idempotent Provision ran %v, want no --import", call)
		}
		if containsArg(call, "--terminate") {
			t.Errorf("idempotent Provision with a matching config ran %v, want no --terminate", call)
		}
	}
}

// TestProvisionStartsAfterTerminatingAnAlreadyRunningDistribution covers the
// state ensureRunning is handed across a terminate. Before this tracked
// whether the terminate branch actually ran, an already-Running distribution
// whose /etc/wsl.conf needed rewriting was terminated and then told
// "running=true" from the stale row captured before the terminate, so
// ensureRunning skipped starting it back up: Provision only appeared to
// leave the sandbox running because verifyHardening's first probe boots it
// as a side effect.
func TestProvisionStartsAfterTerminatingAnAlreadyRunningDistribution(t *testing.T) {
	fake := &fakeRunner{results: []fakeResult{
		{stdout: verboseListFixture(verboseRow{name: sandboxDistro, state: "Running", version: 2}), code: 0}, // findRow
		{stdout: []byte(markerContent), code: 0},                   // marker read
		{stdout: []byte("[automount]\nenabled = true\n"), code: 0}, // readWslConf: differs
		{code: 0},                          // write /etc/wsl.conf
		{stdout: []byte(wslConf), code: 0}, // readback after write: matches
		{code: 0},                          // terminate
		{code: 0},                          // start (/bin/true): must actually run
		{code: 1},                          // probe /mnt/c absent
		{stdout: []byte("/usr/bin:/bin\n"), code: 0}, // probe clean PATH
		{stdout: []byte("learner\n"), code: 0},       // probe default user
	}}
	rt := &wslRuntime{distro: sandboxDistro, run: fake, fetch: &fakeFetcher{}}

	if err := rt.Provision(context.Background(), shellforgeruntime.ImageSpec{}); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	var sawTerminate, sawStart bool
	terminateIdx, startIdx := -1, -1
	for i, call := range fake.calls {
		if containsArg(call, "--terminate") {
			sawTerminate = true
			terminateIdx = i
		}
		if containsArg(call, "/bin/true") {
			sawStart = true
			startIdx = i
		}
	}
	if !sawTerminate {
		t.Fatalf("Provision with a differing config never ran --terminate: %v", fake.calls)
	}
	if !sawStart {
		t.Errorf("Provision terminated an already-Running distribution but never started it again, want a /bin/true start after the terminate: %v", fake.calls)
	}
	if sawStart && terminateIdx > startIdx {
		t.Errorf("start (index %d) ran before terminate (index %d), want terminate first", startIdx, terminateIdx)
	}
}

func TestCapabilitiesMatchTheTicket(t *testing.T) {
	rt := &wslRuntime{distro: sandboxDistro}
	want := shellforgeruntime.Caps{Networking: true, MultiUser: true}
	got1 := rt.Capabilities()
	got2 := rt.Capabilities()
	if got1 != want {
		t.Errorf("Capabilities() = %+v, want %+v", got1, want)
	}
	if got1 != got2 {
		t.Errorf("Capabilities() is not stable across calls: %+v != %+v", got1, got2)
	}
}

// noDistributionsFriendlyMessageFixture reproduces the shape a real
// `wsl -l -v` prints when zero distributions are registered on the host at
// all: a multi-line, human-readable sentence (localized, so never matched
// by text) on stdout and a non-zero exit code, rather than empty output.
// This is what broke Test (windows-latest) in CI: the first line was
// dropped as the header the same way any table's header is, but the
// remaining two lines each failed to parse as a distribution row, which
// routed the whole call through classifyFailure's default branch and
// surfaced "wsl -l -v exited <code>" as an opaque failure instead of the
// honest "no distributions" answer.
func noDistributionsFriendlyMessageFixture() []byte {
	text := "Windows Subsystem for Linux has no installed distributions.\r\n" +
		"Use 'wsl.exe --list --online' to list available distributions\r\n" +
		"Install a distribution by running 'wsl.exe --install <Distro>'.\r\n"
	return encodeUTF16LE(true, text)
}

func TestStatusTreatsNoDistributionsRegisteredAsNotProvisioned(t *testing.T) {
	fake := &fakeRunner{results: []fakeResult{
		{stdout: noDistributionsFriendlyMessageFixture(), code: 4294967295},
	}}
	rt := &wslRuntime{distro: sandboxDistro, run: fake}

	status, err := rt.Status(context.Background())
	if err != nil {
		t.Fatalf("Status with zero distributions registered on the host = %v, want nil error", err)
	}
	if status.Provisioned {
		t.Errorf("Status = %+v, want Provisioned false", status)
	}
}

// TestFindRowTreatsNoDistributionsRegisteredAsAbsent covers the same defect
// on Provision's own path: a learner's first-ever run, on a Windows host
// that has WSL but has never registered any distribution, would otherwise
// hit the identical failure Status did in CI, and Provision would never
// even reach the import step that is supposed to handle exactly this case.
func TestFindRowTreatsNoDistributionsRegisteredAsAbsent(t *testing.T) {
	fake := &fakeRunner{results: []fakeResult{
		{stdout: noDistributionsFriendlyMessageFixture(), code: 4294967295},
	}}
	rt := &wslRuntime{distro: sandboxDistro, run: fake}

	_, exists, err := rt.findRow(context.Background())
	if err != nil {
		t.Fatalf("findRow with zero distributions registered on the host = %v, want nil error", err)
	}
	if exists {
		t.Errorf("findRow reported the distribution exists, want false")
	}
}

// TestStatusStillReportsAGenuineFailureRatherThanAssumingNoDistributions
// pins the other half of the fix: treating an unparseable non-zero `wsl -l
// -v` exit as "no distributions" only holds when nothing recognizable
// explains the failure. A real, classifiable wsl.exe error must still be
// reported as that error.
func TestStatusStillReportsAGenuineFailureRatherThanAssumingNoDistributions(t *testing.T) {
	fake := &fakeRunner{results: []fakeResult{
		{stderr: []byte("Access is denied. Please update your antivirus exclusions"), code: 4294967295},
	}}
	rt := &wslRuntime{distro: sandboxDistro, run: fake}

	_, err := rt.Status(context.Background())
	if err == nil {
		t.Fatal("Status with a recognizable wsl.exe failure = nil error, want a refusal")
	}
	if !hasDocAnchor(err, "wsl-import-blocked") {
		t.Errorf("Status error = %v, want DocAnchor wsl-import-blocked", err)
	}
}

// TestClassifyFailureDoesNotMisreadAnAntivirusPromptAsKernelOutdated pins
// the narrowed kernel-outdated matcher: the bare words "kernel" and
// "update" alone used to be enough, so "Access is denied. Please update
// your antivirus exclusions" matched the kernel-outdated case first (it was
// listed first in the switch) and told the learner to run `wsl --update`,
// which does nothing for an antivirus block.
func TestClassifyFailureDoesNotMisreadAnAntivirusPromptAsKernelOutdated(t *testing.T) {
	rt := &wslRuntime{distro: sandboxDistro}
	msg := "Access is denied. Please update your antivirus exclusions"
	err := rt.classifyFailure(context.Background(), "import the Windows sandbox", errors.New("boom"), []byte(msg))
	if hasDocAnchor(err, "wsl-kernel-outdated") {
		t.Errorf("classifyFailure(%q) = %v, want no wsl-kernel-outdated anchor", msg, err)
	}
	if !hasDocAnchor(err, "wsl-import-blocked") {
		t.Errorf("classifyFailure(%q) = %v, want DocAnchor wsl-import-blocked", msg, err)
	}
}

func TestStartSessionBeforeProvisionIsSandboxMissing(t *testing.T) {
	fake := &fakeRunner{results: []fakeResult{
		{stdout: verboseListFixture(), code: 0}, // Status: absent
	}}
	rt := &wslRuntime{distro: sandboxDistro, run: fake}

	_, err := rt.StartSession(context.Background(), shellforgeruntime.SessionSpec{})
	if !errors.Is(err, shellforgeruntime.ErrSandboxMissing) {
		t.Errorf("StartSession before Provision: err = %v, want errors.Is(err, runtime.ErrSandboxMissing)", err)
	}
}

func TestWslConfConstantMatchesImagesWslConf(t *testing.T) {
	repoPath, err := repoRootRelative("images/wsl.conf")
	if err != nil {
		t.Fatalf("repoRootRelative: %v", err)
	}
	want, err := os.ReadFile(repoPath)
	if err != nil {
		t.Fatalf("reading %s: %v", repoPath, err)
	}
	if normalizeCRLF(wslConf) != normalizeCRLF(string(want)) {
		t.Errorf("the wslConf constant does not match %s after CRLF normalisation:\n--- wslConf ---\n%s\n--- images/wsl.conf ---\n%s", repoPath, wslConf, want)
	}
}

// hasDocAnchor reports whether err is (or wraps) a *ux.Error carrying the
// given DocAnchor.
func hasDocAnchor(err error, anchor string) bool {
	var uxErr *ux.Error
	if !errors.As(err, &uxErr) {
		return false
	}
	return uxErr.DocAnchor == anchor
}

func containsArg(argv []string, needle string) bool {
	for _, a := range argv {
		if strings.Contains(a, needle) {
			return true
		}
	}
	return false
}
