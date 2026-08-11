package platform

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestWindowsCacheDirIsDistinctFromDataDir guards the collision the cache
// element exists to prevent. os.UserCacheDir returns %LocalAppData% on Windows,
// the same base DataDir builds on, so without a distinct element CacheDir and
// DataDir would be the identical directory and a cache clear would delete the
// progress database and the WSL .vhdx. This exercises the resolution logic on
// every platform, not only behind a Windows build, so a regression is caught in
// Linux CI.
func TestWindowsCacheDirIsDistinctFromDataDir(t *testing.T) {
	base := filepath.Join(t.TempDir(), "LocalAppData")
	cache := windowsCacheDir(base)
	data := windowsDataDir(base)

	if cache == data {
		t.Fatalf("windows cache and data dirs are identical: %q", cache)
	}
	// Cache must nest below data, never the reverse, so clearing the cache
	// cannot reach up into the progress database or the WSL store.
	if !strings.HasPrefix(cache, data+string(filepath.Separator)) {
		t.Errorf("windows cache dir %q is not nested below data dir %q", cache, data)
	}
}

// TestDirsAreDistinct asserts the three writable roots never collide on the
// platform under test, so a future collision of any pair is caught. The XDG
// variables make the layout deterministic on Linux and macOS; on Windows they
// are ignored and the AppData/LocalAppData split plus the cache element keep the
// three apart.
func TestDirsAreDistinct(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))

	dirs := map[string]string{}
	for name, fn := range map[string]func() (string, error){
		"config": ConfigDir,
		"cache":  CacheDir,
		"data":   DataDir,
	} {
		d, err := fn()
		if err != nil {
			t.Fatalf("%s dir: %v", name, err)
		}
		if !filepath.IsAbs(d) {
			t.Errorf("%s dir %q is not absolute", name, d)
		}
		dirs[name] = d
	}

	for aName, a := range dirs {
		for bName, b := range dirs {
			if aName < bName && a == b {
				t.Errorf("%s and %s resolve to the same directory %q", aName, bName, a)
			}
		}
	}
}

// TestDatabasePathIsNotUnderCacheDir is the direct statement of the safety
// property: whoever writes cache clearing must not be able to delete the
// progress database with it.
func TestDatabasePathIsNotUnderCacheDir(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))

	cache, err := CacheDir()
	if err != nil {
		t.Fatalf("CacheDir: %v", err)
	}
	db, err := DatabasePath()
	if err != nil {
		t.Fatalf("DatabasePath: %v", err)
	}

	if db == cache || strings.HasPrefix(db, cache+string(filepath.Separator)) {
		t.Errorf("database path %q sits under cache dir %q; a cache clear would delete progress", db, cache)
	}
	if filepath.Base(db) != "progress.db" {
		t.Errorf("database path %q does not end in progress.db", db)
	}
}

// TestLogDirIsUnderCacheDir pins the intended containment: logs are cache, and a
// cache clear is allowed to take them.
func TestLogDirIsUnderCacheDir(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))

	cache, err := CacheDir()
	if err != nil {
		t.Fatalf("CacheDir: %v", err)
	}
	logs, err := LogDir()
	if err != nil {
		t.Fatalf("LogDir: %v", err)
	}
	if !strings.HasPrefix(logs, cache+string(filepath.Separator)) {
		t.Errorf("log dir %q is not under cache dir %q", logs, cache)
	}
}

// TestDataDirRejectsRelativeXDG matches the standard library: os.UserConfigDir
// and os.UserCacheDir both refuse a relative XDG variable, so DataDir does too
// rather than resolving a data directory relative to the working directory.
func TestDataDirRejectsRelativeXDG(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("XDG_DATA_HOME is not consulted on windows")
	}
	t.Setenv("XDG_DATA_HOME", filepath.Join("relative", "data"))

	if _, err := DataDir(); err == nil {
		t.Fatal("DataDir accepted a relative XDG_DATA_HOME; expected an error")
	}
}

// TestWindowsDataDirDistinctFromCacheDirLive runs the real functions and is the
// Windows-guarded companion to TestWindowsCacheDirIsDistinctFromDataDir: it
// proves the wiring, not only the helper, on the platform that actually
// collided.
func TestWindowsDataDirDistinctFromCacheDirLive(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-specific layout")
	}
	cache, err := CacheDir()
	if err != nil {
		t.Fatalf("CacheDir: %v", err)
	}
	data, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir: %v", err)
	}
	if cache == data {
		t.Fatalf("on windows CacheDir and DataDir resolve to the same directory: %q", cache)
	}
}
