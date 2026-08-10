---
name: code-navigation
description: How to explore the Shellforge codebase and docs efficiently using jCodemunch-MCP and jDocmunch-MCP, with the routing, symbol names, and layer boundaries specific to this repository. Load at the start of any task that requires finding or understanding existing code or documentation, before reaching for Read, Grep, or Glob.
---

# Navigating this repository

jCodemunch-MCP handles code, jDocmunch-MCP handles documentation. Both take
priority over Read, Grep, Glob, and Bash for exploration. Use `Read` only when you
are about to edit a file, because the harness requires a `Read` before `Edit` or
`Write` will succeed.

Both servers auto-connect from `.mcp.json` at the repository root. If either is
unavailable, fall back to the default tools for that side only. Do not block on it.

## Opening move

```
plan_turn { "repo": ".", "query": "<the task>", "model": "<your model id>" }
```

Obey the confidence level it returns:

- `high`: go straight to the recommended symbols, at most two supplementary reads
- `medium`: explore the recommended files, at most five supplementary reads
- `low`: **the feature probably does not exist.** In a repository this young that
  is usually the correct answer rather than a search failure. Report the gap and
  check `PROGRESS.md` before writing anything.

For a well-defined task, prefer the one-call shortcut:

```
assemble_task_context { "repo": ".", "task": "<the task>" }
```

## What actually exists right now

**This repository is a Day 0 scaffold.** Most of the architecture described in the
documentation is not implemented. Before concluding that something is missing,
check `PROGRESS.md`, which is the authoritative list of what is built.

Implemented today:

| Path | Contents |
|---|---|
| `cmd/shellforge/` | CLI dispatcher, command table, `version`, help rendering |
| `internal/platform/` | `ConfigDir`, `CacheDir`, `DataDir`, `LogDir`, `DatabasePath`, `EnsureDir` |
| `internal/platform/ux/` | `Error`, `Fail`, `Render`, the palette and `NO_COLOR` handling |
| `internal/archtest/` | Layer dependency enforcement |

Every other `internal/*` package contains only a `doc.go` stating its layer and its
import constraints. Those doc comments are the fastest way to understand the
intended design of an unimplemented package, so `get_file_outline` on a `doc.go` is
worth doing before assuming a package is empty.

## Routing by question

| You need | Use |
|---|---|
| A symbol by name | `search_symbols`, narrowed with `kind=`, `language=go`, `file_pattern=` |
| A string, comment, or config value | `search_text` |
| To read anything | `get_file_outline` first, then `get_symbol_source` |
| A symbol plus its imports | `get_context_bundle` |
| What imports a file | `find_importers` |
| Where a name is used | `find_references` |
| Whether an identifier is used at all | `check_references` |
| What breaks if you change X | `get_blast_radius` |
| What changed since the last commit | `get_changed_symbols` |
| Unreachable code | `find_dead_code` |

If `search_symbols` returns `negative_evidence` with
`verdict: "no_implementation_found"`, do not re-search with synonyms and do not
assume a nearby file implements it. Report that it needs to be created. In this
repository that verdict is usually correct.

## Layer-aware search

The codebase is strictly layered, and the layer determines where a thing can
legally live. Use `file_pattern` to scope searches to the right layer rather than
sweeping the whole tree:

```
L5  cmd/shellforge          CLI, output rendering
L4  internal/game           orchestrator, scoring, DAG, achievements, eventbus
L3  internal/content        pack loading and validation
    internal/verify         check registry and engine
L2  internal/pty            PTY multiplexer, OSC parser
    internal/journal        command journal, snapshots
L1  internal/runtime        Runtime interface, docker and wsl implementations
    internal/sandbox        image spec, provisioning
L0  internal/store          sqlite
    internal/platform       OS probes, console modes, paths
    internal/doctor         preflight
```

Two consequences worth internalising before you search:

- **A runtime backend type will never appear above L1.** If you are looking for
  Docker or WSL specifics from the game layer, they do not exist there by design.
  Look at `Runtime.Capabilities()` instead.
- **`internal/archtest/layers_test.go` is the machine-readable version of that
  map.** Reading it is faster than inferring the rules from prose.

## Documentation routing

Use jDocmunch for everything under `docs/`. The split matters:

| Location | Status | Treat it as |
|---|---|---|
| `CLAUDE.md`, `.claude/skills/` | Live | Rules that bind current work |
| `docs/LEVEL-FORMAT.md` | Live contract | Authoritative for the level schema and check catalogue |
| `docs/CURRICULUM.md` | Live contract | Authoritative for level designs |
| `docs/01` to `docs/07` | Outlines | Written on Day 7. Do not cite them as fact. |
| `docs/design/` | Frozen record | Intent, not current state. When it disagrees with the code, the code is right. |
| `PROGRESS.md` | Live | The only accurate statement of what is built |

The most common mistake in this repository is reading `docs/design/ARCHITECTURE.md`
and describing a component as though it exists. It is a 900 line design document
for software that is mostly unwritten.

## After editing

Hooks are configured on this machine and auto-reindex on `Edit` and `Write`. If
they are not firing, call `register_edit` with the paths. For bulk edits of five or
more files, call `register_edit` with all paths to batch the invalidation.

## Token discipline

- If `_meta` carries a `budget_warning`, stop exploring and work with what you have.
- Call `get_session_context` before re-reading something you may already have.
- `packs/**/assets/` will eventually hold deliberately large fixtures, including a
  4,000 line log and a directory of 60 files. Never read one whole. Use
  `search_text` with `context_lines`, or `get_file_content` with a line range.
