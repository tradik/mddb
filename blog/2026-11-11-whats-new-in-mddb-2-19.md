---
title: "What's New in MDDB 2.19"
slug: "blog/whats-new-in-mddb-2-19"
status: publish
type: post
date: 2026-11-11
tags: [release, rag, mcp, security, agents]
excerpt: "MDDB 2.19 turns the chat assistant into something you configure in the panel instead of a file: scenarios, skills, signed-in users with their own history, a log of what people ask, and read-only knowledge from other MCP servers. It also changes the licence, lets you switch off the modules you do not use, and makes the panel reach every server feature."
description: "MDDB 2.19: chat scenarios and skills in the panel, per-user chat history, a conversation log, module switches, and a new licence."
---

MDDB 2.19 is about the assistant that answers questions over your
collections. Until now its behaviour lived in a TOML file next to the chat
service: changing one word of the prompt meant editing a file and restarting.
Now the assistant is data in MDDB, edited in the panel, and the chat service
picks up a change at the visitor's next message.

The release also changes the licence, lets you switch off the modules you do
not use, and closes the gap between what the server can do and what the panel
shows. The full list is in the
[changelog](https://github.com/tradik/mddb/blob/v2.19.0/CHANGELOG.md).

## A new licence

From 2.19.0 the server, `mddb-cli`, the panel and the chat service are under
the **Business Source License 1.1**. Using MDDB in your company, on your
servers, for your own products, stays free. Offering MDDB itself to other
people as a hosted or embedded service that competes with Tradik's needs a
commercial licence. Each version becomes Apache 2.0 four years after its
release.

The client libraries, the integrations, the proto files and the chat widget
stay BSD-3-Clause, so code you write against MDDB is not affected. Releases up
to 2.18.10 stay BSD-3-Clause. [LICENSING.md](https://github.com/tradik/mddb/blob/v2.19.0/LICENSING.md)
maps every directory to its licence.

## The assistant is configured in the panel

The panel has a **Chat** page. A *scenario* is one assistant: what it is for,
what it must never do, how it answers questions outside its scope, which
collections it may search, its tone, model and limits. The page shows the
system prompt exactly as the server composes it, with its size in tokens.

The chat service loads the scenarios from MDDB and checks for changes every
ten seconds. The scenarios in its TOML file still work as a fallback, so an
existing deployment keeps running unchanged.

A scenario's collection list is now enforced where the tools run. Before
2.19, a model that was confused or prompt-injected could ask the search tool
for any collection the chat service could read.

## Skills

A *skill* is a reusable block of instructions in the `SKILL.md` format: a
name, a description and the instructions. Attach skills to scenarios in the
order you want. They go into the prompt after the guardrails, under a line
saying the guardrails win, so a skill cannot override them.

You can paste a skill or import it from a URL. An import shows the full text
before anything is saved, and a GitHub link is pinned to a commit. It is
fetched again only when you press *Update from source*, which shows a diff
first. Every change keeps the previous version.

## Signed-in users keep their conversation

A host application that already knows who its user is can sign a short-lived
token for the chat. With that token, the conversation is stored in MDDB. The
user can close the tab, switch devices, or meet a restarted chat pod, and the
assistant still remembers what was said. Users can list their conversations,
start a new one, or delete all of them. A host can export or delete
everything of one user in one call.

Visitors without a token are anonymous, as before. A dropped connection no
longer loses their conversation either: it waits for them until the session
expires.

## What people ask, and which answers are bad

A scenario can log its conversations. Logging is off by default, and e-mail
addresses, tokens and card numbers are masked before anything is stored. The
**Log** tab on the Chat page lists turns with filters for thumbs-down,
searches that found nothing, refusals, scenario and date. Each turn shows the
question, the answer, the searches it ran, what they returned and how it was
rated. Visitors can add a comment to a thumbs-down.

The **Docs gaps** view groups questions whose searches found nothing or found
only weak matches, most frequent first. It is a list of what to write next.

## Knowledge from other MCP servers

A scenario can also use read-only tools of other MCP servers, for things a
collection cannot hold well: order status, open tickets, the state of a
service. MDDB checks every tool when you save the scenario and refuses any
tool that is not declared read-only. The calls go through MDDB, so the chat
service never sees the credentials, and the results reach the model marked
as outside data. It is off until you turn it on.

## Switch off what you do not use

The new **Features** page lists the optional modules: chat, geo, temporal,
the spell checker, automation, webhooks, schemas, aliases, code intelligence
and more. A module that is off disappears from the menu, its API answers
"feature disabled", and its MCP tools leave the tool list. Data stays where
it is. The page groups modules like the menu and can turn a whole group on
or off at once. An operator can force modules off with
`MDDB_FEATURES_FORCE_OFF`.

The menu itself is now grouped and has a search box, because it had grown to
34 entries.

## The panel reaches everything

An audit of the API against the panel found 24 endpoints the panel never
called. They all have a screen now: agent memory, collection schemas, bulk
import jobs, personal API keys, facets, duplicate detection, Wikipedia
import, batch delete, checksums, backup now and restore, TTL and
classification in the document viewer, and more tools on the geo, temporal,
spell and code pages. Wiring them found nine server bugs, all fixed in this
release.

## Upgrading

Nothing to migrate. Every new field is optional, and every new module is on
by default except external MCP servers.

A few things behave differently:

- **The licence** changes as described above.
- **mddb-chat's `session.end` webhook** fires when a session ends or
  expires, no longer when the visitor's connection drops.
- **mddb-chat's `/health`** answers 503 until it has reached mddbd, so a
  container is unhealthy until then. It no longer exits when mddbd is not up
  yet.
- **mddb-chat needs `[mddb] http_url`** when its gRPC address does not use
  port 11024. Before, it guessed the login address and got it wrong.
- **`/v1/delete-batch`** answers `notFound`. The old `not_found` is still
  sent and will be removed in a later release.

Download 2.19.0 from
[GitHub releases](https://github.com/tradik/mddb/releases), or run
`mddb-cli self-update`.
