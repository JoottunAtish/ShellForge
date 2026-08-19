package wsl

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JoottunAtish/ShellForge/internal/platform"
	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

// defaultRootfsRel is where `make rootfs` writes its output, relative to
// the repository root.
const defaultRootfsRel = "images/out/rootfs.tar.gz"

// repoRootRelative resolves rel against the repository root rather than
// the process's current working directory, by walking up from the working
// directory to the nearest go.mod. `go test` runs a package's tests with
// the working directory set to that package's own directory, not the
// repository root, so a plain relative "images/out/rootfs.tar.gz" resolves
// to nothing under `go test ./internal/runtime/wsl/...`. This mirrors
// internal/runtime/docker's repoRootRelative; it is not shared with that
// package, because cross-importing a sibling backend is forbidden by the
// layer rule.
func repoRootRelative(rel string) (string, error) {
	if _, err := os.Stat(rel); err == nil {
		abs, err := filepath.Abs(rel)
		return abs, err
	}

	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("wsl: find the repository root: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, rel), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("wsl: could not find %s: no go.mod between %s and the filesystem root", rel, dir)
		}
		dir = parent
	}
}

// resolvedRootfs is a rootfs tarball Provision is about to import.
// downloaded is true only when this package fetched the file itself,
// which is the only case a digest mismatch may delete it: deleting a
// developer's own `make rootfs` output, or a caller-supplied local path,
// is itself a destructive act on a file Shellforge did not create.
type resolvedRootfs struct {
	path       string
	downloaded bool
}

// downloadDest is where an https Reference is downloaded to.
func downloadDest() (string, error) {
	cacheDir, err := platform.CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cacheDir, "rootfs", "rootfs.tar.gz"), nil
}

// defaultRootfs finds the backend's own default rootfs when
// ImageSpec.Reference is empty: first the repository's own
// images/out/rootfs.tar.gz, which is what a developer who ran `make
// rootfs` has, then <CacheDir>/rootfs.tar.gz, which is where a future
// installer places a downloaded artifact. Neither existing is a refusal
// with the rootfs-not-found doc anchor, not a panic or a silent empty
// import.
func defaultRootfs() (resolvedRootfs, error) {
	repoPath, repoErr := repoRootRelative(defaultRootfsRel)
	if repoErr == nil {
		if _, err := os.Stat(repoPath); err == nil {
			return resolvedRootfs{path: repoPath}, nil
		}
	}

	cacheDir, err := platform.CacheDir()
	if err != nil {
		return resolvedRootfs{}, err
	}
	cachedPath := filepath.Join(cacheDir, "rootfs.tar.gz")
	if _, err := os.Stat(cachedPath); err == nil {
		return resolvedRootfs{path: cachedPath}, nil
	}

	return resolvedRootfs{}, ux.Fail(
		"find the sandbox rootfs",
		fmt.Errorf("no rootfs tarball at %s or %s", defaultRootfsRel, cachedPath),
		"run `make rootfs` to build one, then run `shellforge init` again",
		"rootfs-not-found",
	)
}

// resolveRootfs decides what file Provision imports from, without
// verifying its digest: an empty Reference means the backend default
// (defaultRootfs), an https URL is downloaded to downloadDest through
// fetch, and anything else is used as a local absolute path exactly as
// given. Any other URL scheme is refused outright.
func resolveRootfs(ctx context.Context, fetch fetcher, spec runtime.ImageSpec) (resolvedRootfs, error) {
	ref := spec.Reference
	switch {
	case ref == "":
		return defaultRootfs()
	case strings.HasPrefix(ref, "https://"):
		if spec.SHA256 == "" {
			return resolvedRootfs{}, ux.Fail(
				"verify the sandbox rootfs",
				fmt.Errorf("ImageSpec.SHA256 is empty for a downloaded rootfs (%s)", ref),
				"pin ImageSpec.SHA256 to the published sha256 digest before downloading a rootfs",
				"rootfs-checksum-mismatch",
			)
		}
		dest, err := downloadDest()
		if err != nil {
			return resolvedRootfs{}, err
		}
		if err := platform.EnsureDir(filepath.Dir(dest)); err != nil {
			return resolvedRootfs{}, fmt.Errorf("wsl: create download directory: %w", err)
		}
		if err := fetch.fetch(ctx, ref, dest); err != nil {
			return resolvedRootfs{}, fmt.Errorf("wsl: download rootfs from %s: %w", ref, err)
		}
		return resolvedRootfs{path: dest, downloaded: true}, nil
	case strings.Contains(ref, "://"):
		return resolvedRootfs{}, fmt.Errorf("wsl: refusing to fetch rootfs from %q: only https is supported", ref)
	default:
		return resolvedRootfs{path: ref}, nil
	}
}

// wslBackingFiles are the only entries an install directory is allowed to
// hold. Anything else is refused rather than removed, on the theory that an
// install directory containing something unexpected might not be a WSL
// backing store this package created, or might hold something the removal
// path did not anticipate.
var wslBackingFiles = map[string]bool{
	"ext4.vhdx": true,
	"nvme.vhdx": true,
	"tmp.vhdx":  true,
}

// installDirFor returns the directory a distribution's WSL backing store
// lives in, given a resolved DataDir. It is the pure form paths_test.go
// drives against a t.TempDir(), so the refusal table runs on both OS legs
// of CI without touching the process environment.
func installDirFor(dataDir, distro string) string {
	return filepath.Join(dataDir, "wsl", distro)
}

// installDir returns the directory rt's WSL backing store lives in,
// deriving its value from platform.DataDir() and rt.distro and from
// nothing else.
func (rt *wslRuntime) installDir() (string, error) {
	dataDir, err := platform.DataDir()
	if err != nil {
		return "", err
	}
	return installDirFor(dataDir, rt.distro), nil
}

// validateInstallDir resolves platform.DataDir() and delegates to
// validateInstallDirUnder. It is the only form non-test code calls.
func validateInstallDir(dir string) error {
	dataDir, err := platform.DataDir()
	if err != nil {
		return err
	}
	return validateInstallDirUnder(dataDir, dir)
}

// validateInstallDirUnder reports why dir must not be used as a WSL install
// directory rooted at dataDir, or nil when it is safe. It refuses, in this
// order: empty, a ".." segment (checked before any cleaning, because
// cleaning is exactly what would hide it), not absolute after cleaning,
// equal to dataDir itself (which holds the progress database), not
// strictly under dataDir, then a symlink whose resolved target escapes
// dataDir. Refuse, never adjust: destructive-safety's rule for anything
// bound for a host-side os.RemoveAll.
func validateInstallDirUnder(dataDir, dir string) error {
	if strings.TrimSpace(dir) == "" {
		return fmt.Errorf("wsl: refusing to use an install directory: the path is empty")
	}
	for _, segment := range strings.Split(filepath.ToSlash(dir), "/") {
		if segment == ".." {
			return fmt.Errorf("wsl: refusing to use install directory %q: it contains a .. segment", dir)
		}
	}

	clean := filepath.Clean(dir)
	if !filepath.IsAbs(clean) {
		return fmt.Errorf("wsl: refusing to use install directory %q: it is not absolute", dir)
	}

	cleanDataDir := filepath.Clean(dataDir)
	if clean == cleanDataDir {
		return fmt.Errorf("wsl: refusing to use install directory %q: it is the data directory root itself, which holds the progress database", dir)
	}
	if !strings.HasPrefix(clean, cleanDataDir+string(filepath.Separator)) {
		return fmt.Errorf("wsl: refusing to use install directory %q: it is not strictly under the data directory %q", dir, dataDir)
	}

	if err := validateNoSymlinkEscape(clean, cleanDataDir); err != nil {
		return err
	}
	return nil
}

// validateNoSymlinkEscape resolves symlinks on the deepest existing
// ancestor of clean and re-checks the prefix, so a symlink placed inside
// dataDir that points outside it is refused even though the unresolved
// path looked fine. A path with no existing ancestor yet (the common case
// before the first Provision creates it) has nothing to resolve and is not
// refused by this step.
func validateNoSymlinkEscape(clean, cleanDataDir string) error {
	ancestor := clean
	for {
		if _, err := os.Lstat(ancestor); err == nil {
			break
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return nil
		}
		ancestor = parent
	}

	resolved, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return fmt.Errorf("wsl: refusing to use install directory: resolving symlinks on %q: %w", ancestor, err)
	}
	// Rebuild the full candidate path using the resolved ancestor plus
	// whatever suffix of clean was still below it.
	suffix := strings.TrimPrefix(clean, ancestor)
	resolvedFull := filepath.Clean(resolved + suffix)

	resolvedDataDir, err := filepath.EvalSymlinks(cleanDataDir)
	if err != nil {
		// The data directory itself does not exist yet: nothing to
		// resolve, and the unresolved prefix check already passed.
		return nil
	}
	if resolvedFull != resolvedDataDir && !strings.HasPrefix(resolvedFull, resolvedDataDir+string(filepath.Separator)) {
		return fmt.Errorf("wsl: refusing to use install directory: it resolves to %q, outside the data directory %q", resolvedFull, resolvedDataDir)
	}
	return nil
}

// refuseUnexpectedInstallDirContents reports an error when dir holds an
// entry that is not a known WSL backing file, rather than silently deleting
// something removeInstallDir did not anticipate. A directory that does not
// exist yet has nothing to refuse.
func refuseUnexpectedInstallDirContents(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("wsl: read install directory %s: %w", dir, err)
	}
	for _, e := range entries {
		if !wslBackingFiles[e.Name()] {
			return fmt.Errorf("wsl: refusing to remove install directory %s: it contains %q, which is not a known WSL backing file", dir, e.Name())
		}
	}
	return nil
}

// removeInstallDir removes dir after validating it. This is the only
// os.RemoveAll in this package, per destructive-safety's "no os.RemoveAll
// outside the validated helper".
func removeInstallDir(dir string) error {
	if err := validateInstallDir(dir); err != nil {
		return err
	}
	return os.RemoveAll(dir)
}
