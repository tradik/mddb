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

Do not guess a vector index. Pass `algorithm: "auto"` to `semantic_search` and
`cross_search`, or `vector_algorithm: "auto"` to `hybrid_search`. The server
then picks one per query, from the collection size, the indexes already built
and how much your filter admits. Add `explain: true` once to see why.

When a vector or hybrid search returned some right and some wrong results, do
not rewrite the query. Search again with the keys: `positive` for the hits
that answered, `negative` for the ones that did not. The second round moves
towards the first and away from the second. On a test corpus, one round
raised recall@10 from 0.31 to 0.49.

When a search keeps missing what it should find, measure instead of guessing.
Record the question and the document that answers it with `add_eval_query`,
then compare configurations with `run_eval`.

## Before implementing something new

If a project that already solved it is indexed as a reference (`mddb-cli
reference list`), ask it first: `reference_search` for "how did they do X",
`reference_examples` for code that calls a function, `reference_symbol` for
where something is defined. Each answer names the licence in
`meta.license` — learn from the approach; copy code only when that licence
allows it, and say which licence applies.

## Before changing code

Ask `code_symbol` about the function, type or selector first: one call returns
where it is defined (path and lines), every place it is used and from inside
which definition, what it calls and which tests touch it. Read those passages,
not the files around them. `code_graph` answers the file-level question —
which documents depend on which.

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
| `format: "passages"` | documents, then reading them for the place | path + line range + enclosing definition + those lines, per hit — the default to reach for in code |
| `filter: {"path": {"prefix": "src/"}}` | searching everything and discarding | narrows on the metadata index before scoring; also `contains`, `gte`/`lt`, `not`, `exists` |
| `group_by: {"key": "path", "per_group": 1}` | five hits from the same file | one result per file (or author, or collection), so the page covers more ground |

## Fewer tools, better choices

If the server or your key lists many tools you never use, ask the operator for
a key limited to the toolsets your work needs — `search,code` for reading code,
`search` for answering from documents, `search,memory,documents` for an
assistant that keeps notes. Fewer schemas in context means a cheaper turn and
a clearer choice.

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

- To delete, retag or expire many documents, use `delete_where`,
  `update_where` or `set_ttl_where` with a metadata filter. Run them with
  `dry_run: true` first and check `matched` and `sample` before the real
  call. Don't list keys and delete them one by one.
- To make a repository or folder searchable, run `mddb-cli autoindex <dir>`
  rather than adding files one by one: it sets path, kind and language, lets
  the server extract symbols, and skips what has not changed.

- `add_document` for one document; `bulk_ingest_submit` for many. The bulk path
  is asynchronous — poll `bulk_ingest_status`, or subscribe to the job's events
  and wait rather than poll.
- When several agents may edit the same document, pass the `revision` you read
  as `expected_revision` to `update_document`. A conflict means someone else
  wrote first: read again and redo the change, never retry blindly.
- Metadata is flat: `map[string][]string`. Nested structure does not survive —
  JSON-encode it into a single value if you must keep it.
- Full-text and metadata indexing follow the write before it returns, so
  `full_text_search` finds a document at once. Its embedding is computed in
  the background: to find it with `semantic_search` straight after writing,
  pass `wait_for_index: 5000, wait_for_keys: ["<key>"]`, or check
  `get_indexing_status`.
