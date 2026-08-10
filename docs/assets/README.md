# Documentation assets

Screenshots and the demo GIF live here.

## The demo GIF

Recorded with [VHS](https://github.com/charmbracelet/vhs) rather than a screen
recorder, so the demo is a scripted `.tape` file that can be re-recorded whenever
the interface changes. A hand-recorded GIF goes stale silently; a scripted one
does not.

Target: under 5 MB, roughly 25 seconds, showing a real level actually being
solved.

Storyboard:

1. `shellforge play`, the briefing appears: "02:41, Billing service"
2. `ls logs/`, three log files
3. `grep -ri ERROR logs/ | wc -l > report.txt`
4. `check`, objectives turn green, XP awarded, rank up

The rank-up at the end is the moment that makes people click install.

## Screenshots

Captured on Day 7 against a clean Windows 11 VM, in Windows Terminal at a
consistent size. The list the install guide needs:

`winver`, Task Manager virtualization, Administrator PowerShell, `wsl --install`
output, installer output, a healthy `doctor`, a failing `doctor`, `init`
progress, the first level briefing, a passed level with XP.

## Status

Empty. These are produced on Day 7, against a build that actually runs.
