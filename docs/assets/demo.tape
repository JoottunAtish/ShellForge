# Shellforge demo recording.
#
# Run it with `make demo`, not with `vhs` directly: the target builds the
# binary, puts bin/ on PATH, and points the game at a throwaway progress
# database first. Running this file on its own would record against whatever
# shellforge is on your PATH and write to your real save file.
#
# Why it is a script rather than a screen recording: a hand-recorded GIF goes
# stale the first time the interface changes and nobody notices, because there
# is nothing to re-run. This can be re-recorded in one command.
#
# Two things here exist for reproducibility and should not be "simplified":
#
#   1. `make demo` sets XDG_DATA_HOME to a temporary directory. Without it the
#      recording shows whoever recorded it: their XP, their rank, their
#      achievements, and a different GIF every time someone passes a level.
#   2. It plays pipe-05 by name rather than running `shellforge play`. `play`
#      resolves the next level from the player's own progress, which is the
#      opposite of reproducible. pipe-05 is also the level the storyboard in
#      README.md was written against: it opens "02:41. Billing service."
#
# The sleeps are the part most likely to need adjusting on a different machine.
# They wait for real work (provisioning a level world, running the checks), so
# a slower disk needs longer. If the GIF cuts off mid-output, raise them.

Require shellforge

Output docs/assets/demo.gif

Set Shell bash
Set FontSize 16
Set Width 1200
Set Height 750
Set Padding 20
Set TypingSpeed 60ms
# 24 rather than the default 50. The GIF has to stay under 5 MB for a README
# that loads on a phone, and halving the frame rate is the cheapest way there.
Set Framerate 24

# Provision the sandbox before the recording starts. It is idempotent, it takes
# minutes on a cold machine, and nobody wants to watch it.
Hide
Type "shellforge init >/dev/null 2>&1 || true"
Enter
Type "clear"
Enter
Show

# 1. The briefing. "02:41. Billing service."
Type "shellforge run pipe-05"
Enter
Sleep 8s

# 2. Three rotated logs, too long to scroll.
Type "cd quest"
Enter
Sleep 1s
Type "ls logs/"
Enter
Sleep 2s

# 3. Count the errors, and collect the distinct codes.
Type "grep -ri ERROR logs/ | wc -l > report.txt"
Enter
Sleep 1s
Type "grep -rhoE 'E[0-9]{3}' logs/ | sort -u > codes.txt"
Enter
Sleep 1s

# 4. The objectives turn green and the level pays out.
Type "check"
Enter
Sleep 6s
