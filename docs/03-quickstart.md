# Quickstart

> **Status: outline.** Written on Day 7.

You are installed. Here is the loop.

## The loop

1. **Read the briefing.** It gives you the situation and what needs to be true when
   you are done.
2. **Type commands into the shell.** It is a real shell. `vim`, `less`, `htop`, tab
   completion, job control and Ctrl-C all work exactly as they would anywhere else.
3. **Type `check`.** Shellforge looks at the actual state of the system and tells
   you which objectives you have hit.
4. **Stuck? Type `hint`.** It shows you the cost before it charges you.
5. **Broke something? Type `reset`.** The level rebuilds in under a second.

## Commands you type inside the sandbox

| Command | What it does |
|---|---|
| `check` | Verify the current level |
| `hint` | Reveal the next hint, after telling you the cost |
| `brief` | Reprint the briefing and objectives |
| `reset` | Rebuild this level from scratch |

## Commands you type on your own machine

| Command | What it does |
|---|---|
| `shellforge play` | Start, or resume where you left off |
| `shellforge run <id>` | Play one specific level |
| `shellforge map` | See the campaign and what is still locked |
| `shellforge stats` | XP, rank, streak, achievements |
| `shellforge skip` | Give up on a level and move on |
| `shellforge reset --all` | Start the campaign over |
| `shellforge sandbox status` | Check the sandbox is healthy |
| `shellforge doctor` | Check your machine |

## Scoring, briefly

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
