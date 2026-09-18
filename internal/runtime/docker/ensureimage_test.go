package docker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/platform"
	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
)

// noRepoAbove puts the test in a directory with no go.mod above it and an
// empty cache directory, which is what a machine holding only the released
// binary looks like. It returns the cache path a rootfs would live at.
//
// It skips rather than fails if a Containerfile is still resolvable from
// there, because a test that silently exercised the build branch while
// claiming to exercise the import branch would be worse than no test.
func noRepoAbove(t *testing.T) (rootfsPath string) {
	t.Helper()

	cache := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", cache)
	} else {
		t.Setenv("XDG_CACHE_HOME", cache)
	}
	t.Chdir(t.TempDir())

	if _, _, ok := repoContainerfile(); ok {
		t.Skip("skipping: a Containerfile is still resolvable from the temporary working directory, so the no-clone branches cannot be exercised here")
	}

	path, err := platform.RootfsCachePath()
	if err != nil {
		t.Fatalf("RootfsCachePath: %v", err)
	}
	return path
}

// TestEnsureImageDoesNothingWhenTheImagePresent pins the first rung of the
// ladder. It matters beyond saving a build: `docker import` mints a fresh
// image id every time, and containerIsStale compares the container's
// recorded image id against the current one, so an import that ran on every
// Provision would recreate the learner's container on every Provision.
func TestEnsureImageDoesNothingWhenTheImageIsPresent(t *testing.T) {
	fake := &fakeRunner{results: []fakeResult{{code: 0}}}
	rt := &dockerRuntime{name: "shellforge-sandbox", image: "shellforge-sandbox", run: fake}

	if err := rt.ensureImage(context.Background(), "shellforge-sandbox"); err != nil {
		t.Fatalf("ensureImage: %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("calls = %d (%v), want exactly the one inspect", len(fake.calls), fake.calls)
	}
	if got := strings.Join(fake.calls[0], " "); !strings.Contains(got, "image inspect") {
		t.Errorf("first call = %q, want the image inspect", got)
	}
}

// TestEnsureImageBuildsFromTheRepositoryWhenThereIsOne pins the developer's
// path. Issue #172's acceptance criteria are explicit that a developer
// working in a clone sees no change, so the repository must win over a
// cached tarball rather than the other way round.
func TestEnsureImageBuildsFromTheRepositoryWhenThereIsOne(t *testing.T) {
	if _, _, ok := repoContainerfile(); !ok {
		t.Skip("skipping: no images/Containerfile resolvable from this working directory")
	}

	fake := &fakeRunner{results: []fakeResult{{code: 1}, {code: 0}}}
	rt := &dockerRuntime{name: "shellforge-sandbox", image: "shellforge-sandbox", run: fake}

	if err := rt.ensureImage(context.Background(), "shellforge-sandbox"); err != nil {
		t.Fatalf("ensureImage: %v", err)
	}
	if len(fake.calls) != 2 {
		t.Fatalf("calls = %d (%v), want the inspect plus one build", len(fake.calls), fake.calls)
	}
	got := fake.calls[1]
	if got[1] != "build" {
		t.Errorf("second call = %v, want a docker build", got)
	}
	if !strings.HasSuffix(got[3], filepath.FromSlash(containerfilePath)) {
		t.Errorf("build -f = %q, want it to end in %q", got[3], containerfilePath)
	}
}

// TestEnsureImageImportsTheCachedRootfsWithoutAClone is the one that closes
// issue #172 on Linux: no repository anywhere, a verified tarball in the
// cache, and an image at the end of it.
func TestEnsureImageImportsTheCachedRootfsWithoutAClone(t *testing.T) {
	rootfs := noRepoAbove(t)
	if err := platform.EnsureDir(filepath.Dir(rootfs)); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
	if err := os.WriteFile(rootfs, []byte("a verified rootfs"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	fake := &fakeRunner{results: []fakeResult{{code: 1}, {code: 0}}}
	rt := &dockerRuntime{name: "shellforge-sandbox", image: "shellforge-sandbox", run: fake}

	if err := rt.ensureImage(context.Background(), "shellforge-sandbox"); err != nil {
		t.Fatalf("ensureImage: %v", err)
	}
	if len(fake.calls) != 2 {
		t.Fatalf("calls = %d (%v), want the inspect plus one import", len(fake.calls), fake.calls)
	}

	got := fake.calls[1]
	if got[1] != "import" {
		t.Fatalf("second call = %v, want a docker import", got)
	}
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, rootfs) {
		t.Errorf("import argv = %v, want it to name the cached rootfs %q", got, rootfs)
	}

	// The --change flags are the whole reason this is not a one-line
	// call. docker export discards image configuration, so without them
	// the imported sandbox has no locale and no timezone and stops
	// matching the one `docker build` produces.
	for _, kv := range sandboxImageEnv() {
		if !strings.Contains(joined, "--change ENV "+kv) {
			t.Errorf("import argv = %v, want it to carry --change ENV %s", got, kv)
		}
	}
}

// TestEnsureImageRefusesWhenThereIsNoSourceAtAll pins the bottom rung. The
// old code returned repoRootRelative's bare error here, with no remediation
// and no doc anchor, which non-negotiable rule 6 forbids.
func TestEnsureImageRefusesWhenThereIsNoSourceAtAll(t *testing.T) {
	rootfs := noRepoAbove(t)

	fake := &fakeRunner{results: []fakeResult{{code: 1}}}
	rt := &dockerRuntime{name: "shellforge-sandbox", image: "shellforge-sandbox", run: fake}

	err := rt.ensureImage(context.Background(), "shellforge-sandbox")
	if err == nil {
		t.Fatal("ensureImage returned nil with no image, no clone and no cached rootfs")
	}
	if len(fake.calls) != 1 {
		t.Errorf("calls = %v, want nothing attempted past the inspect", fake.calls)
	}

	var uxErr *ux.Error
	if !errors.As(err, &uxErr) {
		t.Fatalf("ensureImage error = %T (%v), want a *ux.Error carrying a remediation", err, err)
	}
	if uxErr.DocAnchor != "containerfile-not-found" {
		t.Errorf("DocAnchor = %q, want %q", uxErr.DocAnchor, "containerfile-not-found")
	}
	// A remediation the reader cannot follow is the shape of bug #172
	// came in as: the old one said `make rootfs`, which needs a clone,
	// a Makefile and a container engine that the person seeing it has
	// none of.
	if !strings.Contains(uxErr.Remediation, "installer") {
		t.Errorf("Remediation = %q, want it to name the installer that fetches the rootfs", uxErr.Remediation)
	}
	if !strings.Contains(uxErr.Remediation, "clone") {
		t.Errorf("Remediation = %q, want it to offer the clone route too, which is the only one that works on arm64", uxErr.Remediation)
	}
	if !strings.Contains(uxErr.Remediation, rootfs) {
		t.Errorf("Remediation = %q, want it to name the cache path %q the installer fills", uxErr.Remediation, rootfs)
	}
}

// TestSandboxImageEnvMatchesTheContainerfile pins sandboxImageEnv against
// the ENV block in images/Containerfile, in the same style as the WSL
// backend's wsl.conf constant.
//
// Without this, adding a variable to the Containerfile would change the
// image a developer builds and leave the image a release install imports
// behind, and the symptom would be a level that passes on one machine and
// fails on the other for a reason nobody can reproduce. That is exactly the
// failure the Containerfile's own header says one source of truth exists to
// prevent.
func TestSandboxImageEnvMatchesTheContainerfile(t *testing.T) {
	path, err := repoRootRelative(containerfilePath)
	if err != nil {
		t.Skipf("skipping: %s is not resolvable from here: %v", containerfilePath, err)
	}
	content, err := os.ReadFile(path) // #nosec G304 -- a test reading this repository's own Containerfile
	if err != nil {
		t.Skipf("skipping: cannot read %s: %v", path, err)
	}

	want := parseContainerfileEnv(t, string(content))
	got := sandboxImageEnv()

	if len(got) != len(want) {
		t.Fatalf("sandboxImageEnv() = %v, but images/Containerfile declares %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sandboxImageEnv()[%d] = %q, want %q from images/Containerfile", i, got[i], want[i])
		}
	}
}

// parseContainerfileEnv reads the one ENV instruction out of a
// Containerfile, following its backslash continuations. It deliberately
// refuses a second ENV rather than merging them: the pin above compares an
// ordered list, and silently concatenating two blocks would let the order
// drift without the test noticing.
func parseContainerfileEnv(t *testing.T, content string) []string {
	t.Helper()

	var out []string
	seen := false
	lines := strings.Split(content, "\n")
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(strings.TrimSpace(lines[i]), "ENV ") {
			continue
		}
		if seen {
			t.Fatalf("images/Containerfile has more than one ENV instruction; this test and sandboxImageEnv assume exactly one")
		}
		seen = true

		block := strings.TrimSpace(lines[i])
		for strings.HasSuffix(block, "\\") {
			i++
			if i >= len(lines) {
				t.Fatal("images/Containerfile ends inside an ENV continuation")
			}
			block = strings.TrimSuffix(block, "\\") + " " + strings.TrimSpace(lines[i])
		}
		out = append(out, strings.Fields(strings.TrimPrefix(block, "ENV "))...)
	}
	if !seen {
		t.Fatal("images/Containerfile has no ENV instruction; sandboxImageEnv would then be inventing one")
	}
	return out
}
