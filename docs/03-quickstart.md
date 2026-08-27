# Quickstart

> **Status: outline.** Written properly on Day 7. The loop below is real and
> playable today; the table further down marks what is not built yet.

You are installed. Here is the loop.

```
shellforge run nav-01
```

## The loop

1. **Read the briefing.** It gives you the situation and what needs to be true when
   you are done. The objective checklist prints underneath it, before the prompt.
2. **Type commands into the shell.** It is a real shell. `vim`, `less`, `htop`, tab
   completion, job control and Ctrl-C all work exactly as they would anywhere else.
3. **Type `check`.** Shellforge looks at the actual state of the system and tells
   you which objectives you have hit. Get it wrong and it tells you about the first
   thing that is wrong, not all of them: fixing that one often fixes the rest.
4. **Type `exit`** when you are done, or when you want to stop. The level's world
   is removed and nothing is left running.

A check that takes too long, or that cannot tell, says so in different words from
a check that failed. "That is not a wrong answer" means exactly what it says: the
verification could not finish, and it is not a comment on what you typed.

**Ctrl-C during a check** cancels your wait and returns you to the prompt. The
session survives it. Type `check` again when you are ready.

## The levels you can play today

Nine, in this order: `nav-01` to `nav-04`, `files-01` to `files-04`, and
`pipe-05`. Running an id that does not exist lists the ones that do.

Levels 9 to 25 arrive later. `shellforge play`, which starts you where you left
off, arrives with them.

## Commands you type inside the sandbox

| Command | What it does |
|---|---|
| `check` | Verify the current level |
| `brief` | Reprint the briefing |
| `hint` | Not built yet. Arrives with scoring |
| `reset` | Not built yet. `exit` and run the level again does the same thing |

## Commands you type on your own machine

| Command | What it does |
|---|---|
| `shellforge run <id>` | Play one specific level |
| `shellforge author validate <pack>` | Check a content pack against the level format |
| `shellforge author test --all` | Run every level through the golden contract |
| `shellforge doctor` | Not built yet. Checks your machine |
| `shellforge play` | Not built yet. Start, or resume where you left off |
| `shellforge map` | The campaign as a tree of passed, available, and locked levels. `--ascii` forces plain output, and `NO_COLOR` is honoured too |
| `shellforge stats` | Not built yet. XP, rank, streak, achievements |
| `shellforge skip` | Not built yet. Give up on a level and move on |
| `shellforge sandbox status` | Check whether the sandbox is provisioned and running |
| `shellforge sandbox shell` | Open an interactive shell inside the sandbox, with no level running |
| `shellforge sandbox rebuild` | Destroy the sandbox, then provision it again from scratch |
| `shellforge sandbox destroy` | Remove the sandbox. Asks you to type the sandbox name first, unless you pass `--yes` |

## Scoring, briefly

Not built yet: nothing below is wired up, and `check` awards nothing today. It is
described here because it is what the hints and the bonus objectives are for, and
because a bonus objective you can see is worth understanding before it counts.

Each level is worth XP, scaled by its difficulty. Hints cost XP and always tell you
the price before you commit. Solving a level in fewer commands than par earns a
bonus, and so does passing on the first try. Optional objectives are bonus only and
never block you from passing.

Replaying a level you have already passed can improve your best score. It cannot
lower it.

## What to do when you are stuck

In this order:

1. Reread the briefing with `brief`. Most stuck moments are a misread objective.
2. Look at the actual state. `ls -l`, `cat`, `pwd`. The answer is usually visible.
3. Read the manual. `man <command>`, then press `/` to search inside it and `q` to
   quit. Level 4 teaches this deliberately, because it is the skill that makes
   every later level possible.
4. Spend a hint.

Getting stuck and working your way out is the skill being taught. That is why the
hints cost something.

## A note on breaking things

You cannot damage your computer from in here, and you cannot permanently ruin a
level. If you delete something you needed, `reset` puts it back. There is an
achievement for destroying a level so thoroughly that you had to reset, and earning
it is not a failure.
