# Documentation assets

The demo GIF and the install screenshots live here.

## The demo GIF

Recorded with [VHS](https://github.com/charmbracelet/vhs) rather than a screen
recorder, so the demo is a scripted `.tape` file that can be re-recorded whenever
the interface changes. A hand-recorded GIF goes stale silently; a scripted one
does not.

`demo.tape` is the script. `make demo` runs it and writes `demo.gif` here.

Target: under 5 MB, roughly 25 seconds, showing a real level actually being
solved.

Storyboard:

1. `shellforge run pipe-05`, the briefing appears: "02:41. Billing service."
2. `ls logs/`, three log files
3. `grep -ric error logs/ | ...`, counting the errors into `report.txt`
4. `check`, objectives turn green, XP awarded

The award at the end is the moment that makes people click install.

Two things the tape does that the storyboard does not say, and both matter:

- It points `XDG_DATA_HOME` at a temporary directory, so the recording shows a
  fresh player rather than whoever recorded it, and re-recording after passing
  another level produces the same GIF rather than a different one.
- It runs `shellforge run pipe-05` rather than `shellforge play`. `play` resolves
  the next level from the player's own progress, which is the opposite of
  reproducible.

## Screenshots

**Status: none captured yet.** Capturing them needs a clean Windows 11 VM, which
is the same machine task 6.8 of the seven-day plan needs, and neither has
happened.

`docs/01-install-windows.md` is written so that every step stands on its own
prose, and it carries no placeholder text waiting for an image. Adding a
screenshot is an improvement to a working page rather than the completion of a
broken one.

The shots worth taking, in page order:

| Step | Shot |
|---|---|
| 2 | `winver`, showing the build number |
| 2 | Task Manager, Performance, CPU, with the Virtualization line visible |
| 3 | Windows Terminal in the Microsoft Store |
| 5 | An Administrator terminal, with "Administrator" visible in the title bar |
| 5 | `wsl --install --no-distribution` output |
| 6 | `install.ps1` output, including the checksum line |
| 7 | A healthy `doctor` table |
| 7 | A failing `doctor` table, so a reader knows what one looks like |
| 8 | `init` progress |
| 9 | The first level briefing |
| 9 | A passed level with the score breakdown |

Rules for anyone taking them: use Windows Terminal at a consistent window size,
capture against a real build rather than a mock, and put nothing personal in
frame. A screenshot showing a real home directory path or a real username is a
screenshot that has to be retaken.
