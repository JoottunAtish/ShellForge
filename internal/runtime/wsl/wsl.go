package wsl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/platform"
	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

// sandboxDistro and contractDistro are the closed set of two compile-time
// string literals a destroy target may ever equal. sandboxDistro is the
// production distribution, matching the destructive-safety skill's naming.
// contractDistro equals runtimetest.SandboxName
// (TestContractDistroMatchesRuntimetestSandboxName pins the two together),
// so the runtimetest contract suite can provision and destroy its own
// sandbox without either editing the suite or handing it a Runtime whose
// destroy constant is the production name. New refuses any other value;
// Destroy re-checks against the same set before acting.
const (
	sandboxDistro  = "shellforge-sandbox"
	contractDistro = "shellforge-contracttest"
)

// markerPath and markerContent identify a distribution as Shellforge's own.
// The image bakes this file in at build time (images/Containerfile), owned
// root, mode 0444, so a learner cannot delete or rewrite it. Destroy reads
// it before acting and refuses when it is missing or holds different
// content: a missing or wrong marker means a name collision with something
// Shellforge did not create, not a sandbox to remove.
const (
	markerPath    = "/opt/shellforge/.sandbox-id"
	markerContent = "shellforge-sandbox-v1"
)

// importTimeout bounds `wsl --import` of the rootfs tarball.
//
// TODO(v0.2): detect a slow import and name the antivirus exclusion path
// automatically (ARCHITECTURE section 10 item 9). Until then, a timeout
// here is reported through the wsl-import-blocked doc anchor, whose
// heading already tells the learner to add the exclusion by hand.
const importTimeout = 10 * time.Minute

// wslConf is the exact content Provision writes to /etc/wsl.conf inside the
// distribution. It is a package constant, not a go:embed of
// images/wsl.conf, because go:embed cannot reach outside this package's own
// directory and a second copied file is one nobody would remember to keep
// in sync. TestWslConfConstantMatchesImagesWslConf reads images/wsl.conf
// from the repository and compares byte for byte after CRLF normalisation,
// so drift between the two is a red test rather than a silent safety
// regression.
const wslConf = `# WSL configuration for the Shellforge sandbox distribution.
#
# Every setting here exists to protect a teaching promise. Changing one silently
# breaks either the safety guarantee or a lesson.

[automount]
# No /mnt/c. This is what makes "rm -rf / cannot hurt your computer" true on
# Windows. It also means ` + "`ls /`" + ` shows a clean Linux root instead of a confusing
# mount of the learner's Windows drive.
enabled = false

[interop]
# The learner cannot launch notepad.exe from bash.
enabled = false
# $PATH is not polluted with several hundred Windows directories. Without this,
# ` + "`echo $PATH`" + ` is unreadable and every lesson in Act VI is nonsense.
appendWindowsPath = false

[boot]
# systemd costs seconds on first start and v0.1 teaches no systemctl levels.
# Turn this on only when a level declares requires: [systemd].
systemd = false

[user]
default = learner
`

// wslRuntime implements runtime.Runtime over a single named WSL
// distribution.
//
// distro is the distribution's identity: fixed at construction to one of
// the two package constants above, never a parameter to any method, and
// never read back from an ImageSpec. That is what destructive-safety
// requires of a destroy target.
type wslRuntime struct {
	distro string
	run    runner
	fetch  fetcher
}

var _ runtime.Runtime = (*wslRuntime)(nil)

// New returns a Runtime for the named WSL distribution. name must be
// exactly one of the two Shellforge-owned distribution constants; New
// refuses anything else rather than accepting an arbitrary name, because
// the value it returns is what Destroy will later act on.
func New(name string) (runtime.Runtime, error) {
	if !platform.ValidIdentifier(name) {
		return nil, fmt.Errorf("wsl.New: distribution name %q does not match %s", name, platform.IdentifierPattern)
	}
	if name != sandboxDistro && name != contractDistro {
		return nil, fmt.Errorf("wsl.New: distribution name %q is not one of the Shellforge constants %q or %q", name, sandboxDistro, contractDistro)
	}

	bin, err := exec.LookPath("wsl.exe")
	if err != nil {
		return nil, ux.Fail(
			"find the wsl command",
			err,
			"install WSL2: run `wsl --install` from an administrator PowerShell, then restart",
			"wsl-not-installed",
		)
	}

	return &wslRuntime{distro: name, run: execRunner{bin: bin}, fetch: httpFetcher{}}, nil
}

// Capabilities reports what this backend supports. The image ships a
// learner and a root account (MultiUser true) and a level can opt a
// session into networking (Networking true), but /etc/wsl.conf turns
// systemd off (Systemd false), reset wipes the scratch directory instead of
// round tripping wsl --export (Snapshotting false), and Shellforge never
// asks for elevated privileges (Privileged false).
func (rt *wslRuntime) Capabilities() runtime.Caps {
	return runtime.Caps{
		Networking: true,
		MultiUser:  true,
	}
}

// normalizeCRLF turns every CRLF pair into a bare LF, so a /etc/wsl.conf
// readback that came back with Windows-style line endings still compares
// equal to the LF-only wslConf constant.
func normalizeCRLF(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// listRows runs `wsl -l -v` and returns every distribution row it reports.
//
// A non-zero exit is not automatically an error: wsl.exe reports "no
// distributions registered on this host at all" in more than one shape
// depending on the build. Some print nothing and exit 0, which parseList
// already reads as zero rows. Others print a human-readable sentence,
// worded differently per display language and so never safe to match by
// text the way the header row is dropped, and exit non-zero; a GitHub
// hosted windows-latest runner is exactly this case. Both are read the same
// way: no rows, no error. The one thing this must not paper over is a
// genuine failure, so a non-zero exit whose output carries one of the
// signatures classifyFailure recognizes is still reported as that failure.
// Anything else non-zero and otherwise unrecognized is treated as "no
// distributions", which is the honest answer when there is no stronger
// signal either way: op names the caller's operation for a genuine failure's
// message.
func (rt *wslRuntime) listRows(ctx context.Context, op string) ([]distro, error) {
	stdout, stderr, code, err := rt.run.run(ctx, []string{"wsl.exe", "-l", "-v"}, nil)
	if err != nil {
		return nil, rt.classifyFailure(ctx, op, err, stderr)
	}
	if code != 0 {
		// Check for a recognizable failure signature before ever trying to
		// parse stdout as a distribution table: a genuine failure can carry
		// an empty stdout with everything on stderr, which parseList would
		// otherwise read as a cleanly-parsed empty table and let win by
		// accident. Only when nothing recognizable explains the non-zero
		// exit is it read as "no distributions", regardless of whether
		// stdout was empty, a friendly multi-line sentence that failed to
		// parse as rows, or anything else unrecognized: that is the honest
		// answer when there is no stronger signal either way.
		if failErr := recognizedNonZeroExit(code, stdout, stderr); failErr != nil {
			return nil, rt.classifyFailure(ctx, op, failErr, stderr)
		}
		return nil, nil
	}
	rows, err := parseList(stdout)
	if err != nil {
		return nil, fmt.Errorf("wsl: %s: %w", op, err)
	}
	return rows, nil
}

// findRow returns the row of `wsl -l -v` matching rt.distro, if any.
func (rt *wslRuntime) findRow(ctx context.Context) (distro, bool, error) {
	rows, err := rt.listRows(ctx, "list WSL distributions")
	if err != nil {
		return distro{}, false, err
	}
	for _, row := range rows {
		if row.Name == rt.distro {
			return row, true, nil
		}
	}
	return distro{}, false, nil
}

// Provision ensures the distribution exists at WSL2, hardened, and
// running.
//
// A distribution not yet present is imported from the resolved rootfs,
// configured, and started: import, write /etc/wsl.conf, read it back to
// confirm, terminate so the config takes effect, start it again, then
// verify the hardening by observation. A distribution already present is
// reused only when it is WSL2 and carries the Shellforge marker; a WSL1
// distribution or one missing the marker is refused rather than clobbered.
// For an already-marked WSL2 distribution, /etc/wsl.conf is rewritten and
// the distribution terminated only when the readback differs from what
// Shellforge expects, and the hardening is re-verified either way: this is
// the idempotent path.
func (rt *wslRuntime) Provision(ctx context.Context, spec runtime.ImageSpec) error {
	row, exists, err := rt.findRow(ctx)
	if err != nil {
		return err
	}

	if !exists {
		if err := rt.importRootfs(ctx, spec); err != nil {
			return err
		}
		if err := rt.writeAndVerifyWslConf(ctx); err != nil {
			return err
		}
		if err := rt.terminate(ctx); err != nil {
			return err
		}
		if err := rt.ensureRunning(ctx, false); err != nil {
			return err
		}
		return rt.verifyHardening(ctx)
	}

	if row.Version == 1 {
		return ux.Fail(
			"provision the Windows sandbox",
			fmt.Errorf("distribution %q is a WSL1 distribution; Shellforge requires WSL2", rt.distro),
			fmt.Sprintf("run `wsl --set-version %s 2`, or remove it with `wsl --unregister %s` and run `shellforge init` again", rt.distro, rt.distro),
			"wsl-version-1",
		)
	}

	hasMarker, err := rt.hasSandboxMarker(ctx)
	if err != nil {
		return err
	}
	if !hasMarker {
		return ux.Fail(
			"provision the Windows sandbox",
			fmt.Errorf("distribution %q already exists and does not carry the Shellforge marker at %s", rt.distro, markerPath),
			fmt.Sprintf("remove it yourself with `wsl --unregister %s` if it is not a Shellforge sandbox, then run `shellforge init` again", rt.distro),
			"wsl-name-collision",
		)
	}

	current, err := rt.readWslConf(ctx)
	if err != nil {
		return err
	}
	terminated := false
	if normalizeCRLF(current) != normalizeCRLF(wslConf) {
		if err := rt.writeAndVerifyWslConf(ctx); err != nil {
			return err
		}
		if err := rt.terminate(ctx); err != nil {
			return err
		}
		terminated = true
	}

	// row.State was captured before any terminate this call might have just
	// run: an already-Running distribution whose /etc/wsl.conf needed
	// rewriting is stopped by the terminate above, so ensureRunning must be
	// told the real, current state, not the stale one findRow observed.
	// Otherwise a successful Provision can leave the sandbox stopped, only
	// appearing to satisfy "a successful Provision leaves the sandbox
	// running" because verifyHardening's first probe boots it as a side
	// effect.
	if err := rt.ensureRunning(ctx, row.State == "Running" && !terminated); err != nil {
		return err
	}
	return rt.verifyHardening(ctx)
}

// importRootfs resolves and digest-verifies the rootfs tarball, then runs
// `wsl --import`. The digest is verified before import, never after: an
// unverified artifact is never handed to wsl --import, per the security
// skill's artifact integrity rule.
func (rt *wslRuntime) importRootfs(ctx context.Context, spec runtime.ImageSpec) error {
	rf, err := resolveRootfs(ctx, rt.fetch, spec)
	if err != nil {
		return err
	}
	if spec.SHA256 != "" {
		if err := verifyDigest(rf.path, spec.SHA256); err != nil {
			if rf.downloaded {
				_ = os.Remove(rf.path)
			}
			return ux.Fail(
				"verify the sandbox rootfs",
				err,
				"delete the corrupted download and run `shellforge init` again to re-download",
				"rootfs-checksum-mismatch",
			)
		}
	}

	dir, err := rt.installDir()
	if err != nil {
		return err
	}
	if err := validateInstallDir(dir); err != nil {
		return err
	}
	if err := platform.EnsureDir(dir); err != nil {
		return fmt.Errorf("wsl: create install directory %s: %w", dir, err)
	}

	importCtx, cancel := context.WithTimeout(ctx, importTimeout)
	defer cancel()
	_, stderr, code, err := rt.run.run(importCtx, []string{"wsl.exe", "--import", rt.distro, dir, rf.path, "--version", "2"}, nil)
	if err != nil {
		if importCtx.Err() != nil && ctx.Err() == nil {
			return ux.Fail(
				"import the Windows sandbox",
				err,
				"add an antivirus exclusion for the Shellforge cache and install directories, then run `shellforge init` again",
				"wsl-import-blocked",
			)
		}
		return rt.classifyFailure(ctx, "import the Windows sandbox", err, stderr)
	}
	if code != 0 {
		return rt.classifyFailure(ctx, "import the Windows sandbox", fmt.Errorf("wsl --import exited %d: %s", code, stderr), stderr)
	}
	return nil
}

// writeWslConf pipes the wslConf constant to /etc/wsl.conf as root.
func (rt *wslRuntime) writeWslConf(ctx context.Context) error {
	_, stderr, code, err := rt.run.run(ctx, []string{"wsl.exe", "-d", rt.distro, "-u", "root", "--exec", "/bin/sh", "-c", "cat > /etc/wsl.conf"}, []byte(wslConf))
	if err != nil {
		return rt.classifyFailure(ctx, "harden the Windows sandbox", err, stderr)
	}
	if code != 0 {
		return rt.classifyFailure(ctx, "harden the Windows sandbox", fmt.Errorf("writing /etc/wsl.conf exited %d: %s", code, stderr), stderr)
	}
	return nil
}

// readWslConf reads /etc/wsl.conf back as root.
func (rt *wslRuntime) readWslConf(ctx context.Context) (string, error) {
	stdout, stderr, code, err := rt.run.run(ctx, []string{"wsl.exe", "-d", rt.distro, "-u", "root", "--exec", "/bin/cat", "--", "/etc/wsl.conf"}, nil)
	if err != nil {
		return "", rt.classifyFailure(ctx, "harden the Windows sandbox", err, stderr)
	}
	if code != 0 {
		return "", rt.classifyFailure(ctx, "harden the Windows sandbox", fmt.Errorf("reading /etc/wsl.conf exited %d: %s", code, stderr), stderr)
	}
	return string(stdout), nil
}

// writeAndVerifyWslConf writes wslConf and reads it back, refusing with
// sandbox-unhealthy when the two do not match. /etc/wsl.conf is read only
// when the distribution boots, so a write this method cannot confirm is a
// claim, not a fact: the terminate step that makes it take effect only
// happens after this succeeds.
func (rt *wslRuntime) writeAndVerifyWslConf(ctx context.Context) error {
	if err := rt.writeWslConf(ctx); err != nil {
		return err
	}
	got, err := rt.readWslConf(ctx)
	if err != nil {
		return err
	}
	if normalizeCRLF(got) != normalizeCRLF(wslConf) {
		return ux.Fail(
			"harden the Windows sandbox",
			errors.New("/etc/wsl.conf readback does not match what Shellforge wrote"),
			"run `shellforge sandbox rebuild`",
			"sandbox-unhealthy",
		)
	}
	return nil
}

// terminate runs `wsl --terminate`, which is required before a rewritten
// /etc/wsl.conf takes effect: WSL reads it only at boot.
func (rt *wslRuntime) terminate(ctx context.Context) error {
	_, stderr, code, err := rt.run.run(ctx, []string{"wsl.exe", "--terminate", rt.distro}, nil)
	if err != nil {
		return rt.classifyFailure(ctx, "harden the Windows sandbox", err, stderr)
	}
	if code != 0 {
		return rt.classifyFailure(ctx, "harden the Windows sandbox", fmt.Errorf("wsl --terminate exited %d: %s", code, stderr), stderr)
	}
	return nil
}

// ensureRunning starts the distribution when it is not already running.
// Runtime.Provision's doc comment requires a successful Provision to leave
// the sandbox running, not merely created.
func (rt *wslRuntime) ensureRunning(ctx context.Context, running bool) error {
	if running {
		return nil
	}
	_, stderr, code, err := rt.run.run(ctx, []string{"wsl.exe", "-d", rt.distro, "-u", "root", "--exec", "/bin/true"}, nil)
	if err != nil {
		return rt.classifyFailure(ctx, "start the Windows sandbox", err, stderr)
	}
	if code != 0 {
		return rt.classifyFailure(ctx, "start the Windows sandbox", fmt.Errorf("starting the distribution exited %d: %s", code, stderr), stderr)
	}
	return nil
}

// verifyHardening confirms by observation, not by trusting the config
// file, that automount and interop are actually off: /mnt/c does not
// exist, $PATH carries no /mnt/ element, and the default user is learner.
func (rt *wslRuntime) verifyHardening(ctx context.Context) error {
	if err := rt.probeNoMntC(ctx); err != nil {
		return err
	}
	if err := rt.probeCleanPath(ctx); err != nil {
		return err
	}
	return rt.probeDefaultUserIsLearner(ctx)
}

func (rt *wslRuntime) probeNoMntC(ctx context.Context) error {
	_, stderr, code, err := rt.run.run(ctx, []string{"wsl.exe", "-d", rt.distro, "-u", "learner", "--exec", "/bin/sh", "-c", "test -e /mnt/c"}, nil)
	if err != nil {
		return rt.classifyFailure(ctx, "harden the Windows sandbox", err, stderr)
	}
	if code == 0 {
		return ux.Fail(
			"harden the Windows sandbox",
			errors.New("/mnt/c exists inside the sandbox; automount did not disable"),
			"run `shellforge sandbox rebuild`",
			"sandbox-unhealthy",
		)
	}
	return nil
}

func (rt *wslRuntime) probeCleanPath(ctx context.Context) error {
	stdout, stderr, code, err := rt.run.run(ctx, []string{"wsl.exe", "-d", rt.distro, "-u", "learner", "--exec", "/bin/sh", "-c", "echo $PATH"}, nil)
	if err != nil {
		return rt.classifyFailure(ctx, "harden the Windows sandbox", err, stderr)
	}
	if code != 0 {
		return rt.classifyFailure(ctx, "harden the Windows sandbox", fmt.Errorf("checking $PATH exited %d: %s", code, stderr), stderr)
	}
	if strings.Contains(string(stdout), "/mnt/") {
		return ux.Fail(
			"harden the Windows sandbox",
			fmt.Errorf("$PATH contains a /mnt/ element: %s", strings.TrimSpace(string(stdout))),
			"run `shellforge sandbox rebuild`",
			"sandbox-unhealthy",
		)
	}
	return nil
}

func (rt *wslRuntime) probeDefaultUserIsLearner(ctx context.Context) error {
	stdout, stderr, code, err := rt.run.run(ctx, []string{"wsl.exe", "-d", rt.distro, "-u", "learner", "--exec", "/bin/sh", "-c", "id -un"}, nil)
	if err != nil {
		return rt.classifyFailure(ctx, "harden the Windows sandbox", err, stderr)
	}
	if code != 0 {
		return rt.classifyFailure(ctx, "harden the Windows sandbox", fmt.Errorf("checking the default user exited %d: %s", code, stderr), stderr)
	}
	if got := strings.TrimSpace(string(stdout)); got != "learner" {
		return ux.Fail(
			"harden the Windows sandbox",
			fmt.Errorf("the sandbox's default user is %q, not learner", got),
			"run `shellforge sandbox rebuild`",
			"sandbox-unhealthy",
		)
	}
	return nil
}

// enumerate runs `wsl -l -q` and returns the distribution names it
// reports.
func (rt *wslRuntime) enumerate(ctx context.Context) ([]string, error) {
	stdout, stderr, code, err := rt.run.run(ctx, []string{"wsl.exe", "-l", "-q"}, nil)
	if err != nil {
		return nil, rt.classifyFailure(ctx, "list WSL distributions", err, stderr)
	}
	if code != 0 {
		// `wsl -l -q` with zero distributions registered can exit non-zero
		// on some builds while still printing nothing useful; treat a
		// clean decode of empty output as "no distributions" rather than
		// an error, and anything else as a real failure.
		names, parseErr := parseQuietList(stdout)
		if parseErr == nil && len(names) == 0 {
			return nil, nil
		}
		return nil, rt.classifyFailure(ctx, "list WSL distributions", fmt.Errorf("wsl -l -q exited %d: %s", code, stderr), stderr)
	}
	return parseQuietList(stdout)
}

// present reports whether rt.distro appears in `wsl -l -q`.
func (rt *wslRuntime) present(ctx context.Context) (bool, error) {
	names, err := rt.enumerate(ctx)
	if err != nil {
		return false, err
	}
	for _, n := range names {
		if n == rt.distro {
			return true, nil
		}
	}
	return false, nil
}

// hasSandboxMarker reads markerPath as root inside rt.distro and reports
// whether it holds exactly markerContent. A missing file (the read runs but
// exits non-zero) is reported as (false, nil): both Destroy and Provision
// still refuse on that, because a marker they cannot positively confirm is
// treated the same as a marker that is absent, which is the fail-closed
// half of this function's contract. A read that could not run at all (wsl.exe
// itself failed to start or be waited on) is a different fact and is
// reported as (false, err), wrapped through classifyFailure rather than
// returned raw: a caller that folded this into "no marker" would tell a
// learner their sandbox is a name collision with something Shellforge did
// not create, and point them at `wsl --unregister`, the one command that
// would destroy a healthy sandbox by hand, when the real cause was wsl.exe
// not answering at all.
func (rt *wslRuntime) hasSandboxMarker(ctx context.Context) (bool, error) {
	stdout, stderr, code, err := rt.run.run(ctx, []string{
		"wsl.exe", "-d", rt.distro, "-u", "root", "--exec", "/bin/cat", "--", markerPath,
	}, nil)
	if err != nil {
		return false, rt.classifyFailure(ctx, "read the Shellforge sandbox marker", err, stderr)
	}
	if code != 0 {
		return false, nil
	}
	return strings.TrimRight(string(stdout), " \t\r\n") == markerContent, nil
}

// Status parses `wsl -l -v` for rt.distro. It never refuses a Version 1
// row: Provision is where that is refused, so Status stays the cheap
// non-judging probe the contract expects.
func (rt *wslRuntime) Status(ctx context.Context) (runtime.Status, error) {
	rows, err := rt.listRows(ctx, "check the Windows sandbox status")
	if err != nil {
		return runtime.Status{}, err
	}
	for _, row := range rows {
		if row.Name != rt.distro {
			continue
		}
		return runtime.Status{
			Provisioned: true,
			Running:     row.State == "Running",
			Backend:     "wsl",
			Detail:      fmt.Sprintf("%s, version %d", row.State, row.Version),
		}, nil
	}
	return runtime.Status{Backend: "wsl"}, nil
}

// StartSession opens a session on the already-provisioned distribution. It
// does not create or start anything: Provision is documented to leave the
// sandbox running, so StartSession only has to check that promise held.
func (rt *wslRuntime) StartSession(ctx context.Context, spec runtime.SessionSpec) (runtime.Session, error) {
	status, err := rt.Status(ctx)
	if err != nil {
		return nil, err
	}
	if !status.Provisioned || !status.Running {
		return nil, fmt.Errorf("start a wsl session: %w", runtime.ErrSandboxMissing)
	}
	return &wslSession{rt: rt, spec: spec}, nil
}

// Destroy removes the WSL distribution this Runtime owns, in this order:
// refuse a distro that is not one of the two package constants, refuse an
// empty distro, treat "not present" as the documented idempotent success,
// verify the sandbox marker, verify the install directory holds only
// expected WSL backing files, terminate and unregister, confirm exactly one
// entry disappeared, then remove the install directory and report its
// absolute path.
func (rt *wslRuntime) Destroy(ctx context.Context) error {
	if rt.distro == "" {
		return errors.New("wsl: refusing to destroy: distribution name is empty")
	}
	if rt.distro != sandboxDistro && rt.distro != contractDistro {
		return fmt.Errorf("wsl: refusing to destroy distribution %q: shellforge only ever removes %q or %q", rt.distro, sandboxDistro, contractDistro)
	}
	if !platform.ValidIdentifier(rt.distro) {
		return fmt.Errorf("wsl: refusing to destroy distribution %q: does not match %s", rt.distro, platform.IdentifierPattern)
	}

	before, err := rt.enumerate(ctx)
	if err != nil {
		return err
	}
	if !containsString(before, rt.distro) {
		// Idempotent success means "no sandbox remains", not merely "wsl no
		// longer lists it". A previous Destroy can unregister the
		// distribution and then fail to remove its install directory, for
		// instance because Windows still held ext4.vhdx open; retrying
		// Destroy must finish that job rather than reporting success a
		// second time while the 2 GB backing file sits orphaned forever.
		// removeInstallDir validates dir itself, and os.RemoveAll on a
		// directory that was never created is a no-op, so the ordinary
		// never-provisioned case stays exactly as clean as before.
		dir, err := rt.installDir()
		if err != nil {
			return err
		}
		if err := removeInstallDir(dir); err != nil {
			return fmt.Errorf("wsl: remove the install directory %s: %w", dir, err)
		}
		return nil
	}

	hasMarker, err := rt.hasSandboxMarker(ctx)
	if err != nil {
		return err
	}
	if !hasMarker {
		return ux.Fail(
			"remove the Windows sandbox",
			fmt.Errorf("distribution %q does not carry the Shellforge marker at %s", rt.distro, markerPath),
			fmt.Sprintf("if this is not a Shellforge sandbox, remove it yourself with `wsl --unregister %s`; otherwise run `shellforge sandbox rebuild`", rt.distro),
			"wsl-name-collision",
		)
	}

	dir, err := rt.installDir()
	if err != nil {
		return err
	}
	if err := refuseUnexpectedInstallDirContents(dir); err != nil {
		return err
	}

	if _, stderr, code, err := rt.run.run(ctx, []string{"wsl.exe", "--terminate", rt.distro}, nil); err != nil {
		return rt.classifyFailure(ctx, "remove the Windows sandbox", err, stderr)
	} else if code != 0 {
		return rt.classifyFailure(ctx, "remove the Windows sandbox", fmt.Errorf("wsl --terminate exited %d: %s", code, stderr), stderr)
	}

	if _, stderr, code, err := rt.run.run(ctx, []string{"wsl.exe", "--unregister", rt.distro}, nil); err != nil {
		return rt.classifyFailure(ctx, "remove the Windows sandbox", err, stderr)
	} else if code != 0 {
		return rt.classifyFailure(ctx, "remove the Windows sandbox", fmt.Errorf("wsl --unregister exited %d: %s", code, stderr), stderr)
	}

	after, err := rt.enumerate(ctx)
	if err != nil {
		return err
	}
	gone := missingFrom(before, after)
	switch {
	case len(gone) == 0:
		return fmt.Errorf("wsl: unregistered %q but it still appears in `wsl -l -q`", rt.distro)
	case len(gone) > 1:
		return fmt.Errorf("wsl: unregistering %q removed more than one distribution: %v", rt.distro, gone)
	case gone[0] != rt.distro:
		return fmt.Errorf("wsl: unregistering %q instead removed %q", rt.distro, gone[0])
	}

	if err := removeInstallDir(dir); err != nil {
		return fmt.Errorf("wsl: remove the install directory %s: %w", dir, err)
	}
	return nil
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// missingFrom returns the entries of before that do not appear in after.
func missingFrom(before, after []string) []string {
	afterSet := make(map[string]bool, len(after))
	for _, a := range after {
		afterSet[a] = true
	}
	var gone []string
	for _, b := range before {
		if !afterSet[b] {
			gone = append(gone, b)
		}
	}
	return gone
}

// kernelOutdatedSignatures and importBlockedSignatures are the stderr
// substrings that identify a genuine wsl.exe failure, as opposed to the
// many shapes a non-zero exit with no real failure at all can otherwise
// take (see listRows). classifyFailure and listRows both match against
// these same two lists, so narrowing or widening what counts as a known
// failure only ever happens in one place.
var (
	kernelOutdatedSignatures = []string{"WSL_E_KERNEL", "0x80370102", "kernel update"}
	importBlockedSignatures  = []string{"0x80070005", "Access is denied", "antivirus"}
)

// recognizedNonZeroExit is the single shared implementation of "is this
// non-zero wsl.exe exit a recognized failure, or an empty distribution
// table". listRows and probe.go's version2 probe seam both call this rather
// than each keeping its own copy of the signature check, so narrowing or
// widening what counts as a known failure only ever happens here.
//
// combined stdout and stderr is checked, because a genuine failure can put
// everything on stderr and leave stdout empty, which would otherwise parse
// as a cleanly empty table and let the failure pass unnoticed. A recognized
// signature returns a non-nil error describing the failure, for the caller
// to classify further. Anything else, including a plain non-zero exit with
// no recognizable signature, returns a nil error: that is read as "no
// distributions", the honest answer when there is no stronger signal
// either way.
func recognizedNonZeroExit(code int, stdout, stderr []byte) error {
	combined := string(stdout) + string(stderr)
	if containsAny(combined, kernelOutdatedSignatures...) || containsAny(combined, importBlockedSignatures...) {
		return fmt.Errorf("wsl -l -v exited %d: %s", code, stderr)
	}
	return nil
}

// classifyFailure turns a raw wsl.exe failure into a ux.Fail carrying a
// remediation and a doc anchor. It mirrors the docker sibling's shape:
// distinguish what is knowable from the failure text, and fall back to a
// generic wrapped error when nothing more specific applies. New already
// catches "wsl.exe missing" before this is ever reached.
func (rt *wslRuntime) classifyFailure(_ context.Context, op string, err error, stderr []byte) error {
	msg := string(stderr)
	switch {
	// Match the specific kernel-outdated signatures only. The bare words
	// "kernel" and "update" alone are too eager: "Access is denied. Please
	// update your antivirus exclusions" contains "update" and would
	// otherwise route here first and tell the learner to run `wsl
	// --update`, which does nothing for an antivirus block.
	case containsAny(msg, kernelOutdatedSignatures...):
		return ux.Fail(op, err, "run `wsl --update` from an administrator PowerShell, then try again", "wsl-kernel-outdated")
	case containsAny(msg, importBlockedSignatures...):
		return ux.Fail(op, err, "add an antivirus exclusion for the Shellforge cache directory and try again; see the troubleshooting guide", "wsl-import-blocked")
	default:
		return fmt.Errorf("%s: %w", op, err)
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if sub != "" && strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
