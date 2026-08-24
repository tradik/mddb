---
name: mddb
description: Use MDDB's MCP tools well: which search tool fits a question, how to ask for chunks and projections instead of whole documents, and how to use the memory_* tools across sessions.
---

<!-- Generated from integrations/agent-instructions/AGENTS.md by
     scripts/gen-agent-instructions.py — edit the source, not this file. -->

# Working with MDDB

MDDB is a markdown database with full-text, vector and hybrid search, exposed
over MCP. This file tells an agent **which tool to reach for and what to ask
for**, because the tool schemas alone do not.

The failure this prevents is not using the wrong tool — it is reading whole
documents to answer a question the index could have answered, which is how a
one-line edit ends up costing thousands of tokens.

## Choosing a search tool

```
Do you know words that appear in the text?
├─ yes, and they are exact (names, identifiers, error strings, selectors)
│   └─ full_text_search
├─ yes, but the wording may differ from the source
│   └─ hybrid_search          ← start here when unsure
└─ no, you are describing a concept
    └─ semantic_search
```

- **`full_text_search`** — literal terms. Best for identifiers, error messages,
  configuration keys and anything a user quoted verbatim.
- **`semantic_search`** — meaning. Finds a passage about "cancelling a
  subscription" when the text says "ending your plan".
- **`hybrid_search`** — both, combined. `alpha` moves the balance: `0` is pure
  keyword, `1` pure vector, and the default in between suits most questions.
  Reach for this first when you cannot tell which side the answer sits on.
- **`search_documents`** — metadata only (tags, category, language). Use it to
  narrow a set, not to find text.
- **`aggregate`** — counts and groupings. Never fetch documents to count them.

## Asking for less

These matter more than tool choice. Measured on this engine: the same
`full_text_search` over five 12 KB documents returns **~19 700 tokens** with
its defaults and **~327 tokens** with `fields: ["key"]` — a 60x difference for
the same question, because the projection drops the bodies you were not going
to read.

| Ask for | Instead of | Why |
|---|---|---|
| `fields: ["key", "meta.title"]` | the whole document | projection drops the body from the response |
| `retrievalMode: "chunk"` | `"parent"` (the default) | one result per matching passage, with the passage — not the whole document it came from |
| `retrievalMode: "window"` | fetching neighbours yourself | widens a chunk by `windowSize` neighbours when you need surrounding context |
| `limit: 5` then refine | `limit: 50` | a first page usually answers it; ask again if it did not |
| `highlight: true` | reading the document to find the match | returns the matching fragments directly |
| `cacheTtl: 60` | repeating an identical query | serves a repeat from memory; writes invalidate it immediately |

## Anti-patterns

**Do not fetch a document to edit part of it.** Search with
`retrievalMode: "chunk"` and `highlight: true`, work from the fragment, and
write back only what changed. Pulling a 40 KB file to change one line is the
single most expensive mistake available here.

**Do not page through everything.** If you find yourself asking for `limit:
200`, the question is wrong — narrow it with metadata (`search_documents`) or
ask `aggregate` for the shape first.

**Do not re-search what you already have.** Within one task, keep the keys you
found. Re-running the same search to recover a key you already saw is pure
waste; if you genuinely need the record again, `search_documents` by key beats
a fresh full-text or semantic query.

**Do not use `semantic_search` for identifiers.** Embeddings blur exact
strings; a function name or an error code belongs in `full_text_search`.

## Conversation memory

The `memory_*` tools persist across sessions, which is what makes them worth
the calls:

1. `memory_start_session` once, at the beginning of a task.
2. `memory_add_message` as the conversation produces facts worth keeping —
   decisions, constraints, things the user corrected you on.
3. `memory_recall` at the start of related work, before asking the user to
   repeat themselves.
4. `memory_summarize` when a session grows long, so the next recall is cheap.
5. `memory_session_history` / `memory_list_sessions` to find prior work.

Record decisions and constraints, not transcripts.

## Writing

- `add_document` for one document; `bulk_ingest_submit` for many. The bulk path
  is asynchronous — poll `bulk_ingest_status`, or subscribe to the job's events
  and wait rather than poll.
- Metadata is flat: `map[string][]string`. Nested structure does not survive —
  JSON-encode it into a single value if you must keep it.
- Indexing follows the write in the same transaction, so a document is
  searchable as soon as the write returns.
