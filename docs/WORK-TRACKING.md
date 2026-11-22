---
title: "Work Tracking for Agents (v2.19.11)"
slug: "docs/work-tracking"
description: "Mirror a repository's issues and pull requests into a collection, keep the plan, backlog and progress notes next to them, and hand sessions over through the memory tools."
status: draft
---

# Work Tracking for Agents (v2.19.11)

> **Draft.** This page describes 2.19.11 as planned. It is published when
> the release ships, with every example run against a server.

A coding agent needs three things besides the code: what is asked for
(issues), what to do next (a plan), and what the last session left behind.
MDDB keeps all three next to the code collection, so one search tool and one
token budget cover them.

| Collection | Holds | Written by |
|---|---|---|
| `<repo>` | the code | `mddb-cli autoindex` ([Bulk Import](BULK-IMPORT.md)) |
| `<repo>-issues` | a read-only mirror of issues and pull requests | a sync rule with `source: "issues"` |
| `<repo>-work` | backlog rows, release plans, progress notes, decisions | the agent and `mddb-cli plan` |
| memory | session summaries and hand-over notes | the `memory_*` tools |

The forge stays the source of truth for issues. MDDB reads from it and never
writes to it: create, comment and close through the forge as before, then
refresh the mirror.

## 1. Mirror the issues

Store a token that can read issues as a secret, then add a sync rule
([Automations](AUTOMATIONS.md#synced-sources)):

```bash
curl -X POST http://localhost:11023/v1/automation -d '{
  "type": "cron", "name": "shop-api issues", "schedule": "*/15 * * * *", "enabled": true,
  "sync": {
    "source": "issues",
    "url": "https://github.com/acme/shop-api",
    "collection": "shop-api-issues", "lang": "en",
    "state": "all", "include": ["issues", "pulls"], "comments": true,
    "tokenSecretRef": "forge-token", "hookSecretRef": "forge-hook"
  }
}'
```

| `sync` field (`issues`) | Default | Description |
|---|---|---|
| `url` | (required) | the repository over https, as for `git` |
| `forge` | known by host | `github`, `gitlab`, `gitea` or `forgejo`, for a host that is not recognised |
| `state` | `all` | `open`, `closed` or `all` |
| `since` | none | only items updated on or after this date |
| `include` | `["issues"]` | `issues`, `pulls` or both |
| `comments` | `true` | append each comment to its item's document |
| `tokenSecretRef`, `hookSecretRef` | | stored secrets, as for `git`; a public repository needs no token |
| `prune` | `false` | delete the documents of items that no longer exist; only on a full listing |

**One document per item.** Key `issue-<number>` or `pull-<number>`. The
content is Markdown: the title as a heading, the body, then one section per
comment (`## Comment by <login> on <date>`), so a passage points at the
comment that matched. Metadata: `number`, `kind`, `state`, `stateReason`,
`labels`, `assignees`, `author`, `milestone`, `createdAt`, `updatedAt`,
`closedAt`, `comments`, `url`, `linkedIssues`, `linkedPulls`, and `paths`:
the file paths the text mentions, which is how an issue is found from a file
in the code collection.

**Runs are incremental.** A run asks the forge for the items updated since
the last one and sends conditional requests; nothing changed means nothing
written. When the forge's rate limit is reached the run ends with an error
that says when the limit resets, and deletes nothing.

**Three ways to refresh:**

- the schedule;
- the forge's webhook, pointed at `POST /v1/automation/{id}/hook` with the
  events for issues, issue comments and pull requests: one event refreshes
  one document;
- on demand: `sync_refresh` over MCP (`POST /v1/sync/refresh`) with the
  collection and, optionally, a number. A key limited to the collection may
  call it, at most once every 30 s per rule. `run_automation` remains an
  administration tool.

## 2. Ask the mirror instead of the forge

```json
{"name": "search_documents", "arguments": {"collection": "shop-api-issues",
  "filter": {"state": "open", "labels": {"in": ["bug"]}},
  "fields": ["key", "meta.title", "meta.updatedAt"], "limit": 20}}
```

```json
{"name": "hybrid_search", "arguments": {"collection": "shop-api-issues",
  "query": "discount applied twice at checkout", "format": "passages", "limit": 5}}
```

```json
{"name": "search_documents", "arguments": {"collection": "shop-api-issues",
  "filter": {"paths": {"contains": "internal/cart/discount.go"}},
  "fields": ["key", "meta.title", "meta.state"]}}
```

`cross_search` over `<repo>` and `<repo>-issues` answers "why does this code
look like this" with the code and the discussion in one response.

## 3. Keep the plan in a collection

`<repo>-work` holds documents whose `kind` says what they are:

| `kind` | Key | Content | Metadata |
|---|---|---|---|
| `backlog` | `backlog/<slug>` | one session's worth of work, as a ready prompt | `size`, `order`, `status` (`todo`, `doing`, `done`), `issues`, `release`, `model` |
| `plan` | `plan/<release>` | a release plan: waves, dependencies, decisions | `release`, `status` |
| `progress` | `progress/<date>-<slug>` | what was done, what is left, what not to do | `issues`, `commit`, `release` |
| `decision` | `decision/<slug>` | a decision and its reason | `issues` |

`mddb-cli plan` keeps the backlog rows; the other kinds are ordinary
documents, written with `add_document` or `mddb-cli add`. Without `-c` the
collection is the one `autoindex` picks for the working directory, with
`-work` added.

```bash
mddb-cli plan import BACKLOG.md -c shop-api-work   # a Markdown table in
mddb-cli plan add "#31: webhook with HMAC and a replay window" --size M --slug webhook-hmac
mddb-cli plan next          # the first todo row; marks it doing
mddb-cli plan done webhook-hmac --commit 3f2a9c1 --note "HMAC done; the replay window is left"
mddb-cli plan list --status todo
mddb-cli plan export > backlog-table.md            # and out again
```

- **Slugs.** Without `--slug` a row's slug is its prompt's first six words
  (`31-webhook-with-hmac-and-a` above); a derived slug that is taken gets
  `-2`, `-3`. The same prompt added twice is one row, so importing a file
  again adds only its new rows. `#N` in a prompt is added to `issues`, as
  `--issue N` is.
- **Order.** A new row goes after the last one; `--order` places it.
  `plan next` asks a facet for the lowest `order` among the `todo` rows and
  reads only the rows at that order.
- **The table.** `plan import` reads the first Markdown table in the file.
  Prompt, Size and Model become the row's prompt, `size` and `model`; any
  other column is kept as meta under its name in camelCase, and the column
  names in `columns`. `plan export` prints the `todo` and `doing` rows as
  that table (`--status` chooses), and only the table: the text around it is
  not stored. A table in the shape export writes (`| a | b |` lines and a
  `|---|---|` separator) comes back byte for byte.

`plan next` takes a row with a conditional write: it marks the row `doing`
only over the revision it read, and a session that loses takes the next row,
so two agents that ask at the same moment get different rows. `--peek` shows
the row without taking it. Over MCP alone the same takes two calls:
`search_documents` with `filter: {"kind": "backlog", "status": "todo"}`, then
`update_document` with the row's meta and its revision as `expected_revision`.
This is a notebook for agents, not a tracker: there are no boards,
assignments or notifications.

## 4. Hand a session over

The memory tools ([MCP](MCP.md)) work with a key limited to a project from
2.19.11: such a key reads and writes only its own memory namespace, and a
read-only key only recalls.

- **At the end of a session:** `memory_summarize`, then one message that
  says what was done, what is left, what not to do, and the commit.
- **At the start:** `memory_recall` with the task as the query and
  `limit: 3`.

Store summaries, not transcripts. A transcript holds tool output, which can
include secrets, and costs more to write than it saves to read.

## 5. One key per project

```bash
curl -X POST http://localhost:11023/v1/mcp/keys -d '{
  "name": "shop-api",
  "collections": ["shop-api", "shop-api-issues", "shop-api-work"],
  "readOnlyCollections": ["shop-api", "shop-api-issues"]
}'
```

The agent can search the code and the issues and write only its plan.

## 6. Tell the agent

`integrations/agent-instructions/` carries the rules ([Using MDDB from AI
Agents](AGENTS.md#2-give-the-agent-its-instructions)). In short:

1. Start: `plan next`, then `memory_recall` for that task.
2. Issues: search `<repo>-issues`; fetch one with `search_documents` and
   `filter: {"number": "<number>"}`. Call `sync_refresh` when the forge was
   just changed.
3. Writes to the forge go through the forge.
4. End: `plan done <slug> --commit <sha> --note "..."` (the row `done` and
   a `progress` document), then a hand-over message.

## Limits

- The mirror is as fresh as its last run: check `meta.updatedAt`, or refresh.
- Attachments, images, reactions, discussions and project boards are not
  mirrored.
- MDDB does not write to the forge.
