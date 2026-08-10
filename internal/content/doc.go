// Package content loads and validates level packs.
//
// Layer L3. May import internal/verify, internal/runtime, and below.
//
// Levels are declarative YAML, specified in docs/LEVEL-FORMAT.md. The validator
// enforces the authoring invariants listed there, and its error messages must
// name the file, the level id, and the field.
//
// Pack-supplied paths are untrusted input. A `source:` must resolve inside the
// pack directory, and a `path:` must resolve under setup.root, which must itself
// be under /home/learner/. Reject traversal, do not sanitize it.
//
// Strip carriage returns from every file materialized into the sandbox.
package content
