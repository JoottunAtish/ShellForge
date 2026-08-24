package game

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/content/setup"
	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/verify"
)

// One level in play, and deliberately nothing more.
//
// This is the minimum orchestration `shellforge run` needs: load, set up, hand
// out the briefing, answer `check`, tear down. The full Session Orchestrator,
// with the event bus, scoring, the hint ladder and unlock state, is Day 4 task
// 4.2. Everything that would need the progress database is absent, and the
// absences are listed on Session rather than left to be discovered.

// Doc anchors and their remediations, kept together so the wording a learner
// sees and the heading in docs/05-troubleshooting.md cannot drift apart.
const (
	docAnchorLevelPackInvalid = "level-pack-invalid"

	remediationLevelPackInvalid = "This is a problem with the level, not with what you typed. Run `shellforge author validate packs/core-linux-basics` to see every problem in the pack at once."
)

// Verifier is the verification engine, as this package uses it.
//
// It is declared here, in the consumer, rather than taken as *verify.Engine.
// That is the same pattern internal/content uses for TypeChecker and
// internal/verify uses for JournalReader, and it buys the thing those two buy:
// a Session can be built in a unit test with a two-method fake, so the
// orchestration is testable without a registry, a sandbox, or a real level.
type Verifier interface {
	// Build turns specs into runnable checks, once, at load.
	Build(specs []verify.Spec) ([]verify.Check, error)

	// Run evaluates checks and assembles the level result.
	Run(ctx context.Context, checks []verify.Check, env verify.Env) verify.LevelResult
}

// Config is everything NewSession needs. Every field is required except Journal
// and StateDir.
type Config struct {
	// Level is the level being played, already loaded and validated.
	Level *content.Level

	// Sess is an open session on the sandbox. The caller opens it and the
	// caller closes it: a Session borrows it and never takes ownership, so
	// that one sandbox session can outlive one level.
	Sess runtime.Session

	// PackFS is the level pack's filesystem, rooted AT the pack directory, so
	// that a level's `source: assets/app-1.log` resolves against it directly.
	//
	// This is a seam worth stating explicitly because getting it wrong is
	// silent until a level actually uses `source:`. setup.Runner reads assets
	// with fs.ReadFile(packFS, spec.Source) and joins no pack root of its own,
	// while packs.FS is rooted one level higher and its paths begin with the
	// pack directory name. A caller passes fs.Sub(packs.FS, packs.CoreLinuxBasics),
	// never packs.FS.
	PackFS fs.FS

	// Verifier builds and runs the level's checks.
	Verifier Verifier

	// Journal answers a journal check's questions about command history. Nil
	// means a reader that reports no commands, which is the default today.
	//
	// Issue #88 moved Scope down to internal/scope, so journal.Journal now
	// satisfies verify.JournalReader outright. Nothing wires a real journal
	// into internal/game yet, though; that remains a separate piece of work.
	// Until it lands a no-op reader is safe, because the pack validator
	// requires every journal check to be optional or severity: warn, so a
	// reader that reports nothing can only fail to award a bonus objective.
	// It can never gate passing.
	Journal verify.JournalReader

	// StateDir overrides SF_STATE. Empty means setup.DefaultStateDir.
	StateDir string
}

// Session is one level in play.
//
// What it deliberately does NOT do, so the absences read as decisions: no
// scoring, no XP, no hint ladder or hint cost accounting, no prerequisite
// gating, no unlock state, no achievements, no event bus, and no writes to the
// progress database. It also does not resolve or provision a runtime: it is
// handed an open session, because internal/game may never import a runtime
// implementation and internal/archtest enforces that.
type Session struct {
	level    *content.Level
	sess     runtime.Session
	runner   *setup.Runner
	verifier Verifier
	checks   []verify.Check
	journal  verify.JournalReader
}

// noJournal reports no commands, ever.
//
// It exists so that Env.Journal is never nil: a journal check calling
// Commands on a nil interface would panic inside a check, which the engine
// would report as StatusError with a Go panic message in front of a learner.
// Reporting "no commands ran" is both true, given nothing populates the journal
// yet, and harmless, given a journal check can only award a bonus.
type noJournal struct{}

func (noJournal) Commands(verify.Scope) []string { return nil }

// NewSession prepares a level for play.
//
// It does every piece of work that can fail without touching the sandbox, and
// builds the level's checks once. Building here rather than on each Check call
// is what moves a malformed parameter from the learner's first `check` to level
// load, where it is the author's problem rather than a mystery in the middle of
// somebody's session.
func NewSession(cfg Config) (*Session, error) {
	switch {
	case cfg.Level == nil:
		return nil, errors.New("game: Config.Level is nil")
	case cfg.Sess == nil:
		return nil, errors.New("game: Config.Sess is nil")
	case cfg.Verifier == nil:
		return nil, errors.New("game: Config.Verifier is nil")
	}
	// PackFS is not checked here. A level with no `source:` entry never reads
	// from it, and refusing one would make a nil pack filesystem an error for
	// every level rather than for the ones that need it. setup.Runner reports
	// the real problem, naming the asset, for a level that does read.

	var opts []setup.Option
	if cfg.StateDir != "" {
		opts = append(opts, setup.WithStateDir(cfg.StateDir))
	}

	specs, err := verifySpecs(cfg.Level.Checks, cfg.Level.Objectives)
	if err != nil {
		return nil, err
	}

	checks, err := cfg.Verifier.Build(specs)
	if err != nil {
		return nil, ux.Fail(
			fmt.Sprintf("build the checks for level %q", cfg.Level.ID),
			err,
			remediationLevelPackInvalid,
			docAnchorLevelPackInvalid,
		)
	}

	journal := cfg.Journal
	if journal == nil {
		journal = noJournal{}
	}

	return &Session{
		level:    cfg.Level,
		sess:     cfg.Sess,
		runner:   setup.NewRunner(cfg.Sess, cfg.PackFS, opts...),
		verifier: cfg.Verifier,
		checks:   checks,
		journal:  journal,
	}, nil
}

// LevelID returns the level's id.
func (s *Session) LevelID() string { return s.level.ID }

// Level returns the level being played.
//
// It returns the pointer it was given rather than a copy. content.Embedded
// decodes the pack afresh on every call, so a caller already holds a private
// Level, and copying here would only make Session and its caller disagree about
// which one is authoritative.
func (s *Session) Level() *content.Level { return s.level }

// Root returns the level's world inside the sandbox, its setup.root.
func (s *Session) Root() string { return s.level.Setup.Root }

// StateDir returns the effective SF_STATE.
//
// A caller threads this same value into runtime.SessionSpec.StateDir, the
// SF_STATE environment variable it gives the learner's shell, and the paths of
// the control channel FIFOs. That is how the runner, the shell instrumentation
// and the control channel stay in step.
func (s *Session) StateDir() string { return s.runner.StateDir() }

// Briefing returns the level's briefing as authored, in markdown.
//
// Unrendered on purpose. Rendering markdown to ANSI needs to know the terminal
// width and whether colour is wanted, which are properties of the learner's
// terminal, and this package is four layers away from it.
func (s *Session) Briefing() string { return s.level.Briefing }

// Objectives returns the level's objective checklist as authored.
func (s *Session) Objectives() []content.Objective { return s.level.Objectives }

// Setup materializes the level's world.
//
// It tears down first, unconditionally, which is what makes it safe to call
// against a level root a previous run left dirty. setup.Runner owns that
// behaviour and the transactional rollback that goes with it; every error it
// returns is already a *ux.Error carrying the right doc anchor, so this method
// passes them through untouched rather than replacing a specific anchor with a
// vaguer one.
func (s *Session) Setup(ctx context.Context) error {
	return s.runner.Setup(ctx, s.level)
}

// Teardown removes the level's world.
//
// Safe to call more than once, and safe to call when Setup failed halfway or
// never ran: setup.Runner.Teardown is idempotent. A caller is expected to defer
// it before calling Setup for exactly that reason.
func (s *Session) Teardown(ctx context.Context) error {
	return s.runner.Teardown(ctx, s.level)
}

// IsSetUp reports whether this level's world is already in place.
func (s *Session) IsSetUp(ctx context.Context) (bool, error) {
	return s.runner.IsSetUp(ctx, s.level)
}

// Check runs every check and returns the result.
//
// It returns the engine's LevelResult verbatim and decides nothing about it.
// Which failure to show, how to render a pass, what colour anything is: all of
// that belongs to whoever is talking to the learner's terminal.
//
// The error return is for a run that could not be attempted at all, which is
// nothing today: the engine reports a cancelled or broken run inside the result
// so the caller always has a checklist to show. It is in the signature because
// a caller must not have to change shape when the first such failure appears,
// and because the alternative, a bare LevelResult, invites a caller to assume
// success is the only outcome.
func (s *Session) Check(ctx context.Context) (verify.LevelResult, error) {
	return s.verifier.Run(ctx, s.checks, verify.Env{
		Session:   s.sess,
		Root:      s.level.Setup.Root,
		Snapshots: verify.Snapshots{Dir: s.runner.StateDir()},
		Journal:   s.journal,
		LevelID:   s.level.ID,
	}), nil
}
