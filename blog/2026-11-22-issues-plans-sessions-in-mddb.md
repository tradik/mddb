---
title: "Issues, Plans and Sessions in MDDB"
slug: "blog/issues-plans-sessions-in-mddb"
status: draft
type: post
date: 2026-11-22
tags: [agents, mcp, retrieval]
categories: [Guides]
excerpt: "Mirror your repository's issues into MDDB, keep the plan and the session notes next to them, and let the agent ask one index instead of three tools. We measured where that saves tokens and where it does not."
description: "A read-only issue mirror, a plan collection and scoped session memory for coding agents in MDDB 2.19.11: what it costs, what it saves, and when to skip it."
---

<!-- DRAFT. Publish with 2.19.11. Every figure marked TBD is measured on a
     running server before publication (blog/README.md); the forge-side
     figures below are measured already. -->

A coding agent spends tokens on three things before it writes code: finding
the code, finding out what is asked for, and finding out where the last
session stopped. [Keep Your Agent's Code Index Live](/blog/live-code-index-for-agents/)
covered the first. This post is about the other two, and it starts with a
measurement that argues against the idea.

## Reading an issue is already cheap

We measured the forge's CLI on MDDB's own repository: 108 issues, tokens
counted as four characters each.

| Call | Tokens |
|---|---|
| list 30 issues: number, title, labels | 928 |
| list 30 issues with their bodies, as JSON | 21,007 |
| one issue with its comments | 546 – 2,400 |
| all 108 issues with comments | 72,792 |

A careful agent lists titles and opens one issue: about 2,000 tokens. A
mirror in MDDB returns the same text for the same question, so it saves
nothing there. If that is all your agent does with issues, stop reading and
keep the CLI.

## What is expensive

Three questions are expensive, or cannot be asked at all:

- **"Did we fix this before?"** The forge searches by words. An issue that
  says "discount applied twice" does not match "duplicate coupon". The
  alternative is reading the history: 72,792 tokens here, and this is a
  small tracker.
- **"Why does this code look like this?"** The answer is split between a
  file and a closed issue. An agent with two tools makes the connection by
  hand, if it makes it at all.
- **"Where was I?"** Each session starts by reading the plan and the notes
  in full, whatever the task. In our own workflow the notes index alone is
  about 2,500 tokens per session, and it grows with every release.

All three are retrieval problems, and retrieval is what MDDB does.

## The setup in 2.19.11

Three collections per repository and one key:

| Collection | Holds | Written by |
|---|---|---|
| `shop-api` | the code | `mddb-cli autoindex --watch` |
| `shop-api-issues` | issues and pull requests, read-only | a sync rule |
| `shop-api-work` | backlog rows, plans, progress notes | the agent, `mddb-cli plan` |

**Issues** arrive through a sync rule with `source: "issues"`: one document
per issue or pull request, comments as sections, labels and state as
metadata, and the file paths an issue mentions as `paths`. It refreshes on
a schedule, from the forge's webhook, or when the agent calls
`sync_refresh` for one number. MDDB only reads from the forge. Creating,
commenting and closing still go through the forge. Those calls are rare and
small, and a database that writes to your public tracker on an agent's
behalf is a risk we did not want to take for a saving that small.

**The plan** is documents with a `kind`: `backlog`, `plan`, `progress`,
`decision`. `mddb-cli plan next` returns the first open row and marks it
taken with a conditional write, so two agents never start the same row.
`plan import` and `plan export` convert to and from a Markdown table, for
anyone who prefers the file.

**Sessions** use the memory tools MDDB already had. From 2.19.11 a key
limited to one project can use them, inside its own namespace. The agent
ends a session with a summary and a hand-over note, and starts the next one
by recalling the three notes closest to its task.

The commands, fields and the skill text are in
[Work Tracking for Agents](/docs/work-tracking/).

## What it should save

Estimates until the release is measured; the forge column is measured.

| Question | Forge CLI | MDDB |
|---|---|---|
| open bugs, titles only | ~930 for 30 | TBD (projection: keys and titles) |
| one issue, known number | 546 – 2,400 | the same text |
| "did we fix this before?" | up to 72,792, or not found | TBD (5 passages; expected 1,000–2,500) |
| issues that mention a file | not possible directly | TBD (metadata filter) |
| start of session: next task and notes | ~2,500 and growing | TBD (expected under 1,000) |

For ordinary work we expect a saving of 3,000 to 15,000 tokens per session.
That is small next to what a code index saves, which is why this feature is
small too: a mirror and some conventions, with no second tracker. The
larger gain is in answers the agent could not get before, and no token
count shows that.

## When to use it

Worth it:

- a tracker with hundreds of issues, where history matters and nobody
  remembers it;
- several agents or sessions working from one backlog;
- long-running work, where each session continues the last;
- a forge with strict rate limits, or work that has to continue offline.

Not worth it:

- a small tracker you know by heart;
- a workflow where the agent only ever opens the one issue you name;
- a team that needs boards, assignments and notifications. This is an
  agent's notebook next to your tracker, not a replacement for it.

## Measure your own

TBD: the task file and the command for `tools/bench/agent` that run the
same ten questions through the forge's CLI and through MDDB.
