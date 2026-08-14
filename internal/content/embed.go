package content

import (
	"github.com/JoottunAtish/ShellForge/packs"
)

// Embedded returns the content pack compiled into the binary.
//
// It reads from the embedded filesystem and touches no real path, so it works
// from a binary invoked with any working directory, or none: a learner who
// double-clicks the executable gets the same pack as one who runs it from the
// repository. That is what makes the single-binary promise in README.md true
// for content and not only for code.
//
// Each call decodes the pack again rather than returning a shared value. A
// pack is a few hundred kilobytes of YAML read a handful of times per session,
// so caching would buy nothing measurable and would hand every caller a
// pointer into the same mutable Levels slice.
func Embedded() (*Pack, error) {
	return LoadPack(packs.FS, packs.CoreLinuxBasics)
}
