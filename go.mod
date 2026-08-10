module github.com/JoottunAtish/ShellForge

go 1.23

// No dependencies yet, and that is deliberate.
//
// The scaffold builds against the standard library alone so that `go build` and
// CI are green from the first commit, with no module download required.
//
// The approved dependency set is listed in CLAUDE.md under "Go: style and
// formatting". Day 1 Session A introduces spf13/cobra and creack/pty. Ask before
// adding anything outside that list.
