package wsl

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// cancelGrace bounds how long a cancelled wsl.exe invocation is given to
// exit on its own once signalled, before Wait forcibly kills it.
const cancelGrace = 5 * time.Second

// runner executes one wsl.exe invocation and reports its outcome. Every
// wslRuntime and wslSession method builds an argv and calls run exactly
// once per wsl.exe process; nothing here ever touches a host shell.
//
// This indirection exists so the argv-construction tests can assert the
// exact argv a method produces without spawning wsl.exe, by substituting a
// fakeRunner that records what it was asked to run. It mirrors
// internal/runtime/docker's runner exactly in shape; it is not shared with
// that package, because cross-importing a sibling backend is forbidden by
// the layer rule and the duplication is small.
type runner interface {
	// run executes argv[0] with argv[1:] as arguments, feeding stdin to the
	// process and returning what it wrote to stdout and stderr separately,
	// its exit code, and an error only when the process could not be
	// started or waited on at all. A non-zero exit is reported through
	// code, never through err.
	run(ctx context.Context, argv []string, stdin []byte) (stdout, stderr []byte, code int, err error)
}

// execRunner is the real runner, backed by os/exec. bin is the resolved
// path to wsl.exe; argv[0] is always "wsl.exe" as far as callers are
// concerned, and execRunner substitutes bin so the command name itself is
// never variable, per the security skill.
type execRunner struct {
	bin string
}

func (r execRunner) run(ctx context.Context, argv []string, stdin []byte) (stdout, stderr []byte, code int, err error) {
	if len(argv) == 0 || argv[0] != "wsl.exe" {
		return nil, nil, 0, errors.New("wsl: runner.run called with an argv that does not start with \"wsl.exe\"")
	}

	// #nosec G204 -- r.bin is exec.LookPath("wsl.exe") resolved once in New
	// and never variable from config; argv[1:] is the vector every method
	// in this package builds from allowlist-validated identifiers and
	// caller-supplied argv elements passed straight to the sandbox, never
	// a shell string.
	cmd := exec.CommandContext(ctx, r.bin, argv[1:]...)
	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = cancelGrace
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	runErr := cmd.Run()
	if ctx.Err() != nil {
		return outBuf.Bytes(), errBuf.Bytes(), -1, ctx.Err()
	}
	if runErr == nil {
		return outBuf.Bytes(), errBuf.Bytes(), 0, nil
	}

	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return outBuf.Bytes(), errBuf.Bytes(), exitErr.ExitCode(), nil
	}
	return outBuf.Bytes(), errBuf.Bytes(), -1, runErr
}

// fetcher downloads a rootfs artifact. This indirection is what lets the
// digest-refusal tests run with no network: they substitute a fakeFetcher
// that writes known bytes without ever dialing out.
type fetcher interface {
	fetch(ctx context.Context, url, dest string) error
}

// httpFetcher is the real fetcher, backed by net/http. It refuses any
// scheme other than https, matching the security skill's artifact
// integrity rules: an http, file, or any other URL scheme is refused
// outright rather than fetched.
type httpFetcher struct{}

func (httpFetcher) fetch(ctx context.Context, url, dest string) error {
	if !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("wsl: refusing to fetch %q: only https is supported", url)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("wsl: build request for %s: %w", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("wsl: fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("wsl: fetch %s: unexpected status %s", url, resp.Status)
	}

	// #nosec G304 -- dest is always CacheDir()/rootfs/<fixed name>, never a
	// path taken from config or from the URL itself.
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("wsl: open %s for writing: %w", dest, err)
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return fmt.Errorf("wsl: write %s: %w", dest, err)
	}
	return nil
}

// sha256HexPattern is 64 lowercase hex characters, the shape of a sha256
// digest. verifyDigest refuses anything else before it opens a file.
var sha256HexPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// verifyDigest reports whether the file at path has sha256 digest wantHex.
// wantHex is checked against sha256HexPattern before anything is opened, so
// a malformed digest is refused as a programmer error rather than treated
// as "verification failed".
func verifyDigest(path, wantHex string) error {
	if !sha256HexPattern.MatchString(wantHex) {
		return fmt.Errorf("wsl: refusing to verify against a malformed sha256 digest %q: want 64 lowercase hex characters", wantHex)
	}

	// #nosec G304 -- path is always a Shellforge-resolved rootfs location
	// (a CacheDir download destination or a caller-supplied local rootfs
	// path), never taken from a level or from config.
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("wsl: open %s to verify its digest: %w", path, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("wsl: read %s to verify its digest: %w", path, err)
	}
	gotHex := hex.EncodeToString(h.Sum(nil))
	if gotHex != wantHex {
		return fmt.Errorf("wsl: %s has sha256 %s, want %s", path, gotHex, wantHex)
	}
	return nil
}
