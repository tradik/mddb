# langchain-mddb

LangChain `VectorStore` and `Retriever`, LangGraph `Store` (agent memory) and
a Deep Agents filesystem backend, backed by [MDDB](https://github.com/tradik/mddb).

## Install

```bash
pip install langchain-mddb
```

The adapter needs `mddb-client`, and **that is not on PyPI**. Its protobuf
stubs are generated at build time and are not committed, so it cannot be
installed from git either — it is built from a checkout:

```bash
git clone https://github.com/tradik/mddb.git
cd mddb && ./proto/generate.sh      # needs buf
pip install ./clients/python
```

`pip install "langchain-mddb[client]"` names the dependency but cannot satisfy
it on its own; the extra exists so the requirement is declared rather than
implied.

## Use

```python
from langchain_mddb import MddbVectorStore

store = MddbVectorStore(collection="docs", address="localhost:11024")

store.add_texts(
    ["Restart the service with systemctl.", "Rotate the certificate with certbot."],
    [{"kind": "runbook"}, {"kind": "runbook"}],
)

for doc, score in store.similarity_search_with_score("how do I restart", k=3):
    print(f"{score:.3f}  {doc.metadata['key']}  {doc.page_content[:50]}")

retriever = store.as_retriever(search_kwargs={"k": 5, "filter": {"kind": "runbook"}})
```

## Two differences from most LangChain vector stores

**MDDB embeds server-side.** LangChain's contract assumes the application owns
an embedding function and passes vectors in. MDDB is configured with a provider
and embeds on write: one model per collection rather than one per application,
and no vectors crossing the wire. The `embedding` argument is accepted for
interface compatibility, and `similarity_search_by_vector` raises rather than
quietly re-embedding your text with a different model — a caller reaching for
that method is usually trying to keep two systems in the same vector space, and
silently using another model defeats exactly that.

**Hybrid search is the default.** LangChain's `similarity_search` means vector
search; MDDB's best answer for most corpora is a keyword/vector blend. This
adapter follows MDDB, because following the framework would mean returning
worse results than the same server queried directly. `search_type="vector"`
gets the literal reading, `"fts"` gets keyword only.

```python
store = MddbVectorStore(collection="docs", search_type="vector")
store.similarity_search("query", k=5, search_type="fts")   # or per call
```

## Which settings should this collection use?

MDDB can measure a collection and say. Not part of LangChain's interface, and
here because choosing `search_type` by hand is guessing while the server has
measured:

```python
advice = store.recommended_settings()
print(advice["search_type"], advice["retrieval_mode"])
for reason in advice["reasons"]:
    print(" -", reason)
```

## Metadata and filters

MDDB metadata is multi-valued. Writing `{"tag": "go"}` stores `["go"]`; reading
it back unwraps a single value and leaves a list as a list.

Filters translate equality and membership:

```python
store.similarity_search("query", filter={"kind": "runbook"})
store.similarity_search("query", filter={"kind": ["runbook", "guide"]})
```

Operator forms (`{"$gt": ...}`) raise rather than being dropped. A dropped
condition silently widens the search, which is worse than an error.

## Ids

MDDB addresses documents by `(collection, key, lang)`, so `ids` are used as
keys. Without them the store derives a key from the text, so re-running an
ingest script updates the same documents instead of doubling the collection.

## Deep Agents backend

`MddbBackend` (extra `deepagents`, Python 3.11+) implements Deep Agents'
`BackendProtocol` over one collection:

```python
from deepagents.backends import CompositeBackend, StateBackend
from langchain_mddb import MddbBackend

backend = CompositeBackend(
    default=StateBackend(),
    routes={"/memories/": MddbBackend(collection="agent-memory", address="localhost:11024")},
)
```

| Protocol | MDDB |
|---|---|
| `write` | `Add`, key = path without the leading `/`, meta `path`, `dir`, `dirPrefix` |
| `read(offset, limit)` | `Get`, sliced by lines |
| `edit` | `Get`, then `UpdateDocument` with `expected_revision`: a concurrent edit returns an error instead of being overwritten |
| `ls`, `glob` | `Search` with `dirPrefix` (an indexed equality filter), glob matched here |
| `grep` | literal substring over every file under the path, at most 1000 matches |
| `delete` | `DeleteBatch` of the file and everything under it |
| `upload_files` / `download_files` | text as is, other bytes base64 with `meta.encoding` |
| `history(path)` | `ListRevisions` — every earlier version |

Paths must be absolute; a path with `..` is refused in the result's `error`,
never sent to the server.

## LangGraph Store (agent memory)

`MddbStore` (extra `store`) implements LangGraph's `BaseStore` — long-term
memory for graphs, Deep Agents' `StoreBackend`, LangMem — on one collection
(default `langgraph_store`). Every method has its async twin (`aput`, `aget`,
`asearch`, `adelete`, `alist_namespaces`); they run the same calls on a worker
thread, because the gRPC client blocks.

```python
from langchain_mddb import MddbStore

store = MddbStore(collection="agent-memory", address="localhost:11024",
                  ttl={"default_ttl": 60 * 24 * 30, "refresh_on_read": True})

ns = ("users", "u1", "semantic")
store.put(ns, "tea", {"content": "Prefers green tea", "kind": "preference"})
store.get(ns, "tea").value                       # {'content': 'Prefers green tea', 'kind': 'preference'}
store.search(("users", "u1"), query="drink")     # hybrid search, each item has .score
store.search(("users",), filter={"kind": "preference"}, limit=20)   # newest first
store.list_namespaces(prefix=("users",), max_depth=2)              # [('users', 'u1')]
store.put(ns, "raw", {"blob": "..."}, index=False)  # stored, filterable, not in query search
store.delete(ns, "tea")
```

How an item is stored:

| LangGraph | MDDB |
|---|---|
| `namespace` + `key` | document key `users/u1/semantic/tea`; `%`, `/` and `\|` in labels and keys are escaped, so `("a/b",)` and `("a", "b")` never collide |
| `value` | `content_md`, the value as JSON — nested dicts, lists and Unicode round-trip exactly |
| namespace | `meta.ns` = `users/u1/semantic`, `meta.nsPrefix` = every prefix of it |
| top-level scalars of `value` | `meta.value.<field>`, JSON-encoded, for `filter` |
| `index=False` | `meta.indexed` = `false` |
| `ttl` (minutes) | `SetTTL` (seconds), recorded in `meta.ttlSeconds` so reads can refresh it |

**Why the value is in the body and not in metadata.** Every metadata value is
also a key in MDDB's metadata index: that caps its length (a Deep Agents file
can be megabytes) and would make `list_namespaces` read every value back. The
body has neither problem, and it is what the server embeds and indexes.

**Search.** With `query`, `HybridSearch` restricted to the namespace prefix;
`score` is the combined keyword/vector score. Without `query`, items come
newest first (`updatedAt` descending), paged by `limit` and `offset`.

**Filters.** Equality on a top-level scalar — `{"kind": "preference"}` or
`{"kind": {"$eq": "preference"}}` — is answered by the metadata index; `3`
and `"3"` are different values. `$gt`, `$gte`, `$lt`, `$lte`, `$ne`, nested
objects, lists and strings over 256 characters raise `NotImplementedError`:
dropping a condition would silently widen the search.

**Embeddings.** MDDB embeds server-side with the collection's provider, and
embeds the stored value whole. `index={"dims": ..., "embed": ...}` is accepted
so a configuration ports over unchanged; `fields` other than `["$"]` emits a
warning, because it cannot narrow what the server embeds.

**Memory types are namespaces.** Semantic memory (facts) in
`("users", id, "semantic")`, episodic (interaction summaries) in
`("users", id, "episodic")`, procedural (the agent's instructions) in
`("agents", id, "procedural")`. `search(("users", id))` reads one user's
memory across all three.

### Deep Agents: files that outlive the thread

```python
from dataclasses import dataclass

from deepagents import create_deep_agent
from deepagents.backends import StoreBackend
from langchain_mddb import MddbStore


@dataclass
class Context:
    user_id: str


store = MddbStore(collection="agent-files")
agent = create_deep_agent(
    model=llm,  # any tool-calling chat model
    backend=StoreBackend(store=store, namespace=lambda rt: ("users", rt.context.user_id, "files")),
    store=store,
    context_schema=Context,
)
agent.invoke({"messages": [("user", "remember that I drink green tea")]},
             {"configurable": {"thread_id": "t1"}}, context=Context(user_id="alice"))
```

A later thread for the same user reads the files this one wrote; another
user's namespace does not see them.

## Development

```bash
pip install -e ".[test,client]"   # test pulls in langgraph and, on 3.11+, deepagents
pytest
```

The tests run against a fake client; no MDDB instance is needed.
