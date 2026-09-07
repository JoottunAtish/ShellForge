// Package score computes what passing a level awards.
//
// Layer 4. It imports the standard library and nothing else, not even
// internal/content: Score takes primitives rather than a *content.Level so
// the whole formula is testable with a table and no fixtures. It must not
// import internal/store, internal/content, or any runtime package.
//
// # The formula
//
// One multiplicative stage, then four additive terms, in this order:
//
//	base       = the level's authored XP, floored at zero
//	scaled     = round(base * difficultyMultiplier[difficulty])
//	efficiency = round(base * 0.25)   when par is set and commands used is at or under it
//	first try  = round(base * 0.10)   when this attempt passed on the first check
//	bonus      = 10 per optional objective satisfied
//	hints      = minus the authored cost of every hint taken
//
// Rounding happens twice per term at most and never twice on the same
// number: the multiplicative stage is rounded once, at its end, and each
// additive fraction is rounded once as it is computed, so every
// Breakdown.Delta is a whole number and their sum is the total exactly.
// math.Round is half away from zero, which is the rule this package
// specifies rather than banker's rounding.
//
// # Hints subtract, they do not multiply
//
// The design record disagrees with itself here, so this comment records
// which side won and why rather than leaving the next reader to find the
// other one and assume the code is wrong.
//
// ARCHITECTURE section 4.11 writes the hint penalty multiplicatively, as
// "x (1 - hint_penalty)". SESSION-PROMPTS Day 4 Session G item 5 writes it
// subtractively, as "- sum(hint costs taken)". The level format settles it:
// content.Hint.Cost is authored in XP, as a whole number, not as a
// fraction, so subtraction is the only reading consistent with the data a
// level actually carries. A multiplicative reading would have to invent a
// conversion from "5 XP" to "some fraction", and every level in the pack
// would silently mean something the author did not write.
//
// # The award never goes below zero
//
// The whole product thesis is a beginner who is afraid of breaking
// something. A level that ends with "you earned -15 XP" is the exact
// feeling this game exists to avoid. So the hint line is clamped to
// whatever was earned above it rather than the total being clamped after
// the fact: that keeps sum(Breakdown) == Total true in every case,
// including the floored one, so the banner can print the arithmetic and
// have it add up in front of the learner.
//
// # No time bonus
//
// ARCHITECTURE 4.11 includes one and immediately qualifies it: "only above
// a generous threshold; never punish thinking", and "never let time
// pressure be mandatory, it is a per-mode toggle". A beginner staring at a
// prompt for four minutes is the target user having a normal experience.
// Building the toggle is more work than the bonus is worth, so the input
// does not exist and the formula has one less way to feel unfair.
//
// # Nothing here trusts a journal exit code
//
// Per issue #23, a learner can forge a journal record from inside the
// sandbox. Inputs therefore has no exit code field and no journal field.
// CommandsUsed is a count, it can only ever add a bonus, and it can never
// make a level unpassable, which is the property that makes it safe to
// derive from data the learner influences.
package score
