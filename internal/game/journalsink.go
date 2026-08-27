package game

import (
	"context"
	"fmt"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/game/bus"
	"github.com/JoottunAtish/ShellForge/internal/journal"
)

// JournalSink drains the in-sandbox command journal into the progress
// database and onto the event bus.
//
// It is the join between three pieces that already existed separately: the
// TSV images/rc/instrument.bash writes inside the sandbox, the events table
// internal/journal owns, and the CommandExecuted event the scoring and
// achievement subscribers wait for. Layer 4 is the lowest place that can hold
// all three, because a Collector needs a runtime.Session and the bus lives
// here.
//
// A JournalSink is not wired into the orchestrator. Whoever wires it owns
// calling Drain; see Drain for when.
type JournalSink struct {
	c *journal.Collector
	j *journal.Journal
	b *bus.Bus
}

// NewJournalSink returns a sink that reads through c, records through j, and
// publishes on b.
func NewJournalSink(c *journal.Collector, j *journal.Journal, b *bus.Bus) *JournalSink {
	return &JournalSink{c: c, j: j, b: b}
}

// Drain reads everything new out of the sandbox journal, appends it to the
// store, and publishes one CommandExecuted per entry, in journal order,
// carrying levelID and attemptID.
//
// Call it before a check and once at teardown, not on a timer: the learner's
// commands are only interesting at the moment something reads them, and a
// poll would pull a file out of the sandbox for every idle second of a level.
// Successive calls are incremental, so a command is appended exactly once
// however many times Drain runs.
//
// A journal that cannot be read, or that does not exist yet, is not a
// failure: Drain reports no error and records no commands, so a
// learner whose journal was lost sees an efficiency bonus of zero rather than
// a check that will not run. Nothing in this path reaches ux.Fail and nothing
// in it is a reason to fail a level. The error Drain does return is a failure
// to write the progress database, which is a host-side fault worth
// surfacing to whoever wired the sink up. That one case does lose the rest of
// the batch it was part of, because the read side has already moved past
// those records: a store that cannot be written has ended the session's
// record keeping either way, and buying a retry would mean holding the
// learner's command text in a second place to hold it in.
func (s *JournalSink) Drain(ctx context.Context, levelID string, attemptID int64) error {
	entries, err := s.c.Since(ctx, levelID)
	if err != nil {
		// Deliberately swallowed, and deliberately not logged: the failure
		// is already described by Collector.Since's contract, the learner
		// can do nothing about it, and this package must not print anything
		// derived from a journal read.
		return nil
	}

	for _, e := range entries {
		if err := s.j.Append(ctx, e); err != nil {
			// e is not in this error, and must never be: an Entry reached
			// through an unexported field cannot redact its own Raw, so the
			// safe rule is that an Entry never meets a format verb here at
			// all. Seq identifies the record well enough to debug with.
			return fmt.Errorf("record command %d of level %q: %w", e.Seq, levelID, err)
		}

		s.b.Publish(ctx, bus.CommandExecuted{
			LevelID:   levelID,
			AttemptID: attemptID,
			Seq:       e.Seq,
			Raw:       e.Raw,
			ExitCode:  e.Exit,
			Cwd:       e.Cwd,
			Duration:  time.Duration(e.DurationMS) * time.Millisecond,
			// UsedTab and UsedHistory are always false on this path today,
			// and that is a known gap rather than an oversight. The TSV
			// carries neither: a tab tap is a host-side observation of the
			// learner's keystrokes, which internal/pty makes on
			// CommandEvent.UsedTab, and joining that stream to these records
			// needs a correlation this ticket deliberately does not invent,
			// because matching an OSC event to a TSV row by index drifts the
			// moment either side drops one. Duration is zero for the same
			// reason: instrument.bash writes four fields and none of them is
			// a duration. Named plainly, because it is half of an acceptance
			// criterion rather than a nicety: issue #123 asks the events
			// table to hold the real command text, exit codes, cwd AND
			// durations, and this path delivers the first three only.
			// TestDrainPublishesAZeroDurationForEveryTSVRecord pins the zero
			// so that filling it in has to be a deliberate change to an
			// assertion rather than something that happens by accident.
			// TODO(v0.2): correlate CommandEvent with the TSV record by
			// something durable, and fill UsedTab, UsedHistory and Duration
			// from it. The tab_master and manual_labour achievements need it;
			// nothing that scores a level may.
			UsedTab:     e.UsedTab,
			UsedHistory: e.UsedHistory,
			At:          e.TS,
		})
	}
	return nil
}
