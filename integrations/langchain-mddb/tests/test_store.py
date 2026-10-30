"""INT-020. MddbStore maps LangGraph's BaseStore onto MDDB documents.

The fake below is a small in-memory MDDB: documents with timestamps, the
metadata equality filter, updatedAt ordering, a keyword "hybrid" search with
scores, TTLs and the metadata-key listing — the server behaviour the store
depends on. Fake time ticks one second per write, so ordering is exact.
"""

from __future__ import annotations

import asyncio
import json
import sys
import types
from typing import Any, Dict, List

import grpc
import pytest

pytest.importorskip("langgraph")

from langgraph.store.base import GetOp, InvalidNamespaceError, ListNamespacesOp, PutOp  # noqa: E402

from _fakes import _Msg, _MetaValues, _RpcError  # noqa: E402


def _pb2() -> types.ModuleType:
    pb2 = types.ModuleType("mddb_client.mddb_pb2")
    for name in ("GetRequest", "AddRequest", "DeleteDocumentRequest", "SetTTLRequest", "SearchRequest",
                 "HybridSearchRequest", "GetMetaKeysRequest"):
        setattr(pb2, name, _Msg)
    pb2.MetaValues = _MetaValues
    return pb2


class FakeMddb:
    """Documents by key, as the server keeps them."""

    def __init__(self) -> None:
        self.docs: Dict[str, Dict[str, Any]] = {}
        self.ttl_calls: List[tuple] = []
        self.clock = 1_700_000_000
        self.fail: Dict[str, grpc.StatusCode] = {}
        self.requests: List[Any] = []
        self.closed = False

    def _check(self, name: str) -> None:
        if name in self.fail:
            raise _RpcError(self.fail[name])

    def _doc(self, key: str) -> _Msg:
        d = self.docs[key]
        return _Msg(key=key, content_md=d["content"], meta={k: _MetaValues(v) for k, v in d["meta"].items()},
                    added_at=d["added"], updated_at=d["updated"])

    def _matches(self, d: Dict[str, Any], filter_meta: Dict[str, _MetaValues]) -> bool:
        return all(set(want.values) & set(d["meta"].get(k, [])) for k, want in filter_meta.items())

    def Get(self, req: Any) -> _Msg:
        self._check("Get")
        if req.key not in self.docs:
            raise _RpcError(grpc.StatusCode.NOT_FOUND)
        return self._doc(req.key)

    def Add(self, req: Any) -> _Msg:
        self.clock += 1
        added = self.docs.get(req.key, {}).get("added", self.clock)
        self.docs[req.key] = {"content": req.content_md, "meta": {k: list(v.values) for k, v in req.meta.items()},
                              "added": added, "updated": self.clock}
        return self._doc(req.key)

    def DeleteDocument(self, req: Any) -> _Msg:
        self._check("DeleteDocument")
        if self.docs.pop(req.key, None) is None:
            raise _RpcError(grpc.StatusCode.NOT_FOUND)
        return _Msg(deleted=True)

    def SetTTL(self, req: Any) -> _Msg:
        self.ttl_calls.append((req.key, req.ttl))
        return self._doc(req.key)

    def Search(self, req: Any) -> _Msg:
        self.requests.append(req)
        assert (req.sort, req.asc) == ("updatedAt", False)
        keys = [k for k, d in self.docs.items() if self._matches(d, req.filter_meta)]
        keys.sort(key=lambda k: self.docs[k]["updated"], reverse=True)
        return _Msg(documents=[self._doc(k) for k in keys[req.offset:req.offset + req.limit]])

    def HybridSearch(self, req: Any) -> _Msg:
        self.requests.append(req)
        assert req.include_content
        words = req.query.lower().split()
        scored = []
        for k, d in self.docs.items():
            score = sum(d["content"].lower().count(w) for w in words)
            if score and self._matches(d, req.filter_meta):
                scored.append((score / 10, k))
        scored.sort(reverse=True)
        return _Msg(results=[_Msg(document=self._doc(k), combined_score=s) for s, k in scored[:req.top_k]])

    def GetMetaKeys(self, req: Any) -> _Msg:
        meta: Dict[str, List[str]] = {}
        for d in self.docs.values():
            for k, values in d["meta"].items():
                meta.setdefault(k, [])
                meta[k] += [v for v in values if v not in meta[k]]
        return _Msg(meta={k: _MetaValues(v) for k, v in meta.items()})

    def close(self) -> None:
        self.closed = True


@pytest.fixture(autouse=True)
def fake_client_module(monkeypatch):
    module = types.ModuleType("mddb_client")
    module.mddb_pb2 = _pb2()
    module.MddbClient = lambda address, **kw: FakeMddb()
    monkeypatch.setitem(sys.modules, "mddb_client", module)
    monkeypatch.setitem(sys.modules, "mddb_client.mddb_pb2", module.mddb_pb2)


@pytest.fixture
def mem():
    from langchain_mddb import MddbStore

    server = FakeMddb()
    return MddbStore(client=server), server


def test_put_get_round_trip(mem):
    store, server = mem
    value = {"content": "Zażółć gęślą jaźń — 東京 🚀", "nested": {"list": [1, {"deep": None}], "ok": True},
             "n": 3.5}
    store.put(("users", "u1", "facts"), "k1", value)
    item = store.get(("users", "u1", "facts"), "k1")
    assert item.value == value and item.key == "k1" and item.namespace == ("users", "u1", "facts")
    assert item.created_at == item.updated_at and item.created_at.tzinfo is not None

    doc = server.docs["users/u1/facts/k1"]
    assert json.loads(doc["content"]) == value and "東京" in doc["content"]
    assert doc["meta"]["ns"] == ["users/u1/facts"]
    assert doc["meta"]["nsPrefix"] == ["users", "users/u1", "users/u1/facts"]
    assert doc["meta"]["value.n"] == ["3.5"] and "value.nested" not in doc["meta"]

    store.put(("users", "u1", "facts"), "k1", {"content": "changed"})
    again = store.get(("users", "u1", "facts"), "k1")
    assert again.value == {"content": "changed"}
    assert again.created_at == item.created_at and again.updated_at > item.updated_at
    assert store.get(("users", "u1", "facts"), "missing") is None


def test_keys_and_labels_are_escaped(mem):
    store, server = mem
    store.put(("a/b",), "c", {"v": 1})
    store.put(("a", "b"), "c", {"v": 2})
    store.put(("p|q", "100%"), "x/y|z", {"v": 3})
    assert len(server.docs) == 3
    assert store.get(("a/b",), "c").value == {"v": 1} and store.get(("a", "b"), "c").value == {"v": 2}
    item = store.get(("p|q", "100%"), "x/y|z")
    assert (item.namespace, item.key, item.value) == (("p|q", "100%"), "x/y|z", {"v": 3})
    assert set(store.list_namespaces()) == {("a/b",), ("a", "b"), ("p|q", "100%")}


def test_delete(mem):
    store, server = mem
    store.put(("ns",), "k", {"v": 1})
    store.delete(("ns",), "k")
    assert store.get(("ns",), "k") is None
    store.delete(("ns",), "k")  # deleting nothing is not an error
    server.fail["DeleteDocument"] = grpc.StatusCode.UNAVAILABLE
    with pytest.raises(grpc.RpcError):
        store.delete(("ns",), "k")
    server.fail["Get"] = grpc.StatusCode.UNAVAILABLE
    with pytest.raises(grpc.RpcError):
        store.get(("ns",), "k")


def test_search_without_query_lists_newest_first(mem):
    store, server = mem
    for i in range(5):
        store.put(("docs", "u1"), f"k{i}", {"i": i, "type": "even" if i % 2 == 0 else "odd"})
    store.put(("docs", "u2"), "other", {"i": 9, "type": "even"})
    store.put(("elsewhere",), "x", {"i": 7})

    items = store.search(("docs", "u1"))
    assert [it.key for it in items] == ["k4", "k3", "k2", "k1", "k0"]
    assert items[0].score is None
    assert [it.key for it in store.search(("docs", "u1"), limit=2, offset=1)] == ["k3", "k2"]
    assert {it.key for it in store.search(("docs",))} == {"k0", "k1", "k2", "k3", "k4", "other"}
    assert len(store.search(())) == 7

    evens = store.search(("docs",), filter={"type": "even"})
    assert [it.key for it in evens] == ["other", "k4", "k2", "k0"]
    assert [it.key for it in store.search(("docs",), filter={"i": {"$eq": 3}})] == ["k3"]
    assert store.search(("docs",), filter={"i": "3"}) == []  # 3 is not "3"


@pytest.mark.parametrize("bad", [{"i": {"$gt": 1}}, {"i": {"$ne": 1}}, {"tags": ["a"]}, {"obj": {"a": 1}},
                                 {"text": "x" * 300}])
def test_unsupported_filters_raise(mem, bad):
    store, _ = mem
    with pytest.raises(NotImplementedError, match="widen the search"):
        store.search(("docs",), filter=bad)


def test_search_with_query_scores_and_skips_unindexed(mem):
    store, server = mem
    store.put(("mem", "u1"), "tea", {"content": "likes green tea, tea every day"})
    store.put(("mem", "u1"), "coffee", {"content": "no coffee, one tea"})
    store.put(("mem", "u1"), "secret", {"content": "tea tea tea tea"}, index=False)
    store.put(("mem", "u2"), "tea", {"content": "tea"})

    hits = store.search(("mem", "u1"), query="tea")
    assert [(h.key, h.score) for h in hits] == [("tea", 0.2), ("coffee", 0.1)]
    assert server.docs["mem/u1/secret"]["meta"]["indexed"] == ["false"]
    assert [h.key for h in store.search(("mem", "u1"), query="tea", offset=1)] == ["coffee"]
    assert store.search(("mem",), query="tea", filter={"content": "tea"})[0].namespace == ("mem", "u2")


def test_list_namespaces(mem):
    store, _ = mem
    assert store.list_namespaces() == []
    for ns in [("a", "b", "c"), ("a", "b", "d"), ("a", "x"), ("z", "b", "c")]:
        store.put(ns, "k", {"v": 1})
    assert store.list_namespaces() == [("a", "b", "c"), ("a", "b", "d"), ("a", "x"), ("z", "b", "c")]
    assert store.list_namespaces(prefix=("a", "b")) == [("a", "b", "c"), ("a", "b", "d")]
    assert store.list_namespaces(suffix=("c",)) == [("a", "b", "c"), ("z", "b", "c")]
    assert store.list_namespaces(prefix=("*", "b")) == [("a", "b", "c"), ("a", "b", "d"), ("z", "b", "c")]
    assert store.list_namespaces(prefix=("a", "b", "c", "d")) == []
    assert store.list_namespaces(max_depth=1) == [("a",), ("z",)]
    assert store.list_namespaces(limit=2, offset=1) == [("a", "b", "d"), ("a", "x")]


def test_ttl_default_override_and_refresh():
    from langchain_mddb import MddbStore

    server = FakeMddb()
    store = MddbStore(client=server, ttl={"default_ttl": 2, "refresh_on_read": True})
    store.put(("t",), "default", {"v": 1})
    store.put(("t",), "short", {"v": 2}, ttl=0.5)
    store.put(("t",), "forever", {"v": 3}, ttl=None)
    assert server.ttl_calls == [("t/default", 120), ("t/short", 30)]
    assert server.docs["t/default"]["meta"]["ttlSeconds"] == ["120"]

    server.ttl_calls.clear()
    store.get(("t",), "default")
    store.get(("t",), "forever")
    store.get(("t",), "short", refresh_ttl=False)
    assert server.ttl_calls == [("t/default", 120)]
    server.ttl_calls.clear()
    store.search(("t",))
    assert sorted(server.ttl_calls) == [("t/default", 120), ("t/short", 30)]

    quiet = MddbStore(client=server, ttl={"refresh_on_read": False})
    server.ttl_calls.clear()
    quiet.get(("t",), "default")
    quiet.search(("t",), refresh_ttl=False)
    assert server.ttl_calls == []
    assert MddbStore(client=server).supports_ttl


def test_async_methods(mem):
    store, _ = mem

    async def run():
        await store.aput(("a", "u1"), "k", {"text": "hello async"})
        got = await store.aget(("a", "u1"), "k")
        found = await store.asearch(("a",), query="hello")
        spaces = await store.alist_namespaces(prefix=("a",))
        await store.adelete(("a", "u1"), "k")
        gone = await store.aget(("a", "u1"), "k")
        return got, found, spaces, gone

    got, found, spaces, gone = asyncio.run(run())
    assert got.value == {"text": "hello async"} and found[0].score > 0
    assert spaces == [("a", "u1")] and gone is None


def test_batch_validation(mem):
    store, _ = mem
    results = store.batch([PutOp(("b",), "k", {"v": 1}), GetOp(("b",), "k"), ListNamespacesOp()])
    assert results[0] is None and results[1].value == {"v": 1} and results[2] == [("b",)]
    with pytest.raises(InvalidNamespaceError):
        store.batch([GetOp(("has.dot",), "k")])
    with pytest.raises(ValueError, match="Unknown operation"):
        store.batch([object()])


def test_index_config_and_lifecycle():
    from langchain_mddb import MddbStore
    from langchain_mddb.store import DEFAULT_COLLECTION

    server = FakeMddb()
    assert MddbStore(client=server, index={"dims": 3, "embed": lambda t: [[0.0] * 3], "fields": ["$"]})
    with pytest.warns(UserWarning, match="whole stored value"):
        MddbStore(client=server, index={"dims": 3, "embed": lambda t: [], "fields": ["content"]})

    with MddbStore(address="localhost:1") as owned:
        assert owned.collection == DEFAULT_COLLECTION
        inner = owned._client
    assert inner.closed
    with MddbStore(client=server):
        pass
    assert not server.closed


def test_lazy_export():
    import langchain_mddb

    assert langchain_mddb.MddbStore.__name__ == "MddbStore"


def test_deep_agents_store_backend(mem):
    """Deep Agents keeps an agent's files in the store, one namespace per user."""
    pytest.importorskip("deepagents")
    from deepagents.backends import StoreBackend

    store, server = mem
    alice = StoreBackend(store=store, namespace=lambda rt: ("users", "alice", "files"))
    bob = StoreBackend(store=store, namespace=lambda rt: ("users", "bob", "files"))
    assert alice.write("/notes.md", "Alice: ünïcödé\nline 2").error is None
    assert bob.write("/notes.md", "Bob").error is None
    assert alice.read("/notes.md").file_data["content"] == "Alice: ünïcödé\nline 2"
    assert "Bob" in bob.read("/notes.md").file_data["content"]
    assert [e["path"] for e in alice.ls("/").entries] == ["/notes.md"]
    assert store.list_namespaces(prefix=("users",)) == [("users", "alice", "files"), ("users", "bob", "files")]
