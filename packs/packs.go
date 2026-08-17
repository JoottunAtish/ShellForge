// Package packs embeds the content packs that ship inside the binary.
//
// It exists here, next to the pack directories, only because Go's embed
// directive cannot reach outside the package it is written in: internal/content
// owns pack loading, but it cannot embed packs/core-linux-basics from three
// directories away. This package is therefore data and nothing else. It
// declares one variable, imports nothing but embed, and has no behaviour, so
// it sits at layer 0 in internal/archtest and any layer may read from it.
//
// The single-binary promise in README.md is what makes this necessary. A
// learner who downloads one executable gets the levels with it; there is no
// pack directory to install and no download to go wrong. CLAUDE.md bans remote
// pack downloading outright for the same reason.
package packs

import "embed"

// FS holds every shipped pack, rooted at this directory, so that
// FS's paths begin with the pack directory name.
//
// The all: prefix matters and is not decoration. Without it, embed silently
// skips every file whose name begins with a dot or an underscore. No shipped
// asset needs that today: nav-02's own dotfile, .kofi-notes, is inline
// content in the level YAML, not a pack asset. But a level that teaches
// `ls -a` may need a real dotfile asset to find, and a pack that embedded
// everything except its hidden files would fail at play time, in a level
// about hidden files, which is the worst possible place to discover a
// build-time exclusion rule. The prefix stays as the defensive default.
//
//go:embed all:core-linux-basics
var FS embed.FS

// CoreLinuxBasics is the directory name of the pack that ships by default. It
// is the root to hand to content.LoadPack when reading from FS.
const CoreLinuxBasics = "core-linux-basics"
