// Package platform holds operating system probes, console mode handling, and
// the resolution of the directories Shellforge is allowed to write to.
//
// Layer L0. It may import internal/platform/ux and nothing else from this
// module.
//
// Nothing in Shellforge writes outside the three directories returned here.
// Uninstall depends on that being true, and an orphaned two gigabyte .vhdx is
// the single most complained-about failure mode of tools in this category.
package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
)

// appDir is the per-user subdirectory name used under every base directory.
const appDir = "shellforge"

// ConfigDir returns the directory holding config.toml.
//
//	Linux, macOS: $XDG_CONFIG_HOME/shellforge, else ~/.config/shellforge
//	Windows:      %AppData%\shellforge
func ConfigDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, appDir), nil
}

// CacheDir returns the directory holding downloaded artifacts and logs.
//
//	Linux, macOS: $XDG_CACHE_HOME/shellforge, else ~/.cache/shellforge
//	Windows:      %LocalAppData%\shellforge
func CacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, appDir), nil
}

// DataDir returns the directory holding the progress database and the sandbox
// install directory.
//
//	Linux, macOS: $XDG_DATA_HOME/shellforge, else ~/.local/share/shellforge
//	Windows:      %LocalAppData%\shellforge
//
// The standard library has no UserDataDir, so this is resolved by hand. On
// Windows os.UserCacheDir already returns %LocalAppData%, which is also the
// correct base for application data, so DataDir and CacheDir resolve to the
// identical directory there. That is a known defect tracked by issue #40: do
// not write a cache-clearing operation against CacheDir on Windows until the
// two are separated.
//
// A relative XDG_DATA_HOME is an error, matching os.UserConfigDir and
// os.UserCacheDir, which both refuse a relative value for their own XDG
// variables. The check is absoluteness only. It does not resolve symlinks and
// it does not reject a ".." segment, because filepath.Join cleans those, so
// XDG_DATA_HOME=/home/user/../../etc yields /etc/shellforge with no error.
func DataDir() (string, error) {
	if runtime.GOOS == "windows" {
		base, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(base, appDir), nil
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		if !filepath.IsAbs(xdg) {
			return "", ux.Fail(
				"resolve the data directory",
				fmt.Errorf("$XDG_DATA_HOME is relative: %q", xdg),
				"Set XDG_DATA_HOME to an absolute path, or unset it to use ~/.local/share.",
				"",
			)
		}
		return filepath.Join(xdg, appDir), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", appDir), nil
}

// LogDir returns the directory holding rotated debug logs.
func LogDir() (string, error) {
	base, err := CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "logs"), nil
}

// DatabasePath returns the full path to the progress database.
func DatabasePath() (string, error) {
	base, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "progress.db"), nil
}

// EnsureDir creates dir and its parents if they do not exist.
//
// Mode 0o700 is deliberate: the progress database records every command the
// learner typed, and people type passwords into shells.
func EnsureDir(dir string) error {
	return os.MkdirAll(dir, 0o700)
}
