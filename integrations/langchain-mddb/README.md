# langchain-mddb

LangChain `VectorStore` and `Retriever` backed by
[MDDB](https://github.com/tradik/mddb).

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

## Development

```bash
pip install -e ".[test,client]"
pytest
```

The tests run against a fake client; no MDDB instance is needed.
