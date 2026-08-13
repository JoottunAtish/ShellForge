package setup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/platform"
	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

const (
	// DefaultStateDir is SF_STATE when no Option overrides it. It sits
	// under /home/learner/, inside the sandbox user's own home, which is
	// writable on Docker today and stays writable once a level pack ships
	// against a read-only /opt/shellforge mount: that mount is not a bind
	// mount at all on the Docker backend today, but the WSL backend and the
	// Day 3 decision make it one, and this constant is what keeps the state
	// directory off of it before that can bite.
	DefaultStateDir = "/home/learner/.shellforge"

	// LevelRootPrefix is the only prefix a level root, or the state
	// directory, may live under.
	LevelRootPrefix = "/home/learner/"

	// DefaultTimeout bounds setup.script and teardown.script when the level
	// does not set its own timeout_seconds.
	DefaultTimeout = 30 * time.Second

	// DefaultMode is the permission bits applied to a materialized file
	// whose FileSpec sets no mode.
	DefaultMode = fs.FileMode(0o644)

	// DefaultOwner is the owner and group applied to a materialized file
	// whose FileSpec sets no owner, and to the level root itself after
	// PushFiles creates its ancestor directories as uid 0.
	DefaultOwner = "learner:learner"

	// SentinelName is the marker file's name, written under the state
	// directory, never under the level's own root.
	SentinelName = "SETUP_OK"

	// rollbackTimeout bounds the cleanup Teardown that a failed Setup runs
	// on a context.WithoutCancel copy of the caller's context, so a Ctrl-C
	// at the moment of failure still cleans up rather than hanging forever.
	rollbackTimeout = 30 * time.Second

	// maxManifestBytes bounds the total size of a level's staged assets.
	// Staging happens in host memory, so an unbounded generator or a huge
	// inline asset is a host memory problem driven by pack data; this
	// refuses rather than truncates.
	maxManifestBytes = 64 * 1024 * 1024
)

// Sentinel errors. Compare with errors.Is; never match an error string.
var (
	// ErrUnsafeLevelRoot is wrapped by any error refusing to use a value as
	// a level root or a state directory.
	ErrUnsafeLevelRoot = errors.New("unsafe level root")

	// ErrInvalidLevelPack is wrapped by any error reporting that a level
	// pack declares something this package cannot build.
	ErrInvalidLevelPack = errors.New("invalid level pack")

	// ErrScriptFailed is wrapped by any error reporting that setup.script
	// or teardown.script exited non-zero or timed out.
	ErrScriptFailed = errors.New("script failed")
)

// Remediation strings, one per doc anchor this package emits. Kept together
// so the wording in docs/05-troubleshooting.md and the wording a learner
// sees from ux.Render cannot drift apart unnoticed.
const (
	remediationUnsafeLevelRoot = "Check the level's setup.root: it must be a folder strictly inside /home/learner/, and /home/learner on its own is not allowed. Run `shellforge author validate packs/core-linux-basics`. If a symlink appeared inside your sandbox home during the level, remove it and start the level again."

	remediationLevelPackInvalid = "The error names the file and the field. Correct it, then run `shellforge author validate packs/core-linux-basics`."

	remediationScriptFailed = "This is a bug in the level, not in what you typed. If the level is yours, look at setup.script or teardown.script in its YAML and run `shellforge author test <level-id>`. A timeout means the script ran longer than setup.timeout_seconds allows."

	remediationSandboxUnhealthy = "Run `shellforge sandbox rebuild`."

	remediationLevelStateCorrupted = "Run `shellforge reset --hard`. If that does not help, remove the sandbox with `docker rm -f shellforge-sandbox` and let the next run build a clean one."
)

// ownerPattern matches a bare user or a user:group pair, such as "learner"
// or "learner:learner". The first character of each part is restricted to
// alphanumeric for the same reason platform.IdentifierPattern is: a value
// that can start with a hyphen is flag-shaped once it reaches an argv
// vector as a positional operand.
var ownerPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*(:[a-zA-Z0-9][a-zA-Z0-9_-]*)?$`)

// Runner materializes a level's world into a sandbox session and tears it
// down again.
//
// A Runner is not safe for concurrent use: one level is set up at a time.
type Runner struct {
	sess     runtime.Session
	packFS   fs.FS
	stateDir string
}

// Option configures a Runner constructed by NewRunner.
type Option func(*Runner)

// WithStateDir overrides the default state directory. It is validated the
// same way a level root is, lazily, the first time it is used: it must be
// absolute, contain no .. segment, and live under /home/learner/.
func WithStateDir(dir string) Option {
	return func(r *Runner) { r.stateDir = dir }
}

// NewRunner constructs a Runner over sess, reading level assets from packFS.
// opts is a deliberate superset of a plain two-argument constructor: every
// call written against the simpler shape compiles unchanged, and a caller
// that needs a non-default state directory has WithStateDir rather than a
// second constructor.
func NewRunner(sess runtime.Session, packFS fs.FS, opts ...Option) *Runner {
	r := &Runner{sess: sess, packFS: packFS, stateDir: DefaultStateDir}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// StateDir returns the effective state directory: DefaultStateDir unless
// WithStateDir overrode it. A caller threads this same value into
// runtime.SessionSpec.StateDir and the SF_STATE environment variable, which
// is how the two halves of "the runner and the shell agree" stay in step.
func (r *Runner) StateDir() string {
	return r.stateDir
}

// validateLevelRoot refuses, rather than adjusts, any path that is not safe
// to hand to a recursive delete inside the sandbox. It mirrors
// internal/sandbox/demo_level.go's validateLevelRoot exactly, which is the
// already-reviewed prior art for this exact containment problem: empty,
// then a .. segment (checked BEFORE cleaning, because cleaning is what
// would hide it), then not absolute after cleaning, then "/" or ".", then
// the strict /home/learner/ prefix.
func validateLevelRoot(root string) error {
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("refusing to use %q as a level root: it is empty: %w", root, ErrUnsafeLevelRoot)
	}
	for _, segment := range strings.Split(root, "/") {
		if segment == ".." {
			return fmt.Errorf("refusing to use %q as a level root: it contains a .. segment: %w", root, ErrUnsafeLevelRoot)
		}
	}
	clean := path.Clean(root)
	if !path.IsAbs(clean) {
		return fmt.Errorf("refusing to use %q as a level root: it is not absolute: %w", root, ErrUnsafeLevelRoot)
	}
	if clean == "/" || clean == "." {
		return fmt.Errorf("refusing to use %q as a level root: it resolves to %q: %w", root, clean, ErrUnsafeLevelRoot)
	}
	if !strings.HasPrefix(clean, LevelRootPrefix) {
		return fmt.Errorf("refusing to use %q as a level root: level roots must live under %s: %w", root, LevelRootPrefix, ErrUnsafeLevelRoot)
	}
	return nil
}

// refuseIfInsideStateDir refuses a level root that equals, contains, or is
// contained by the state directory. A level root inside the state directory
// is lexically legal under the /home/learner/ prefix rule, but teardown on
// it would delete the journal, the history, and every other level's
// sentinel.
func (r *Runner) refuseIfInsideStateDir(clean string) error {
	state := path.Clean(r.stateDir)
	if clean == state || strings.HasPrefix(clean, state+"/") || strings.HasPrefix(state, clean+"/") {
		return fmt.Errorf("refusing to use %q as a level root: it contains or is contained by the state directory %q: %w", clean, state, ErrUnsafeLevelRoot)
	}
	return nil
}

// resolveLevelRoot validates root lexically, refuses containment against
// the state directory, then resolves it INSIDE the sandbox with
// `readlink -m` and refuses if resolution moved it at all. `readlink -m`
// canonicalizes without requiring the path to exist, which is what makes
// this serve both a first setup and a reset on an already-torn-down root.
//
// Both the whole-run op string and the doc anchor are fixed at
// "set the level world up" and "unsafe-level-root": Setup and Teardown both
// resolve through this one function, and the plan chose to name the failure
// after what the runner was doing conceptually (managing the level's world)
// rather than which of the two callers happened to trigger it.
func (r *Runner) resolveLevelRoot(ctx context.Context, root string) (string, error) {
	// The state directory itself is validated lazily inside sentinelPath,
	// which every path that writes or reads the sentinel goes through
	// (Setup, Teardown, and IsSetUp alike), so a broken WithStateDir value
	// fails closed there instead of needing to be remembered here too.
	if err := validateLevelRoot(root); err != nil {
		return "", ux.Fail("set the level world up", err, remediationUnsafeLevelRoot, "unsafe-level-root")
	}
	clean := path.Clean(root)
	if err := r.refuseIfInsideStateDir(clean); err != nil {
		return "", ux.Fail("set the level world up", err, remediationUnsafeLevelRoot, "unsafe-level-root")
	}

	res, err := r.sess.Exec(ctx, []string{"readlink", "-m", "--", clean}, runtime.ExecOpts{User: userLearner})
	if err != nil {
		return "", ux.Fail("set the level world up",
			fmt.Errorf("resolve the level root %q inside the sandbox: %w", clean, err),
			remediationSandboxUnhealthy, "sandbox-unhealthy")
	}
	if res.ExitCode != 0 {
		return "", ux.Fail("set the level world up",
			fmt.Errorf("resolve the level root %q inside the sandbox: readlink exited %d: %s", clean, res.ExitCode, res.Stderr),
			remediationSandboxUnhealthy, "sandbox-unhealthy")
	}
	resolved := strings.TrimSpace(string(res.Stdout))
	if resolved != clean {
		return "", ux.Fail("set the level world up",
			fmt.Errorf("refusing to use %q as a level root: inside the sandbox it resolves to %q, so something on that path is a symlink: %w", clean, resolved, ErrUnsafeLevelRoot),
			remediationUnsafeLevelRoot, "unsafe-level-root")
	}

	// Re-validate the resolved value, matching internal/sandbox/demo_level.go's
	// prior art for this exact containment problem. resolved equals clean
	// here by the check above, so neither call below can fail today; both
	// stay because the equality check above is the kind of thing a future
	// change relaxes, and these are the assertions that would catch it.
	if err := validateLevelRoot(resolved); err != nil {
		return "", ux.Fail("set the level world up", err, remediationUnsafeLevelRoot, "unsafe-level-root")
	}
	if err := r.refuseIfInsideStateDir(resolved); err != nil {
		return "", ux.Fail("set the level world up", err, remediationUnsafeLevelRoot, "unsafe-level-root")
	}
	return resolved, nil
}

// Teardown removes lvl's world from the session: the sentinel, an optional
// teardown.script, and finally the root itself. It is idempotent: removing
// a root that was never set up succeeds, because rm -f and rm -rf both exit
// zero on a missing path.
//
// The final delete runs as the learner, NOT as root. The sandbox container
// drops every Linux capability and adds back only CHOWN and FOWNER, so root
// inside it does not hold CAP_DAC_OVERRIDE and does not bypass ordinary
// permission checks; a root `rm -rf` on a learner-owned tree fails with
// Permission denied on every entry. This is measured behavior, reproduced
// deliberately from internal/sandbox/demo_level.go, not a stylistic choice.
func (r *Runner) Teardown(ctx context.Context, lvl *content.Level) error {
	root, err := r.resolveLevelRoot(ctx, lvl.Setup.Root)
	if err != nil {
		return err
	}

	if err := r.removeSentinel(ctx, lvl.ID); err != nil {
		return err
	}

	if lvl.Teardown.Script != "" {
		if err := r.runScript(ctx, lvl.Teardown.Script, "/home/learner", DefaultTimeout, "run the level teardown script"); err != nil {
			return err
		}
	}

	// Best-effort repair for one specific broken state: a Setup that died
	// between PushFiles and its own chown leaves root-owned directories the
	// learner cannot delete. root can still chown them, because CAP_CHOWN
	// is one of the two capabilities the container keeps. The exit code is
	// deliberately ignored: the overwhelmingly common case is that root
	// does not exist at all, and chown on a missing path rightly fails.
	if _, err := r.sess.Exec(ctx, []string{"chown", "-R", DefaultOwner, "--", root}, runtime.ExecOpts{User: userRoot}); err != nil {
		return ux.Fail("remove the level world", err, remediationLevelStateCorrupted, "level-state-corrupted")
	}

	res, err := r.sess.Exec(ctx, []string{"rm", "-rf", "--", root}, runtime.ExecOpts{User: userLearner})
	if err != nil {
		return ux.Fail("remove the level world", err, remediationLevelStateCorrupted, "level-state-corrupted")
	}
	if res.ExitCode != 0 {
		return ux.Fail("remove the level world",
			fmt.Errorf("rm -rf %q exited %d: %s", root, res.ExitCode, res.Stderr),
			remediationLevelStateCorrupted, "level-state-corrupted")
	}
	return nil
}

// runScript runs script as root via bash -c, as a single argv element,
// never concatenated with anything else, with the given working directory
// and timeout. err != nil means Exec itself could not run the call at all,
// which is a sandbox problem rather than the script's fault. TimedOut is
// checked before ExitCode, since ExecResult.ExitCode is -1 and not a real
// process status when a call times out.
func (r *Runner) runScript(ctx context.Context, script, workDir string, timeout time.Duration, op string) error {
	res, err := r.sess.Exec(ctx, []string{"bash", "-c", script}, runtime.ExecOpts{User: userRoot, WorkDir: workDir, Timeout: timeout})
	if err != nil {
		return ux.Fail(op, err, remediationSandboxUnhealthy, "sandbox-unhealthy")
	}
	if res.TimedOut {
		return ux.Fail(op,
			fmt.Errorf("%w: timed out after %s (setup.timeout_seconds)", ErrScriptFailed, timeout),
			remediationScriptFailed, "setup-script-failed")
	}
	if res.ExitCode != 0 {
		return ux.Fail(op,
			fmt.Errorf("%w: exited %d: %s", ErrScriptFailed, res.ExitCode, strings.TrimSpace(string(res.Stderr))),
			remediationScriptFailed, "setup-script-failed")
	}
	return nil
}

// Setup materializes lvl's world into the session. It always tears down
// first, so it is safe to call on a dirty root or twice in a row. Any
// failure after the root has been validated and resolved rolls back through
// rollback, which runs Teardown again on a context that survives the
// caller's own cancellation.
func (r *Runner) Setup(ctx context.Context, lvl *content.Level) error {
	root, err := r.resolveLevelRoot(ctx, lvl.Setup.Root)
	if err != nil {
		return err
	}

	manifest, err := r.buildManifest(lvl, root)
	if err != nil {
		return err
	}

	if err := r.Teardown(ctx, lvl); err != nil {
		return err
	}

	// setup.files is optional (docs/LEVEL-FORMAT.md marks it "O"), so a
	// level with only a setup.script is legal, and PushFiles returns
	// immediately without touching the sandbox when the manifest is empty.
	// Without this mkdir, such a level's chown below and its own script
	// both target a root that was never created, and both fail. This must
	// run as the learner: the ancestor directories under /home/learner
	// belong to the learner, and the chown that follows is what fixes
	// ownership of whatever PushFiles creates afterward as uid 0.
	if res, err := r.sess.Exec(ctx, []string{"mkdir", "-p", "--", root}, runtime.ExecOpts{User: userLearner}); err != nil {
		return r.rollback(ctx, lvl, ux.Fail("materialise the level world", err, remediationSandboxUnhealthy, "sandbox-unhealthy"))
	} else if res.ExitCode != 0 {
		return r.rollback(ctx, lvl, ux.Fail("materialise the level world",
			fmt.Errorf("mkdir -p %q exited %d: %s", root, res.ExitCode, res.Stderr),
			remediationSandboxUnhealthy, "sandbox-unhealthy"))
	}

	if err := r.sess.PushFiles(ctx, runtime.FileManifest{Files: manifest}); err != nil {
		return r.rollback(ctx, lvl, ux.Fail("materialise the level world", err, remediationSandboxUnhealthy, "sandbox-unhealthy"))
	}

	// Required, not decoration: PushFiles creates the level root and every
	// ancestor directory below /home/learner/ as uid 0, so without this the
	// learner cannot write into their own level root at all.
	if res, err := r.sess.Exec(ctx, []string{"chown", "-R", DefaultOwner, "--", root}, runtime.ExecOpts{User: userRoot}); err != nil {
		return r.rollback(ctx, lvl, ux.Fail("materialise the level world", err, remediationSandboxUnhealthy, "sandbox-unhealthy"))
	} else if res.ExitCode != 0 {
		return r.rollback(ctx, lvl, ux.Fail("materialise the level world",
			fmt.Errorf("chown %q exited %d: %s", root, res.ExitCode, res.Stderr),
			remediationSandboxUnhealthy, "sandbox-unhealthy"))
	}

	if lvl.Setup.Script != "" {
		timeout := DefaultTimeout
		if lvl.Setup.TimeoutSeconds > 0 {
			timeout = time.Duration(lvl.Setup.TimeoutSeconds) * time.Second
		}
		if err := r.runScript(ctx, lvl.Setup.Script, root, timeout, "run the level setup script"); err != nil {
			return r.rollback(ctx, lvl, err)
		}
	}

	if err := r.writeSentinel(ctx, lvl.ID); err != nil {
		return r.rollback(ctx, lvl, err)
	}
	return nil
}

// rollback runs Teardown on a context built with context.WithoutCancel plus
// a bounded timeout, so a Ctrl-C at the moment of failure still cleans up.
// The returned error is the original failure; a rollback that also fails is
// attached with errors.Join so neither is lost.
func (r *Runner) rollback(ctx context.Context, lvl *content.Level, cause error) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
	defer cancel()
	if err := r.Teardown(cleanupCtx, lvl); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

// buildManifest validates lvl's setup.files and builds the FileEntry list
// Setup pushes in one call. It touches only host memory and r.packFS:
// nothing here reaches the sandbox.
func (r *Runner) buildManifest(lvl *content.Level, root string) ([]runtime.FileEntry, error) {
	if !platform.ValidIdentifier(lvl.ID) {
		return nil, ux.Fail("read the level definition",
			fmt.Errorf("level id %q does not match %s", lvl.ID, platform.IdentifierPattern),
			remediationLevelPackInvalid, "level-pack-invalid")
	}

	entries := make([]runtime.FileEntry, 0, len(lvl.Setup.Files))
	total := 0
	for _, spec := range lvl.Setup.Files {
		entry, err := r.buildFileEntry(spec, root)
		if err != nil {
			return nil, err
		}
		total += len(entry.Content)
		if total > maxManifestBytes {
			return nil, ux.Fail("read the level assets",
				fmt.Errorf("the level's staged assets exceed %d bytes: %w", maxManifestBytes, ErrInvalidLevelPack),
				remediationLevelPackInvalid, "level-pack-invalid")
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// buildFileEntry validates one FileSpec and produces the FileEntry Setup
// will push. Carriage returns are stripped here, on the host side, before
// the content ever reaches a runtime.FileEntry: Session.PushFiles keeps its
// own strip as a backstop for every other caller, not removed by this.
func (r *Runner) buildFileEntry(spec content.FileSpec, root string) (runtime.FileEntry, error) {
	set := 0
	if spec.Source != "" {
		set++
	}
	if spec.Content != "" {
		set++
	}
	if spec.Generate != nil {
		set++
	}
	if set != 1 {
		return runtime.FileEntry{}, ux.Fail("read the level definition",
			fmt.Errorf("file %q must set exactly one of source, content, or generate, found %d: %w", spec.Path, set, ErrInvalidLevelPack),
			remediationLevelPackInvalid, "level-pack-invalid")
	}

	if spec.Path == "" || path.IsAbs(spec.Path) {
		return runtime.FileEntry{}, ux.Fail("read the level definition",
			fmt.Errorf("file path %q must be relative to setup.root and non-empty: %w", spec.Path, ErrInvalidLevelPack),
			remediationLevelPackInvalid, "level-pack-invalid")
	}
	for _, seg := range strings.Split(spec.Path, "/") {
		if seg == ".." {
			return runtime.FileEntry{}, ux.Fail("read the level definition",
				fmt.Errorf("file path %q escapes setup.root: %w", spec.Path, ErrInvalidLevelPack),
				remediationLevelPackInvalid, "level-pack-invalid")
		}
	}
	abs := path.Join(root, path.Clean(spec.Path))
	if abs == root || !strings.HasPrefix(abs, root+"/") {
		return runtime.FileEntry{}, ux.Fail("read the level definition",
			fmt.Errorf("file path %q escapes or is equal to setup.root: %w", spec.Path, ErrInvalidLevelPack),
			remediationLevelPackInvalid, "level-pack-invalid")
	}

	var body []byte
	switch {
	case spec.Source != "":
		if !fs.ValidPath(spec.Source) {
			return runtime.FileEntry{}, ux.Fail("read the level assets",
				fmt.Errorf("source %q is not a valid pack-relative path: %w", spec.Source, ErrInvalidLevelPack),
				remediationLevelPackInvalid, "level-pack-invalid")
		}
		data, err := fs.ReadFile(r.packFS, spec.Source)
		if err != nil {
			return runtime.FileEntry{}, ux.Fail("read the level assets",
				fmt.Errorf("read source %q: %w: %v", spec.Source, ErrInvalidLevelPack, err),
				remediationLevelPackInvalid, "level-pack-invalid")
		}
		body = data
	case spec.Generate != nil:
		data, err := Generate(spec.Generate.Kind, spec.Generate.Seed, spec.Generate.Lines)
		if err != nil {
			return runtime.FileEntry{}, ux.Fail("generate the level assets",
				fmt.Errorf("generate %q for %q: %w", spec.Generate.Kind, spec.Path, err),
				remediationLevelPackInvalid, "level-pack-invalid")
		}
		body = data
	default:
		body = []byte(spec.Content)
	}
	body = stripCRLF(body)

	mode := DefaultMode
	if spec.Mode != "" {
		v, err := strconv.ParseUint(spec.Mode, 8, 32)
		if err != nil {
			return runtime.FileEntry{}, ux.Fail("read the level definition",
				fmt.Errorf("mode %q on file %q does not parse as octal: %w: %v", spec.Mode, spec.Path, ErrInvalidLevelPack, err),
				remediationLevelPackInvalid, "level-pack-invalid")
		}
		if v > 0o777 {
			return runtime.FileEntry{}, ux.Fail("read the level definition",
				fmt.Errorf("mode %q on file %q has bits set above 0o777: %w", spec.Mode, spec.Path, ErrInvalidLevelPack),
				remediationLevelPackInvalid, "level-pack-invalid")
		}
		mode = fs.FileMode(v)
	}

	owner := DefaultOwner
	if spec.Owner != "" {
		if !ownerPattern.MatchString(spec.Owner) {
			return runtime.FileEntry{}, ux.Fail("read the level definition",
				fmt.Errorf("owner %q on file %q does not match %s: %w", spec.Owner, spec.Path, ownerPattern.String(), ErrInvalidLevelPack),
				remediationLevelPackInvalid, "level-pack-invalid")
		}
		owner = spec.Owner
	}

	return runtime.FileEntry{Path: abs, Content: body, Mode: mode, Owner: owner}, nil
}

// stripCRLF rewrites only the \r in a \r\n pair, leaving a lone CR
// untouched. This is strictly safer than an unconditional strip for a
// binary asset that happens to contain the byte 0x0d with no following
// 0x0a. Neither this nor Session.PushFiles's own strip is safe for a binary
// asset containing the exact bytes 0d 0a; v0.1 has no binary asset concept.
//
// TODO(v0.2): a binary flag on FileSpec so a fixture that is not text is
// pushed byte-for-byte. Fixing PushFiles itself is not this package's
// business.
func stripCRLF(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
}
