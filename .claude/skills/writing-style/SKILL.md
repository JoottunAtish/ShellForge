---
name: writing-style
description: Punctuation and prose rules for Shellforge. Load before writing or editing any prose: documentation, level briefings, on_fail messages, hint text, CLI output strings, error messages, commit messages, issue and PR bodies, or README content. Also load when asked to fix typographic punctuation, when a punctuation CI check fails, or when unsure how to rewrite an em dash.
---

# Writing style

## Rule 0: no em dashes, no en dashes, no smart punctuation

This applies to everything: code, comments, commit messages, documentation, level
briefings, `on_fail` messages, hint text, CLI output strings, and replies in chat.

Banned code points, named rather than shown so this file passes its own lint:

| Code point | Name | Use instead |
|---|---|---|
| U+2014 | EM DASH | A colon, a comma, parentheses, or a full stop. An ASCII hyphen only when nothing else fits. |
| U+2013 | EN DASH | ASCII hyphen for ranges: `10-60 s`, `Acts I-IV`, `M0-M2` |
| U+2010, U+2011, U+2012, U+2015 | HYPHEN, NON-BREAKING HYPHEN, FIGURE DASH, HORIZONTAL BAR | ASCII hyphen |
| U+2212 | MINUS SIGN | ASCII hyphen |
| U+2018, U+2019 | LEFT and RIGHT SINGLE QUOTATION MARK | ASCII apostrophe |
| U+201C, U+201D | LEFT and RIGHT DOUBLE QUOTATION MARK | ASCII quote |
| U+2026 | HORIZONTAL ELLIPSIS | Three ASCII periods |
| U+00A0, U+2009, U+202F | NO-BREAK SPACE, THIN SPACE, NARROW NO-BREAK SPACE | ASCII space |

### How to rewrite an em dash

Do not reach for a hyphen every time. Pick the punctuation the sentence actually
wants. `<EM>` stands for the em dash that was there:

```
BAD   The parser is a state machine <EM> not a regex over a buffer.
GOOD  The parser is a state machine, not a regex over a buffer.

BAD   Reset strategy <EM> wipe the scratch directory.
GOOD  Reset strategy: wipe the scratch directory.

BAD   Doctor is not an afterthought <EM> it is the largest source of avoided support.
GOOD  Doctor is not an afterthought. It is the largest source of avoided support.

BAD   Three runtimes <EM> Docker, WSL, native <EM> share one interface.
GOOD  Three runtimes (Docker, WSL, native) share one interface.

BAD   Levels 9<EN>25 are the content sprint.
GOOD  Levels 9-25 are the content sprint.
```

The most common mistake is reaching for a hyphen in all of those. A hyphen joins
words. It does not separate clauses. If the em dash was separating two independent
clauses, you want a full stop or a semicolon.

### What is still allowed

These are semantic UI glyphs, not typography:

- Status marks and the emoji used in `map` and progress output
- Box drawing characters in diagrams
- Arrows in diagrams and state machines
- Section sign, degree sign, mathematical comparison operators

### Enforcement

```bash
./scripts/check-punctuation.sh
```

Hard CI gate, not a warning. If you are adding a file type that legitimately needs
a listed code point, change the script and say why in the commit message.

## Prose rules for anything a human reads

The audience is a first-year CS student who has never opened a terminal and is
worried about breaking their laptop.

- **Never write "just" or "simply".** They mean "this is easy", and a reader who is
  stuck now feels stupid.
- **Explain what a thing is before telling anyone to install it.** Define WSL,
  distro, shell, sandbox, and PATH at first use.
- **Every error message names what failed, why, and the next command to run.**
  "Operation failed" is a bug.
- **Write `on_fail` in the voice of a helpful senior colleague.** Never "incorrect",
  never "try again". Name what is wrong and what to look at.
- **No marketing adjectives** in documentation. No "blazingly fast", no "seamless",
  no "powerful".
- **Plain ASCII in copy-pasteable commands.** A smart quote in a shell command is a
  broken shell command, and the person it breaks for is a beginner who will assume
  they made the mistake.
- **Every command shown in documentation must be run before it is written down.**
