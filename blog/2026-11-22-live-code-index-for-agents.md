---
title: "Keep Your Agent's Code Index Live"
slug: "blog/live-code-index-for-agents"
status: publish
type: post
date: 2026-11-22
tags: [agents, mcp, retrieval]
categories: [Guides]
excerpt: "Point a local MDDB at the repository you are working in, keep it in step with every save or commit, and tell your coding agent in a skill to search before it reads. Measured costs, expected savings, and when not to bother."
description: "Index a local checkout with mddb-cli autoindex, keep it current with --watch or git hooks, write a skill that makes the agent search first, and estimate the token savings."
---

A coding agent that has no index finds code by grepping and opening files.
Most of the tokens it spends on a task go into that search, before it writes
a single line. This guide sets up a local MDDB that follows your checkout as
you edit it, and a skill that tells the agent to ask the index first. It
ends with the numbers: what the setup costs, what it saves, and when it is
not worth doing.

It builds on [One Server, a Collection per Project](/blog/a-collection-per-project/),
which covers running the server and giving each project its own scoped key.
What is new here, from 2.19.10, is keeping the index live.

## 1. Index the checkout

With a server running on `localhost` (the earlier post has the `docker run`
line), index the repository you work in:

```bash
cd ~/code/shop-api
mddb-cli autoindex . --dry-run    # check the plan first
mddb-cli autoindex .
```

Look at the dry run before the real one. `autoindex` sends every file git
tracks **and every untracked file git does not ignore**. A `.env` that is
missing from `.gitignore` would go into the index. Add it to `.gitignore`, or
pass `--exclude '.env*'`.

On MDDB's own repository (3,021 files, 25 MB, no embedding provider, WSL2)
the first run took 153 s. A second run with nothing changed took 1.0 s,
because the server skips content it already has.

## 2. Keep it in step

Pick one of these, or use both. They write the same documents.

```bash
mddb-cli autoindex . --watch --prune          # while you work; Ctrl-C stops it
mddb-cli autoindex hook install --server http://localhost:11023 -- --prune
```

| | `--watch` | `hook install` |
|---|---|---|
| Follows | every save, including uncommitted work | each commit, merge, checkout, rebase |
| Delay, measured | 1.5–2.1 s from save to searchable | 0.16–0.22 s after a one- or two-file commit |
| Cost while idle | one listing per 2 s: about 12 ms for 3,093 files | none |
| Runs as | a process you keep open in a terminal | a background job git starts; a commit took 9–13 ms with it installed |
| Best for | a session where the agent edits and then searches what it just wrote | set and forget |

`--watch` checks file sizes and modification times every `--interval`
instead of using file events. That is why it also works on a WSL2 checkout
under `/mnt/c`, on NFS and in containers.

Pass `--server` to `hook install`. The hook runs in the environment git runs
in, and a commit from an IDE often has no `MDDB_SERVER` set. When we left it
out, the hook went to the default address and logged a connection error. The
commit still went through: the hook never fails a commit. The log is
`.git/mddb-autoindex.log`, and `mddb-cli autoindex hook status` shows which
hooks have the block and which commit was indexed last. If the server needs
a key, set `MDDB_API_KEY` in that environment; the hook file never contains
one.

`--semantic`, which runs a language server for exact references, needs the
whole tree. Run it by hand when the structure changed, or put it in the
hook's flags if a full run per commit is acceptable. Between runs, the files
you changed fall back to text-based symbols. The server never returns a stale
fact as current.

## 3. Connect the agent

Give the project an MCP key limited to its own collection, with read-only
access, and add it to `.mcp.json`. The earlier post shows both steps. A
read-only key matters here: the agent can search the index but cannot write
to it or delete from it. Only `autoindex` writes.

## 4. Tell the agent to search first

Tool schemas say what a tool accepts, not when to call it. Without
instructions, an agent that has `hybrid_search` will often grep anyway. Two
files fix that.

**The general skill**, shipped with MDDB, covers choosing a search tool and
asking for passages instead of documents:

```bash
mkdir -p .claude/skills/mddb
curl -fsSL -o .claude/skills/mddb/SKILL.md \
  https://raw.githubusercontent.com/tradik/mddb/main/integrations/agent-instructions/claude-code/SKILL.md
```

The same guidance comes as `AGENTS.md`, a Cursor rule and a Windsurf rule in
[`integrations/agent-instructions/`](https://github.com/tradik/mddb/tree/main/integrations/agent-instructions).

**A project skill** says which collection belongs to this repository and
sets the rules for this workflow. Put it in
`.claude/skills/shop-api-code/SKILL.md` and adjust the names:

```markdown
---
name: shop-api-code
description: Find code, callers and design notes in shop-api through the MDDB collection `shop-api` before grepping or opening files. Use for "where is X handled", "who calls Y", "what implements Z" and questions about behaviour.
---

# Finding code in shop-api

The MDDB collection `shop-api` mirrors this checkout and is kept within a few
seconds of the files on disk.

1. Start with one search, not grep:
   - an identifier, error text or config key: `full_text_search`
   - a behaviour ("where do we apply the discount"): `hybrid_search`
   - who calls, or what implements, a function or type: `code_symbol`
2. Pass `collection: "shop-api"`, `format: "passages"` and `limit: 5`. Each
   passage is a path, a line range and those lines. Open only that range.
3. Before editing a file, read it. The index can be a few seconds behind;
   the file on disk is the source of truth.
4. Grep is fine when you already know the exact word and the directory.
5. If two searches return nothing useful, say so and fall back to grep. Do
   not raise the limit or ask for whole documents.
```

Rule 3 is what keeps this safe. Even a slightly stale index gives the agent
the right file and roughly the right lines. The edit is made against the
file the agent has just read, never against the index.

## What to expect

These are estimates, built from two measurements. The search costs come
from the benchmark in [One Server, a Collection per Project](/blog/a-collection-per-project/#will-it-make-you-faster):
15 "where is X handled" tasks on MDDB's repository, each answered by grep and
reading and by one MDDB passage search. Both routes found 13 of 15 answers.

| Per lookup | Tokens | Tool calls |
|---|---|---|
| grep + read, best case | ~9,300 | ~1.9 |
| grep + read, typical | ~185,000 | ~8.9 |
| MDDB passages | ~5,700 | 1 |

The "typical" row is an average dominated by a few bad tasks, where a
common word matched 160 files. Most lookups cost far less than 185,000
tokens, but now and then one costs much more, and those are the ones you
notice on the bill.

Scaled to a working day with 20 lookups:

| Day of 20 lookups | Tokens on finding code |
|---|---|
| grep, every task goes well | ~190,000 |
| grep, a typical mix | ~3,700,000 |
| MDDB passages | ~115,000 |

A single hit shows the scale. Searching for `takeLock stale` returned one
passage of 205 tokens: 21 lines of `autoindex_hook.go`, with the path and
line range. The whole file is about 1,450 tokens (counted as four
characters per token).

What it costs you: the first index (minutes for a few thousand files, longer
with an embedding provider, which we did not measure here), a terminal
running `--watch` or a one-time `hook install`, and one more local service
to keep running.

## When to use it

Worth it:

- repositories of a few thousand files and up, or several open at once;
- questions about behaviour, where you do not know the word the code uses;
- refactors that need every caller of a function with a common name
  (`code_symbol`, better still after `--semantic`);
- long sessions where the agent searches code it wrote an hour ago:
  `--watch` makes that code searchable within seconds.

Not worth it:

- a small repository you know well. You know what to grep for, and grep is
  fast;
- one-off scripts and throwaway checkouts, where the first index costs more
  than it saves;
- a machine where you cannot run another local service.

## Measure your own

Our numbers come from our repository and our questions. `mddb-cli gain
--window 7d` reports how many searches the agent made and how many tokens
passages saved against returning whole documents. `tools/bench/agent` in the
repository runs the grep-versus-MDDB comparison on your own collection with
your own task file. Ten questions from your last week of work will tell you
more than these tables.

Setup details are in [Bulk Import](/docs/bulk-import/#keeping-a-collection-current---changed-since---watch-hook-v21910)
and [Using MDDB from AI Agents](/docs/agents/).
