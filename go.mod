module github.com/JoottunAtish/ShellForge

go 1.23

// Day 1 Session A introduces spf13/cobra (#12) and creack/pty (#9, #10).
// golang.org/x/term (#10) is not yet on the approved set in
// .claude/skills/go-style/SKILL.md; that needs reconciling before #10 lands.
// No Go code imports any of these yet, so they show as indirect until
// #9, #10, and #12 land.
//
// The approved dependency set is listed in CLAUDE.md under "Go: style and
// formatting". Ask before adding anything outside that list.

require (
	github.com/creack/pty v1.1.24 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/cobra v1.10.2 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/term v0.45.0 // indirect
)
