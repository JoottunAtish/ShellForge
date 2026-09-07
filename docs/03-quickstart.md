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

Levels 9 to 25 arrive later. `shellforge play` already starts you where you left
off, and will pick those up as they land.

## Commands you type inside the sandbox

| Command | What it does |
|---|---|
| `check` | Verify the current level |
| `brief` | Reprint the briefing |
| `hint` | Show what the next hint costs. `hint --yes` spends it, `hint --reveal` asks about the solution |
| `reset` | Show what a rebuild would delete. `reset --yes` does it |

## Commands you type on your own machine

| Command | What it does |
|---|---|
| `shellforge play` | Start, or carry on where you left off. `play <id>` replays one level, `play --next` says which is next without starting it |
| `shellforge run <id>` | Play one specific level |
| `shellforge skip <id>` | Mark a level skipped so what comes after it unlocks. Awards no XP |
| `shellforge author validate <pack>` | Check a content pack against the level format |
| `shellforge author test --all` | Run every level through the golden contract |
| `shellforge doctor` | Checks your machine |
| `shellforge map` | The campaign as a tree of passed, available, and locked levels. `--ascii` forces plain output, and `NO_COLOR` is honoured too |
| `shellforge stats` | XP, rank, per-act progress, and achievements. Works with Docker stopped |
| `shellforge sandbox status` | Check whether the sandbox is provisioned and running |
| `shellforge sandbox shell` | Open an interactive shell inside the sandbox, with no level running |
| `shellforge sandbox rebuild` | Destroy the sandbox, then provision it again from scratch |
| `shellforge sandbox destroy` | Remove the sandbox. Asks you to type the sandbox name first, unless you pass `--yes` |

## Scoring, briefly

Each level is worth XP, scaled by its difficulty. Solving a level in fewer commands
than par earns a bonus, and so does passing on the first try. Optional objectives
are bonus only and never block you from passing.

Hints cost XP, and the cost comes off the award by subtraction: take a 5 XP hint
and a 10 XP hint and the level pays 15 less. The price is always shown before you
commit, and it is never a surprise.

**The award never goes below zero.** If your hints cost more than the level was
worth, you earn nothing rather than losing XP you already had. Passing a level
prints the whole sum, line by line, so you can see where the number came from.

Replaying a level you have already passed can improve your best score. It cannot
lower it.

Run `shellforge stats` to see your total, your rank, and which achievements you
have earned. Locked ones are listed too, so you can see what is there to go for.

## What to do when you are stuck

In this order:

1. Reread the briefing with `brief`. Most stuck moments are a misread objective.
2. Look at the actual state. `ls -l`, `cat`, `pwd`. The answer is usually visible.
3. Read the manual. `man <command>`, then press `/` to search inside it and `q` to
   quit. Level 4 teaches this deliberately, because it is the skill that makes
   every later level possible.
4. Spend a hint. Type `hint` to see what the next one costs, then `hint --yes`
   to take it. If you want the whole answer, `hint --reveal` says what that costs
   before you commit to it.

Getting stuck and working your way out is the skill being taught. That is why the
hints cost something.

## A note on breaking things

You cannot damage your computer from in here, and you cannot permanently ruin a
level. If you delete something you needed, `reset` puts it back: type `reset` to
see exactly what it would rebuild, then `reset --yes` to do it. It only ever
touches the level's own folder, so anything you saved elsewhere in the sandbox
survives. There is an
achievement for destroying a level so thoroughly that you had to reset, and earning
it is not a failure.
