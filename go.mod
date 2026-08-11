module github.com/JoottunAtish/ShellForge

go 1.23

// Day 1 Session A introduces spf13/cobra (#12) and creack/pty (#9, #10).
// creack/pty lands here directly: internal/runtime/docker imports it for
// Session.Attach. golang.org/x/term and spf13/cobra are not imported by
// anything yet and will show as indirect until #10 and #12 land; term is
// also not yet on the approved set in .claude/skills/go-style/SKILL.md,
// which needs reconciling before #10 lands.
//
// The approved dependency set is listed in CLAUDE.md under "Go: style and
// formatting". Ask before adding anything outside that list.

require github.com/creack/pty v1.1.24
