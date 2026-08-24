"""INT-017. The adapter is a mapping between two interfaces, so the tests are
about the mapping: what LangChain hands in, what MDDB receives, and what comes
back.

They run against a fake client rather than a live server. The thing that breaks
an adapter is a field named differently on the two sides, and that shows up
here without a gRPC connection.
"""

from __future__ import annotations

import sys
import types
from typing import Any, List

import pytest


# --- a fake mddb_client, installed before the adapter imports it ------------


class _MetaValues:
    def __init__(self, values: List[str]):
        self.values = list(values)

    def __eq__(self, other: Any) -> bool:
        return isinstance(other, _MetaValues) and self.values == other.values

    def __repr__(self) -> str:
        return f"MetaValues({self.values})"


class _Message:
    """A protobuf-ish message: keyword construction, attribute access."""

    def __init__(self, **kwargs: Any):
        self.__dict__.update(kwargs)

    def __getattr__(self, name: str) -> Any:
        # Unset protobuf fields read as their zero value rather than raising.
        return "" if name != "meta" else {}


def _make_pb2() -> types.ModuleType:
    pb2 = types.ModuleType("mddb_client.mddb_pb2")
    for name in (
        "BatchDocument", "AddBatchRequest", "DeleteBatchRequest",
        "DeleteBatchDocument", "FTSRequest", "VectorSearchRequest",
        "HybridSearchRequest", "SearchAdvisorRequest",
    ):
        setattr(pb2, name, _Message)
    pb2.MetaValues = _MetaValues
    return pb2


class FakeClient:
    """Records what the adapter sent and returns what it is told to."""

    def __init__(self) -> None:
        self.calls: list[tuple[str, Any]] = []
        self.fts_results: list[Any] = []
        self.vector_results: list[Any] = []
        self.hybrid_results: list[Any] = []
        self.closed = False

    def _record(self, name: str, req: Any) -> None:
        self.calls.append((name, req))

    def AddBatch(self, req):
        self._record("AddBatch", req)
        return _Message(added=len(req.documents))

    def DeleteBatch(self, req):
        self._record("DeleteBatch", req)
        return _Message(deleted=len(req.documents))

    def FTS(self, req):
        self._record("FTS", req)
        return _Message(results=self.fts_results)

    def VectorSearch(self, req):
        self._record("VectorSearch", req)
        return _Message(results=self.vector_results)

    def HybridSearch(self, req):
        self._record("HybridSearch", req)
        return _Message(results=self.hybrid_results)

    def SearchAdvisor(self, req):
        # Takes a request message, not a bare string. The first version of this
        # fake accepted a string, so the suite passed while the adapter failed
        # against a real server with "Exception serializing request!".
        self._record("SearchAdvisor", req)
        return _Message(search_type="hybrid", reasons=["because"])

    def close(self):
        self.closed = True

    @property
    def last(self):
        assert self.calls, "the adapter sent nothing"
        return self.calls[-1]


@pytest.fixture(autouse=True)
def fake_mddb_client(monkeypatch):
    """Installs a fake `mddb_client` package for the duration of a test."""
    module = types.ModuleType("mddb_client")
    module.mddb_pb2 = _make_pb2()
    module.MddbClient = lambda *a, **k: FakeClient()

    monkeypatch.setitem(sys.modules, "mddb_client", module)
    monkeypatch.setitem(sys.modules, "mddb_client.mddb_pb2", module.mddb_pb2)
    yield module


@pytest.fixture
def store():
    from langchain_mddb import MddbVectorStore

    client = FakeClient()
    return MddbVectorStore(collection="docs", client=client), client


def _result(key: str, text: str, score: float, *, combined: bool = False):
    doc = _Message(key=key, lang="en", id=f"docs|{key}|en", content_md=text, meta={})
    if combined:
        return _Message(document=doc, combined_score=score)
    return _Message(document=doc, score=score)


# --- writing ---------------------------------------------------------------


def test_add_texts_sends_one_batch(store):
    s, client = store

    keys = s.add_texts(
        ["first document", "second document"],
        [{"kind": "runbook"}, {"kind": "guide"}],
    )

    assert len(keys) == 2
    name, req = client.last
    assert name == "AddBatch", "documents should go in one request, not one each"
    assert req.collection == "docs"
    assert len(req.documents) == 2
    assert req.documents[0].content_md == "first document"
    assert req.documents[0].meta["kind"].values == ["runbook"]


def test_ids_become_keys(store):
    s, client = store

    keys = s.add_texts(["a", "b"], ids=["alpha", "beta"])

    assert keys == ["alpha", "beta"]
    _, req = client.last
    assert [d.key for d in req.documents] == ["alpha", "beta"]


def test_generated_keys_are_stable_for_the_same_text(store):
    """Re-running an ingest script should update, not double, the collection."""
    s, _ = store

    first = s.add_texts(["one", "two"])
    second = s.add_texts(["one", "two"])

    assert first == second


def test_metadata_lists_survive(store):
    s, client = store

    s.add_texts(["x"], [{"tag": ["go", "rust"], "count": 3}])

    _, req = client.last
    meta = req.documents[0].meta
    assert meta["tag"].values == ["go", "rust"]
    # Everything becomes a string: MDDB metadata is text, and a silent int
    # would come back as a string anyway.
    assert meta["count"].values == ["3"]


def test_mismatched_texts_and_metadatas_are_rejected(store):
    s, _ = store

    with pytest.raises(ValueError, match="positionally"):
        s.add_texts(["a", "b"], [{"only": "one"}])

    with pytest.raises(ValueError):
        s.add_texts(["a", "b"], ids=["only-one"])


def test_delete_by_key(store):
    s, client = store

    assert s.delete(["alpha", "beta"]) is True
    name, req = client.last
    assert name == "DeleteBatch"
    assert [d.key for d in req.documents] == ["alpha", "beta"]


def test_delete_nothing_returns_none(store):
    """LangChain reads None as "not attempted"; False would claim a failure."""
    s, client = store

    assert s.delete([]) is None
    assert s.delete(None) is None
    assert client.calls == []


# --- searching -------------------------------------------------------------


def test_similarity_search_defaults_to_hybrid(store):
    s, client = store
    client.hybrid_results = [_result("a.md", "content", 0.9, combined=True)]

    docs = s.similarity_search("a query", k=3)

    name, req = client.last
    assert name == "HybridSearch", "the default must follow MDDB, not the framework"
    assert req.query == "a query"
    assert req.top_k == 3
    assert docs[0].page_content == "content"
    assert docs[0].metadata["key"] == "a.md"


def test_search_type_can_be_set_per_store_and_per_call(fake_mddb_client):
    from langchain_mddb import MddbVectorStore

    client = FakeClient()
    client.vector_results = [_result("a.md", "c", 0.8)]
    client.fts_results = [_result("b.md", "c", 0.7)]

    s = MddbVectorStore(collection="docs", client=client, search_type="vector")
    s.similarity_search("q")
    assert client.last[0] == "VectorSearch"

    s.similarity_search("q", search_type="fts")
    assert client.last[0] == "FTS"


def test_scores_come_back_as_similarities(store):
    """Higher is better. LangChain distinguishes distance from similarity by
    method name, and this method promises the second."""
    s, client = store
    client.hybrid_results = [
        _result("a.md", "x", 0.91, combined=True),
        _result("b.md", "y", 0.42, combined=True),
    ]

    scored = s.similarity_search_with_score("q", k=2)

    assert [round(v, 2) for _, v in scored] == [0.91, 0.42]
    assert scored[0][1] > scored[1][1]


def test_filters_translate_to_metadata(store):
    s, client = store
    client.hybrid_results = []

    s.similarity_search("q", filter={"kind": "runbook", "tag": ["go", "rust"]})

    _, req = client.last
    assert req.filter_meta["kind"].values == ["runbook"]
    assert req.filter_meta["tag"].values == ["go", "rust"]


def test_operator_filters_are_rejected_not_dropped(store):
    """A dropped condition silently widens the search, which is worse than an
    error the caller can see."""
    s, _ = store

    with pytest.raises(ValueError, match="widen"):
        s.similarity_search("q", filter={"count": {"$gt": 3}})


def test_document_conversion_carries_provenance(store):
    s, client = store
    doc = _Message(
        key="runbooks/restart.md", lang="en", id="docs|restart|en",
        content_md="restart the service",
        meta={"kind": _MetaValues(["runbook"]), "tag": _MetaValues(["a", "b"])},
    )
    client.hybrid_results = [_Message(document=doc, combined_score=0.9)]

    result = s.similarity_search("q")[0]

    assert result.page_content == "restart the service"
    # A retrieved document that cannot be traced to its source is hard to act on.
    assert result.metadata["key"] == "runbooks/restart.md"
    assert result.metadata["lang"] == "en"
    # A single value is unwrapped; a list stays a list.
    assert result.metadata["kind"] == "runbook"
    assert result.metadata["tag"] == ["a", "b"]


def test_search_by_vector_refuses_rather_than_re_embedding(store):
    s, _ = store

    with pytest.raises(NotImplementedError, match="server-side"):
        s.similarity_search_by_vector([0.1, 0.2, 0.3])


# --- the rest of the interface ---------------------------------------------


def test_as_retriever_works(store):
    """The retriever is what LangChain chains actually use."""
    s, client = store
    client.hybrid_results = [_result("a.md", "content", 0.9, combined=True)]

    retriever = s.as_retriever(search_kwargs={"k": 2})
    docs = retriever.invoke("a query")

    assert len(docs) == 1
    assert docs[0].page_content == "content"


def test_from_texts_builds_and_populates(fake_mddb_client):
    from langchain_mddb import MddbVectorStore

    client = FakeClient()
    s = MddbVectorStore.from_texts(
        ["one", "two"], collection="built", client=client
    )

    assert isinstance(s, MddbVectorStore)
    assert s.collection == "built"
    assert client.last[0] == "AddBatch"


def test_recommended_settings_asks_the_server(store):
    s, client = store

    advice = s.recommended_settings()

    assert client.last[0] == "SearchAdvisor"
    assert client.last[1].collection == "docs"
    assert advice  # whatever shape, it must not be empty


def test_a_supplied_client_is_not_closed(store):
    """The caller who opened the channel owns it; closing someone else's would
    break the other stores sharing it."""
    s, client = store

    s.close()
    assert client.closed is False


def test_the_store_closes_a_channel_it_opened(fake_mddb_client):
    from langchain_mddb import MddbVectorStore

    opened = []
    fake_mddb_client.MddbClient = lambda *a, **k: opened.append(FakeClient()) or opened[-1]

    with MddbVectorStore(collection="docs") as s:
        assert s.collection == "docs"

    assert opened and opened[0].closed is True


def test_embeddings_property_is_none_by_default(store):
    """MDDB embeds server-side, and LangChain accepts None for stores that do."""
    s, _ = store
    assert s.embeddings is None
