package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// sandboxPaths names every base directory a sandboxEnv points at, so a test
// can assert containment against the exact base it configured instead of
// reconstructing filepath.Join calls that would only agree with themselves.
type sandboxPaths struct {
	root         string
	home         string
	localAppData string
}

// sandboxEnv points every environment variable the platform package reads at
// a fresh t.TempDir root and returns that root plus the subdirectories tests
// need by name. It exists for three reasons:
//
//   - Hermeticity. A CI runner with no HOME would otherwise fail the table
//     test in TestDirectoryFunctionsReturnAbsoluteAppPaths for a reason that
//     has nothing to do with the code under test.
//   - Safety. Even a mistaken EnsureDir call on a computed directory lands
//     inside t.TempDir, never in the developer's real
//     ~/.local/share/shellforge.
//   - macOS coverage. os.UserConfigDir on macOS ignores XDG_CONFIG_HOME and
//     reads HOME, so both must be set for every result to land under the
//     same root regardless of GOOS.
//
// t.Setenv forbids t.Parallel on any test that calls this, which is correct:
// these functions read process environment.
func sandboxEnv(t *testing.T) sandboxPaths {
	t.Helper()
	root := t.TempDir()
	p := sandboxPaths{
		root:         root,
		home:         root,
		localAppData: filepath.Join(root, "local"),
	}
	t.Setenv("HOME", p.home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("AppData", filepath.Join(root, "roaming"))
	t.Setenv("LocalAppData", p.localAppData)
	return p
}

// assertUnder fails the test unless child is strictly inside parent. The
// acceptance criteria for this package ask for containment, not string
// equality, so this checks path element boundaries with filepath.Rel rather
// than a raw prefix compare: strings.HasPrefix would wrongly accept
// "/a/bc" as under "/a/b", and it does not account for Windows separators.
func assertUnder(t *testing.T, parent, child string) {
	t.Helper()
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		t.Fatalf("filepath.Rel(%q, %q) error = %v, want nil", parent, child, err)
	}
	if rel == "." {
		t.Fatalf("%q is not under %q: they are the same path", child, parent)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatalf("%q is not under %q: relative path is %q", child, parent, rel)
	}
}

// TestDirectoryFunctionsReturnAbsoluteAppPaths covers ConfigDir, CacheDir,
// and DataDir together: each must return a non-empty absolute path ending in
// the shellforge application directory, contained under the environment root
// the test configured.
//
// LogDir is deliberately not in this table. Its final element is "logs", not
// "shellforge", by design: see TestLogDirIsUnderCacheDir.
func TestDirectoryFunctionsReturnAbsoluteAppPaths(t *testing.T) {
	tests := []struct {
		name string
		fn   func() (string, error)
	}{
		{"ConfigDir", ConfigDir},
		{"CacheDir", CacheDir},
		{"DataDir", DataDir},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := sandboxEnv(t)

			got, err := tt.fn()
			if err != nil {
				t.Fatalf("%s() error = %v, want nil", tt.name, err)
			}
			if got == "" {
				t.Fatalf("%s() = \"\", want a non-empty path", tt.name)
			}
			if !filepath.IsAbs(got) {
				t.Errorf("%s() = %q, want an absolute path", tt.name, got)
			}
			if base := filepath.Base(got); base != "shellforge" {
				t.Errorf("filepath.Base(%s()) = %q, want %q", tt.name, base, "shellforge")
			}
			assertUnder(t, env.root, got)
		})
	}
}

// TestLogDirIsUnderCacheDir pins the contract raised against this ticket: the
// acceptance criteria ask both for "ends in the shellforge directory" and for
// "LogDir resolves under CacheDir", and LogDir cannot satisfy both literally
// because its final element is "logs". The resolution is containment, not
// equality: LogDir lives at CacheDir()/logs. If a future change makes LogDir
// end in "shellforge" instead, to satisfy the literal wording of the other
// criterion, this test fails and forces that conversation instead of a silent
// relocation of the log directory.
func TestLogDirIsUnderCacheDir(t *testing.T) {
	sandboxEnv(t)

	cache, err := CacheDir()
	if err != nil {
		t.Fatalf("CacheDir() error = %v, want nil", err)
	}
	log, err := LogDir()
	if err != nil {
		t.Fatalf("LogDir() error = %v, want nil", err)
	}

	if !filepath.IsAbs(log) {
		t.Errorf("LogDir() = %q, want an absolute path", log)
	}
	assertUnder(t, cache, log)
	if base := filepath.Base(log); base != "logs" {
		t.Errorf("filepath.Base(LogDir()) = %q, want %q", base, "logs")
	}
}

// TestDatabasePathIsUnderDataDir pins the filename contract the store package
// and the Day 6 uninstall work both depend on.
func TestDatabasePathIsUnderDataDir(t *testing.T) {
	sandboxEnv(t)

	data, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir() error = %v, want nil", err)
	}
	db, err := DatabasePath()
	if err != nil {
		t.Fatalf("DatabasePath() error = %v, want nil", err)
	}

	if !filepath.IsAbs(db) {
		t.Errorf("DatabasePath() = %q, want an absolute path", db)
	}
	assertUnder(t, data, db)
	if base := filepath.Base(db); base != "progress.db" {
		t.Errorf("filepath.Base(DatabasePath()) = %q, want %q", base, "progress.db")
	}
}

// TestDataDirHonoursXDGDataHome covers both branches of DataDir with an
// assertion, not a skip. On non-Windows, DataDir must honour XDG_DATA_HOME.
// On Windows, DataDir never reads XDG_DATA_HOME at all, so the test sets it
// to a path outside LocalAppData and asserts DataDir ignores it, which pins
// the documented Windows contract instead of reporting nothing for that
// platform.
func TestDataDirHonoursXDGDataHome(t *testing.T) {
	env := sandboxEnv(t)
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)

	got, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir() error = %v, want nil", err)
	}

	if runtime.GOOS == "windows" {
		assertUnder(t, env.localAppData, got)
		return
	}

	assertUnder(t, xdg, got)
	if base := filepath.Base(got); base != "shellforge" {
		t.Errorf("filepath.Base(DataDir()) = %q, want %q", base, "shellforge")
	}
}

// TestDataDirFallsBackToHome covers the non-Windows fallback branch: when
// XDG_DATA_HOME is unset or empty, DataDir must resolve under the home
// directory instead of returning an empty or relative path.
//
// The empty case is not the bug the ticket predicted: os.Getenv returns ""
// for a variable that was never set, and DataDir's guard is `xdg != ""`, so
// the unset and empty cases already take the same code path today and both
// already produce an absolute path under $HOME. This test's value is
// regression pinning: the natural refactor to os.LookupEnv would silently
// break the empty case, and this test would catch that.
func TestDataDirFallsBackToHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("DataDir on Windows has no fallback branch; see TestDataDirHonoursXDGDataHome")
	}

	tests := []struct {
		name  string
		setup func(t *testing.T)
	}{
		{
			name: "unset",
			setup: func(t *testing.T) {
				// t.Setenv records whatever was set before this call and
				// restores it at cleanup regardless of the Unsetenv below,
				// so this is a safe, idiomatic scoped unset.
				t.Setenv("XDG_DATA_HOME", "placeholder")
				os.Unsetenv("XDG_DATA_HOME")
			},
		},
		{
			name: "empty",
			setup: func(t *testing.T) {
				t.Setenv("XDG_DATA_HOME", "")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := sandboxEnv(t)
			tt.setup(t)

			got, err := DataDir()
			if err != nil {
				t.Fatalf("DataDir() error = %v, want nil", err)
			}
			if !filepath.IsAbs(got) {
				t.Errorf("DataDir() = %q, want an absolute path", got)
			}
			assertUnder(t, env.home, got)
			if base := filepath.Base(got); base != "shellforge" {
				t.Errorf("filepath.Base(DataDir()) = %q, want %q", base, "shellforge")
			}
		})
	}
}

// TestDataDirRejectsRelativeXDGDataHome is the one genuinely red test in this
// file. DataDir hand-rolls its XDG_DATA_HOME branch and, before the fix in
// this change, never checks filepath.IsAbs: it returns
// ("relative/data/shellforge", nil) for a relative value. ConfigDir and
// CacheDir delegate to os.UserConfigDir and os.UserCacheDir, both of which
// return an error for a relative XDG value, so the same misconfiguration was
// refused by two of the three base functions and silently accepted by the
// third. A relative DataDir also re-resolves against the process working
// directory on every run, which is exactly the class of bug that produces an
// orphaned directory that uninstall cannot find.
func TestDataDirRejectsRelativeXDGDataHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("DataDir on Windows never reads XDG_DATA_HOME, so a relative value cannot reach it")
	}

	tests := []struct {
		name string
		xdg  string
	}{
		{"bare relative", "relative/data"},
		{"dot relative", "./relative/data"},
		{"parent traversal", "../data"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sandboxEnv(t)
			t.Setenv("XDG_DATA_HOME", tt.xdg)

			got, err := DataDir()
			if err == nil {
				t.Errorf("DataDir() with XDG_DATA_HOME = %q: got err = nil, want non-nil", tt.xdg)
			}
			if got != "" {
				t.Errorf("DataDir() with XDG_DATA_HOME = %q: got path = %q, want empty", tt.xdg, got)
			}
		})
	}
}

// TestEnsureDirCreatesNestedPathAndIsIdempotent pins two properties a future
// implementer could plausibly break by hand-rolling a single os.Mkdir
// instead of os.MkdirAll: parent creation, and no error on an existing
// directory.
func TestEnsureDirCreatesNestedPathAndIsIdempotent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b", "c")

	if err := EnsureDir(dir); err != nil {
		t.Fatalf("EnsureDir(%q) error = %v, want nil", dir, err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("os.Stat(%q) error = %v, want nil", dir, err)
	}
	if !info.IsDir() {
		t.Errorf("os.Stat(%q).IsDir() = false, want true", dir)
	}

	if err := EnsureDir(dir); err != nil {
		t.Fatalf("EnsureDir(%q) second call error = %v, want nil", dir, err)
	}
	info, err = os.Stat(dir)
	if err != nil {
		t.Fatalf("os.Stat(%q) after second call error = %v, want nil", dir, err)
	}
	if !info.IsDir() {
		t.Errorf("os.Stat(%q).IsDir() after second call = false, want true", dir)
	}
}

// TestEnsureDirUsesRestrictivePermissions pins mode 0700 on both the leaf
// directory and one intermediate directory, because the progress database
// records every command the learner typed and a permissive intermediate
// directory leaks the listing that names it.
//
// os.Stat().Mode().Perm() does not report POSIX bits meaningfully on
// Windows, so the permission comparison is guarded to non-Windows, matching
// the acceptance criterion that platform-specific assertions be guarded
// rather than causing the whole test to be skipped. Windows still gets the
// creation coverage above the guard.
//
// os.MkdirAll masks the requested mode with the process umask. The common
// umasks (022, 002, 077) clear no owner bits, so 0700 survives all of them
// and strict equality is safe here. If a runner ever appears with an exotic
// umask that clears an owner bit, the correct fix is to relax this to
// perm&0o077 == 0, which is the actual security promise of no group and no
// other access, and not to delete the test.
func TestEnsureDirUsesRestrictivePermissions(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "a", "b", "c")

	if err := EnsureDir(dir); err != nil {
		t.Fatalf("EnsureDir(%q) error = %v, want nil", dir, err)
	}

	if runtime.GOOS == "windows" {
		return
	}

	for _, p := range []string{dir, filepath.Join(root, "a", "b")} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("os.Stat(%q) error = %v, want nil", p, err)
		}
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Errorf("os.Stat(%q).Mode().Perm() = %#o, want %#o", p, perm, 0o700)
		}
	}
}

// TestWindowsDataDirMatchesCacheDir characterises a defect rather than a
// desired contract: on Windows, DataDir and CacheDir resolve to the
// identical directory, because both are filepath.Join(os.UserCacheDir(),
// "shellforge") and os.UserCacheDir on Windows returns %LocalAppData%. That
// means the progress database at DataDir()/progress.db lives inside what the
// rest of the codebase calls the cache directory, so the first "clear the
// cache" operation written against CacheDir would delete the learner's
// progress along with it.
//
// This is pinned rather than fixed here: separating the two directories adds
// a path element to CacheDir on Windows, which relocates a directory, and
// this ticket's own scope excludes relocating anything. See the follow-up
// filed against the Day 6 uninstall work. Fixing the collision requires
// deleting this test deliberately, which is the point of writing it this
// way instead of leaving the collision undocumented.
func TestWindowsDataDirMatchesCacheDir(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("this pins a Windows-only path collision between DataDir and CacheDir")
	}
	sandboxEnv(t)

	cache, err := CacheDir()
	if err != nil {
		t.Fatalf("CacheDir() error = %v, want nil", err)
	}
	data, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir() error = %v, want nil", err)
	}
	if cache != data {
		t.Errorf("CacheDir() = %q, DataDir() = %q, want identical on Windows", cache, data)
	}
}
