"""LangGraph ``BaseStore`` on an MDDB collection (INT-020).

LangGraph's long-term memory, Deep Agents' ``StoreBackend`` and LangMem all
read and write through ``BaseStore``. ``MddbStore`` keeps that store in one
MDDB collection, so agent memory gets what MDDB already has: hybrid search
over it, metadata filters answered by an index, and expiry.

    from langchain_mddb import MddbStore

    store = MddbStore(collection="agent-memory", address="localhost:11024")
    store.put(("users", "u1", "facts"), "tea", {"content": "Prefers green tea"})
    store.search(("users", "u1"), query="what does the user drink?")

The document layout is in ``_store_codec``. Two things differ from the
in-memory store:

* **MDDB embeds server-side**, with the collection's provider, and it embeds
  the stored value whole. ``index={"embed": ..., "dims": ...}`` is accepted so
  configurations port over, but no vectors are computed here; ``fields`` other
  than ``["$"]`` cannot narrow what the server embeds, and a warning says so.
* **Queries are hybrid** (keyword + vector), so ``search(query=...)`` finds
  matches even on a collection with no embedding provider.

The gRPC client is synchronous; the async methods run the same batch on a
worker thread, which keeps one code path for both.
"""

from __future__ import annotations

import asyncio
import warnings
from typing import Any, Iterable, List, Optional

import grpc
from langgraph.store.base import (
    BaseStore,
    GetOp,
    IndexConfig,
    Item,
    ListNamespacesOp,
    Op,
    PutOp,
    Result,
    SearchOp,
    TTLConfig,
    validate_op_namespace,
)

from . import _store_codec as codec
from .vectorstore import _open_client, _to_meta, _to_meta_filter

#: The collection a store uses when none is named.
DEFAULT_COLLECTION = "langgraph_store"


class MddbStore(BaseStore):
    """A LangGraph store kept in one MDDB collection."""

    supports_ttl = True

    def __init__(self, collection: str = DEFAULT_COLLECTION, client: Any = None, *,
                 address: str = "localhost:11024", index: Optional[IndexConfig] = None,
                 ttl: Optional[TTLConfig] = None, lang: str = "en", **client_kwargs: Any) -> None:
        """
        Args:
            collection: the MDDB collection that holds the store.
            client: an existing ``MddbClient``; without one the store opens its
                own to ``address`` (the gRPC listener) and closes it on close().
            index: LangGraph's index configuration. See the module docstring
                for what MDDB does with ``embed``, ``dims`` and ``fields``.
            ttl: LangGraph's TTL configuration: ``default_ttl`` (minutes) and
                ``refresh_on_read``. Expiry itself is MDDB's.
            lang: language recorded on every document.
        """
        self._owns_client = client is None
        self._client = client if client is not None else _open_client(address, **client_kwargs)
        self.collection = collection
        self.lang = lang
        self.index_config = index
        self.ttl_config = ttl
        if index and index.get("fields") not in (None, ["$"]):
            warnings.warn("MDDB embeds the whole stored value with the collection's provider; "
                          "index fields cannot narrow it", stacklevel=2)

    # -- BaseStore -----------------------------------------------------------

    def batch(self, ops: Iterable[Op]) -> List[Result]:
        """Runs the operations in order, so a get sees a put before it."""
        results: List[Result] = []
        for op in ops:
            validate_op_namespace(op)
            if isinstance(op, GetOp):
                results.append(self._get(op))
            elif isinstance(op, SearchOp):
                results.append(self._search(op))
            elif isinstance(op, PutOp):
                self._put(op)
                results.append(None)  # BaseStore: a put has no result
            elif isinstance(op, ListNamespacesOp):
                results.append(self._list_namespaces(op))
            else:
                raise ValueError(f"Unknown operation type: {type(op)}")
        return results

    async def abatch(self, ops: Iterable[Op]) -> List[Result]:
        """``batch`` on a worker thread: the gRPC client blocks."""
        return await asyncio.to_thread(self.batch, list(ops))

    # -- operations ----------------------------------------------------------

    def _pb2(self) -> Any:
        from mddb_client import mddb_pb2

        return mddb_pb2

    def _get(self, op: GetOp) -> Optional[Item]:
        pb2 = self._pb2()
        try:
            doc = self._client.Get(pb2.GetRequest(
                collection=self.collection, key=codec.doc_key(op.namespace, op.key), lang=self.lang))
        except grpc.RpcError as exc:
            if exc.code() == grpc.StatusCode.NOT_FOUND:
                return None
            raise
        if op.refresh_ttl:
            self._refresh([doc])
        return codec.to_item(doc)

    def _put(self, op: PutOp) -> None:
        pb2 = self._pb2()
        key = codec.doc_key(op.namespace, op.key)
        if op.value is None:
            try:
                self._client.DeleteDocument(pb2.DeleteDocumentRequest(
                    collection=self.collection, key=key, lang=self.lang))
            except grpc.RpcError as exc:
                if exc.code() != grpc.StatusCode.NOT_FOUND:
                    raise
            return None
        value = dict(op.value)
        seconds = codec.ttl_seconds(op.ttl)
        meta = codec.item_meta(op.namespace, value, indexed=op.index is not False, ttl_seconds=seconds)
        self._client.Add(pb2.AddRequest(
            collection=self.collection, key=key, lang=self.lang,
            content_md=codec.encode_value(value), meta=_to_meta(meta)))
        if seconds:
            # gRPC Add records no expiry of its own; SetTTL is the call that does.
            self._client.SetTTL(pb2.SetTTLRequest(
                collection=self.collection, key=key, lang=self.lang, ttl=seconds))
        return None

    def _search(self, op: SearchOp) -> List[Any]:
        pb2 = self._pb2()
        wanted = codec.filter_meta(op.filter)
        if op.namespace_prefix:
            wanted["nsPrefix"] = codec.ns_path(op.namespace_prefix)
        if op.query:
            wanted["indexed"] = "true"
            resp = self._client.HybridSearch(pb2.HybridSearchRequest(
                collection=self.collection, query=op.query, top_k=op.offset + op.limit,
                include_content=True, filter_meta=_to_meta_filter(wanted)))
            hits = [(r.document, float(r.combined_score)) for r in resp.results][op.offset:]
        else:
            resp = self._client.Search(pb2.SearchRequest(
                collection=self.collection, sort="updatedAt", asc=False, limit=op.limit,
                offset=op.offset, filter_meta=_to_meta_filter(wanted)))
            hits = [(doc, None) for doc in resp.documents]
        if op.refresh_ttl:
            self._refresh([doc for doc, _ in hits])
        return [codec.to_item(doc, score, search=True) for doc, score in hits]

    def _list_namespaces(self, op: ListNamespacesOp) -> List[tuple]:
        resp = self._client.GetMetaKeys(self._pb2().GetMetaKeysRequest(collection=self.collection))
        values = resp.meta.get("ns")
        found = {codec.parse_ns(v) for v in (values.values if values is not None else [])}
        found = {ns for ns in found if all(codec.match(ns, c) for c in op.match_conditions or ())}
        if op.max_depth is not None:
            found = {ns[: op.max_depth] for ns in found}
        return sorted(found)[op.offset: op.offset + op.limit]

    def _refresh(self, docs: List[Any]) -> None:
        """Restarts the expiry clock of every document that has one."""
        pb2 = self._pb2()
        for doc in docs:
            seconds = codec.doc_ttl(doc)
            if seconds:
                self._client.SetTTL(pb2.SetTTLRequest(
                    collection=self.collection, key=doc.key, lang=self.lang, ttl=seconds))

    # -- lifecycle -----------------------------------------------------------

    def close(self) -> None:
        """Closes the channel, unless the caller supplied it."""
        if self._owns_client:
            self._client.close()

    def __enter__(self) -> "MddbStore":
        return self

    def __exit__(self, *exc_info: Any) -> None:
        self.close()


__all__ = ["DEFAULT_COLLECTION", "MddbStore"]
