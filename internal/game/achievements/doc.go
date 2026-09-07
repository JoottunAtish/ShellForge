// Package achievements awards badges by watching the event bus.
//
// Layer 4. It may import internal/game/bus, internal/store and
// internal/content. It must not import any runtime package and nothing from
// cmd/.
//
// # The point of this package is what it does not touch
//
// ARCHITECTURE 4.5 claims the event bus is "what makes game features
// pluggable". This package is the test of that claim: fourteen achievement
// keys, every one of them a bus subscriber, and not one line of
// internal/game/orchestrator.go changed to add them. If adding them had
// required an orchestrator edit, the claim was false and finding that out
// on Day 4 would have been the useful outcome instead.
//
// Adding a fifteenth achievement is one entry in the slice RulesFor returns
// and one test case. Nothing else.
//
// # Nothing here may gate passing
//
// Achievements are cosmetic, and that is what makes it safe for two of them
// to read data a learner can forge. tab_master and manual_labour are
// derived from the command journal, which a learner can write to from
// inside the sandbox during a permissions level. Per issue #23 the only
// consequence of a forged one is a badge somebody awarded themselves. So no
// rule writes to level_state.status, no rule influences a score, and no
// rule can make a level unpassable.
//
// # Command text is matched, never kept
//
// manual_labour reads bus.CommandExecuted.Raw, which internal/journal/doc.go
// classifies as secret material: a learner types passwords and tokens on
// command lines. It is compared against and discarded in the same
// expression. Nothing here stores it, logs it, puts it in an error, or
// keeps a substring of it, and the counter that survives is an integer.
//
// # Counters are integers hiding in a float64
//
// achievement.progress is a REAL column and Rule.Progress carries a
// float64, so several rules pack small integers into one. Every such
// encoding is exact: a float64 represents whole numbers below 2^53 without
// loss, and every value used here is far below that. Where a rule needs to
// remember a set of levels rather than a count, it uses a bit per level and
// refuses to track a pack too large for the encoding rather than
// miscounting one, which is the failure the tests pin.
package achievements
