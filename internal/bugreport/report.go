package bugreport

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/doctor"
	"github.com/JoottunAtish/ShellForge/internal/journal"
	"github.com/JoottunAtish/ShellForge/internal/store"
)

// DefaultJournalLimit is how many of the most recent journal entries a
// bundle carries when Sources.JournalLimit is zero.
const DefaultJournalLimit = 500

// MaxLogBytes is how much of one log file a bundle carries, from the end.
const MaxLogBytes = 1 << 20 // 1 MB

// MaxZipBytes is the cap Write compares the bundle's UNCOMPRESSED content
// against: README.txt, report.json, doctor.txt, and every *.log file's
// bytes, summed before zip.Writer ever compresses anything. It is not a
// cap on the zip file's own size on disk, which is smaller. If the sum
// exceeds this, Write's only remedy is dropping the logs (Report.Journal
// and everything else still ships in full), and it records a Note saying
// so; nothing bounds the result further today, because nothing in this
// tree writes to platform.LogDir() yet and the journal alone, even at
// DefaultJournalLimit entries, does not get close to this cap in
// practice. A second truncation stage for Report.Journal itself is a
// reasonable future addition, not something this comment should imply
// already exists.
const MaxZipBytes = 5 << 20 // 5 MB

// The Note wording Collect and Write produce. Each names what was missing
// or what failed, and why that is normal, per doc.go.
const (
	noteNoRuntime             = "no runtime was resolved, so the bundle carries no sandbox state. This is normal when Docker is not running, which is the most common thing being reported."
	noteSandboxProbeFailedFmt = "the sandbox probe failed: %s. The bundle carries no sandbox state."
	noteNoPack                = "no content pack was supplied, so the bundle carries no pack identity."
	noteNoDatabase            = "no progress database was found, so the bundle carries no progress. This is normal on a first run."
	noteProgressFailedFmt     = "reading the progress database failed: %s. The bundle carries no progress."
	noteJournalNotIncluded    = "the command journal was not included. Run bug-report --journal to include it, with secrets redacted."
	noteJournalNoDatabase     = "--journal was passed, but no progress database was found, so the bundle carries no commands."
	noteJournalFailedFmt      = "reading the command journal failed: %s. The bundle carries no commands."
	noteLogsDroppedFmt        = "the logs were dropped to keep the bundle under %d bytes."
)

// Build is the binary's own identity, passed down from package main because
// an L4 package cannot reach main's ldflag variables.
type Build struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
}

// Sandbox is what the caller could learn about the sandbox.
type Sandbox struct {
	Backend     string          `json:"backend"`
	Detail      string          `json:"detail"`
	Provisioned bool            `json:"provisioned"`
	Running     bool            `json:"running"`
	Caps        map[string]bool `json:"caps,omitempty"`
}

// Prober is the narrow view of a sandbox this package needs.
type Prober interface {
	Probe(ctx context.Context) (Sandbox, error)
}

// Pack identifies the content pack the binary shipped with.
type Pack struct {
	ID         string `json:"id"`
	Version    string `json:"version"`
	ContentAPI int    `json:"content_api"`
	Levels     int    `json:"levels"`
}

// Progress is a count-only summary of the learner's progress database.
type Progress struct {
	SchemaVersion int  `json:"schema_version"`
	Passed        int  `json:"passed"`
	Skipped       int  `json:"skipped"`
	Attempted     int  `json:"attempted"`
	TotalXP       int  `json:"total_xp"`
	DatabaseFound bool `json:"database_found"`
}

// Command is one journal entry, with Text already through journal.Redact.
type Command struct {
	Seq        int64     `json:"seq"`
	TS         time.Time `json:"ts"`
	LevelID    string    `json:"level_id"`
	Cwd        string    `json:"cwd"`
	Text       string    `json:"text"`
	Exit       int       `json:"exit"`
	DurationMS int64     `json:"duration_ms"`
}

// Report is the whole document. It marshals to report.json inside the zip.
type Report struct {
	GeneratedAt     time.Time     `json:"generated_at"`
	Build           Build         `json:"build"`
	Doctor          doctor.Report `json:"doctor"`
	Sandbox         *Sandbox      `json:"sandbox"`
	Pack            Pack          `json:"pack"`
	Progress        Progress      `json:"progress"`
	Journal         []Command     `json:"journal"`
	JournalIncluded bool          `json:"journal_included"`
	JournalRedacted int           `json:"journal_redacted"`
	Notes           []string      `json:"notes"`
}

// Sources is everything Collect needs. Every field is optional.
type Sources struct {
	Build          Build
	Doctor         doctor.Report
	Prober         Prober
	Pack           *content.Pack
	Store          *store.Store
	ProfileID      int64
	IncludeJournal bool
	JournalLimit   int // 0 means the default, DefaultJournalLimit
}

// Collect gathers everything into a Report. It never returns an error for a
// missing source; it records a Note and carries on.
//
// The error return exists for a genuinely impossible state, and there is no
// such state today, so Collect always returns a nil error. The signature
// keeps the return anyway: it is the issue's contract, and a future source
// this package gathers could need it.
func Collect(ctx context.Context, s Sources) (Report, error) {
	r := Report{
		GeneratedAt: time.Now().UTC(),
		Build:       s.Build,
		Doctor:      s.Doctor,
	}

	collectSandbox(ctx, &r, s)
	collectPack(&r, s)
	collectProgress(ctx, &r, s)
	collectJournal(ctx, &r, s)

	scrubReport(&r)
	return r, nil
}

// collectSandbox fills Report.Sandbox by asking s.Prober, recording a Note
// instead of failing when the prober is absent or errors.
func collectSandbox(ctx context.Context, r *Report, s Sources) {
	if s.Prober == nil {
		r.Notes = append(r.Notes, noteNoRuntime)
		return
	}
	sb, err := s.Prober.Probe(ctx)
	if err != nil {
		r.Notes = append(r.Notes, fmt.Sprintf(noteSandboxProbeFailedFmt, err.Error()))
		return
	}
	cp := sb
	r.Sandbox = &cp
}

// collectPack fills Report.Pack from s.Pack, recording a Note when no pack
// was supplied.
func collectPack(r *Report, s Sources) {
	if s.Pack == nil {
		r.Notes = append(r.Notes, noteNoPack)
		return
	}
	r.Pack = Pack{
		ID:         s.Pack.ID,
		Version:    s.Pack.Version,
		ContentAPI: s.Pack.ContentAPI,
		Levels:     len(s.Pack.Levels),
	}
}

// collectProgress fills Report.Progress from s.Store, recording a Note when
// no store was supplied or when reading it failed.
func collectProgress(ctx context.Context, r *Report, s Sources) {
	if s.Store == nil {
		r.Notes = append(r.Notes, noteNoDatabase)
		return
	}
	packID := ""
	if s.Pack != nil {
		packID = s.Pack.ID
	}
	prog, err := readProgress(ctx, s.Store, s.ProfileID, packID)
	if err != nil {
		r.Notes = append(r.Notes, fmt.Sprintf(noteProgressFailedFmt, err.Error()))
		return
	}
	r.Progress = prog
}

// readProgress reads a count-only summary of profileID's progress within
// packID: the schema version, the level_state rows' status counts, and the
// total XP. It never reads a command, output, or the database file itself.
func readProgress(ctx context.Context, s *store.Store, profileID int64, packID string) (Progress, error) {
	ver, err := s.SchemaVersion(ctx)
	if err != nil {
		return Progress{}, fmt.Errorf("read schema version: %w", err)
	}
	states, err := s.LevelStates(ctx, profileID, packID)
	if err != nil {
		return Progress{}, fmt.Errorf("read level states: %w", err)
	}
	xp, err := s.TotalXP(ctx, profileID, packID)
	if err != nil {
		return Progress{}, fmt.Errorf("read total xp: %w", err)
	}

	var passed, skipped, attempted int
	for _, st := range states {
		switch st.Status {
		case store.StatusPassed:
			passed++
		case store.StatusSkipped:
			skipped++
		}
		if st.Attempts > 0 {
			attempted++
		}
	}

	return Progress{
		SchemaVersion: ver,
		Passed:        passed,
		Skipped:       skipped,
		Attempted:     attempted,
		TotalXP:       xp,
		DatabaseFound: true,
	}, nil
}

// collectJournal fills Report.Journal, Report.JournalRedacted, and
// Report.JournalIncluded when s.IncludeJournal is set, redacting every
// entry's text with journal.Redact. Without --journal, no command text is
// ever read at all: the Note alone explains why. JournalIncluded is set
// from whether anything was actually read, not from the flag alone, so a
// bundle never claims "journal_included": true while carrying zero commands
// and no Note explaining the gap: every branch that leaves Report.Journal
// empty records a Note saying why, the same contract collectProgress and
// collectPack already keep for their own optional sources.
func collectJournal(ctx context.Context, r *Report, s Sources) {
	if !s.IncludeJournal {
		r.Notes = append(r.Notes, noteJournalNotIncluded)
		return
	}
	if s.Store == nil {
		r.Notes = append(r.Notes, noteJournalNoDatabase)
		return
	}

	limit := s.JournalLimit
	if limit <= 0 {
		limit = DefaultJournalLimit
	}
	entries, err := recentCommands(ctx, s.Store, limit)
	if err != nil {
		r.Notes = append(r.Notes, fmt.Sprintf(noteJournalFailedFmt, err.Error()))
		return
	}

	cmds := make([]Command, len(entries))
	redacted := 0
	for i, e := range entries {
		text, changed := journal.Redact(e.Text)
		if changed {
			redacted++
		}
		e.Text = text
		cmds[i] = e
	}
	r.Journal = cmds
	r.JournalRedacted = redacted
	r.JournalIncluded = true
}

// recentCommands reads the most recent limit rows of the events table,
// oldest first, directly through s.DB(): this package has no Journal handle
// to read through, since Sources carries a *store.Store rather than a
// *journal.Journal, and journal.Journal's own Commands method answers only
// one level or only command text, neither of which keeps every field
// Command needs. The query and the column list mirror
// internal/journal.Journal.Level and internal/journal.Journal.Commands, its
// scope.LastN case, which read the same table the same way.
func recentCommands(ctx context.Context, s *store.Store, limit int) ([]Command, error) {
	rows, err := s.DB().QueryContext(ctx,
		`SELECT seq, ts, level_id, cwd, raw, exit_code, duration_ms FROM (
			SELECT seq, ts, level_id, cwd, raw, exit_code, duration_ms, id
			FROM events ORDER BY id DESC LIMIT ?
		) ORDER BY id ASC`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query the command journal: %w", err)
	}
	defer rows.Close()

	var out []Command
	for rows.Next() {
		var (
			c  Command
			ts float64
		)
		if err := rows.Scan(&c.Seq, &ts, &c.LevelID, &c.Cwd, &c.Text, &c.Exit, &c.DurationMS); err != nil {
			return nil, fmt.Errorf("scan the command journal: %w", err)
		}
		c.TS = time.UnixMicro(int64(math.Round(ts * 1e6))).UTC()
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the command journal: %w", err)
	}
	return out, nil
}

// scrubReport rewrites the host home directory prefix to "~" everywhere it
// could appear in r: every Note, every Command's Cwd and Text, every doctor
// Result's Detail and Remediation, and the sandbox Detail. Remediation is
// the other free-text field on doctor.Result, marshalled into report.json
// right alongside Detail, so it gets the same treatment: nothing in this
// codebase's doctor probes writes a path into a remediation string today,
// which is what let this gap sit unnoticed, but the invariant this
// function keeps ("every host home directory prefix is rewritten", per
// doc.go) should hold because this function enforces it, not because every
// caller happens to avoid the shape that would break it.
//
// os.UserHomeDir is resolved exactly once here rather than inside a
// per-string helper: a bundle with a full journal can carry on the order of
// a thousand strings across Notes, Journal entries, and Doctor results, and
// a syscall per string was the wrong price for a lookup that never changes
// mid-run.
func scrubReport(r *Report) {
	home, err := os.UserHomeDir()
	if err != nil || len(home) <= 1 {
		return
	}
	for i := range r.Notes {
		r.Notes[i] = scrubHome(r.Notes[i], home)
	}
	for i := range r.Doctor.Results {
		r.Doctor.Results[i].Detail = scrubHome(r.Doctor.Results[i].Detail, home)
		r.Doctor.Results[i].Remediation = scrubHome(r.Doctor.Results[i].Remediation, home)
	}
	if r.Sandbox != nil {
		r.Sandbox.Detail = scrubHome(r.Sandbox.Detail, home)
	}
	for i := range r.Journal {
		r.Journal[i].Cwd = scrubHome(r.Journal[i].Cwd, home)
		r.Journal[i].Text = scrubHome(r.Journal[i].Text, home)
	}
}

// scrubHome rewrites every occurrence of home in s to "~". home is the
// host home directory, resolved once by scrubReport and passed down so
// this stays a plain string replacement with no syscall of its own.
func scrubHome(s, home string) string {
	return strings.ReplaceAll(s, home, "~")
}
