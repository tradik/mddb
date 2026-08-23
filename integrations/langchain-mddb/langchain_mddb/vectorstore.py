"""LangChain VectorStore backed by MDDB (INT-017).

A developer choosing a retrieval backend inside LangChain picks from the list
that has adapters. MDDB was not on it, so it was not chosen — regardless of
what the engine does.

This is a thin layer. Everything it knows how to do, `mddb_client` already
does; the adapter's whole job is mapping LangChain's interface onto it. Where
the two disagree, the comments say which way and why, because a silent
mismatch between a framework's expectations and a backend's behaviour is the
kind of bug that surfaces as "retrieval got worse" six months later.

Two things worth knowing before using it:

* **MDDB embeds server-side.** LangChain's VectorStore contract assumes the
  caller owns an embedding function and passes vectors in. MDDB is configured
  with a provider and embeds on write, which means one embedding model per
  collection rather than one per application, and no vectors crossing the wire.
  `embedding` is accepted for interface compatibility and used only for
  `similarity_search_by_vector`.

* **Hybrid search is the default here.** LangChain's `similarity_search` means
  vector search; MDDB's best answer for most corpora is a keyword/vector blend.
  The default follows MDDB, and `search_type="vector"` gets the literal
  reading. Choosing the framework's default would mean this adapter returns
  worse results than the same server queried directly.
"""

from __future__ import annotations

from typing import Any, Iterable, List, Optional, Sequence, Tuple

from langchain_core.documents import Document
from langchain_core.embeddings import Embeddings
from langchain_core.vectorstores import VectorStore


class MddbVectorStore(VectorStore):
    """LangChain VectorStore over an MDDB collection."""

    def __init__(
        self,
        collection: str,
        client: Any = None,
        *,
        address: str = "localhost:11024",
        embedding: Optional[Embeddings] = None,
        search_type: str = "hybrid",
        lang: str = "en",
        text_key: str = "content_md",
        **client_kwargs: Any,
    ) -> None:
        """
        Args:
            collection: MDDB collection to read and write.
            client: an existing ``MddbClient``. Supplying one is how a caller
                shares a channel, interceptors or credentials; without it the
                store opens its own.
            address: host:port of the MDDB gRPC listener, used only when
                ``client`` is not given.
            embedding: LangChain embeddings, used only by
                ``similarity_search_by_vector``. MDDB embeds server-side; see
                the module docstring.
            search_type: ``"hybrid"`` (default), ``"vector"`` or ``"fts"``.
            lang: language recorded on documents this store writes.
            text_key: which field of an MDDB document becomes
                ``Document.page_content``.
        """
        if client is None:
            try:
                from mddb_client import MddbClient
            except ImportError as exc:  # pragma: no cover - import-time guard
                raise ImportError(
                    "langchain-mddb needs the MDDB client: "
                    "pip install 'langchain-mddb[client]'"
                ) from exc
            client = MddbClient(address, **client_kwargs)
            self._owns_client = True
        else:
            self._owns_client = False

        self._client = client
        self.collection = collection
        self.lang = lang
        self.text_key = text_key
        self.search_type = search_type
        self._embedding = embedding

    # -- LangChain interface ------------------------------------------------

    @property
    def embeddings(self) -> Optional[Embeddings]:
        """The embeddings object, if one was supplied.

        Usually ``None``: MDDB embeds on the server, and LangChain treats that
        as a legitimate answer for stores that do their own embedding.
        """
        return self._embedding

    def add_texts(
        self,
        texts: Iterable[str],
        metadatas: Optional[List[dict]] = None,
        *,
        ids: Optional[List[str]] = None,
        **kwargs: Any,
    ) -> List[str]:
        """Writes documents and returns their keys.

        MDDB documents are addressed by ``(collection, key, lang)`` rather than
        by an opaque id, so ``ids`` are used as keys. Without them the store
        generates keys, and the caller gets them back — an id it cannot predict
        is still an id it can delete.
        """
        from mddb_client import mddb_pb2

        texts = list(texts)
        metadatas = list(metadatas or [{} for _ in texts])
        if len(metadatas) != len(texts):
            raise ValueError(
                f"{len(texts)} texts but {len(metadatas)} metadatas; "
                "LangChain pairs them positionally"
            )

        keys = list(ids) if ids else [_generate_key(t, i) for i, t in enumerate(texts)]
        if len(keys) != len(texts):
            raise ValueError(f"{len(texts)} texts but {len(keys)} ids")

        documents = []
        for key, text, metadata in zip(keys, texts, metadatas):
            documents.append(
                mddb_pb2.BatchDocument(
                    key=key,
                    lang=self.lang,
                    content_md=text,
                    meta=_to_meta(metadata),
                )
            )

        # One request rather than one per document: the round trip dominates
        # everything else when a caller loads a corpus.
        self._client.AddBatch(
            mddb_pb2.AddBatchRequest(collection=self.collection, documents=documents)
        )
        return keys

    def delete(self, ids: Optional[List[str]] = None, **kwargs: Any) -> Optional[bool]:
        """Deletes documents by key.

        Returns ``None`` when given nothing to delete, which is LangChain's
        signal for "not attempted" — distinct from ``False``, which would claim
        the deletion failed.
        """
        from mddb_client import mddb_pb2

        if not ids:
            return None

        self._client.DeleteBatch(
            mddb_pb2.DeleteBatchRequest(
                collection=self.collection,
                documents=[
                    mddb_pb2.DeleteBatchDocument(key=key, lang=self.lang) for key in ids
                ],
            )
        )
        return True

    def similarity_search(
        self, query: str, k: int = 4, **kwargs: Any
    ) -> List[Document]:
        """Returns the k most relevant documents."""
        return [doc for doc, _ in self.similarity_search_with_score(query, k, **kwargs)]

    def similarity_search_with_score(
        self, query: str, k: int = 4, **kwargs: Any
    ) -> List[Tuple[Document, float]]:
        """Returns documents with their scores.

        LangChain's convention is that lower is better for a *distance* and
        higher for a *similarity*, and it distinguishes them by method name.
        This returns MDDB's similarity unchanged: higher is better.
        """
        from mddb_client import mddb_pb2

        search_type = kwargs.pop("search_type", self.search_type)
        filter_meta = _to_meta_filter(kwargs.pop("filter", None))

        if search_type == "fts":
            response = self._client.FTS(
                mddb_pb2.FTSRequest(
                    collection=self.collection, query=query, limit=k, **kwargs
                )
            )
            return [
                (_to_document(r.document, self.text_key), float(r.score))
                for r in response.results
            ]

        if search_type == "vector":
            response = self._client.VectorSearch(
                mddb_pb2.VectorSearchRequest(
                    collection=self.collection,
                    query=query,
                    top_k=k,
                    filter_meta=filter_meta,
                    **kwargs,
                )
            )
            return [
                (_to_document(r.document, self.text_key), float(r.score))
                for r in response.results
            ]

        response = self._client.HybridSearch(
            mddb_pb2.HybridSearchRequest(
                collection=self.collection,
                query=query,
                top_k=k,
                filter_meta=filter_meta,
                **kwargs,
            )
        )
        return [
            (_to_document(r.document, self.text_key), float(r.combined_score))
            for r in response.results
        ]

    def similarity_search_by_vector(
        self, embedding: List[float], k: int = 4, **kwargs: Any
    ) -> List[Document]:
        """Not supported: MDDB embeds the query server-side.

        Raised rather than silently re-embedding the text, because a caller
        reaching for this method is usually trying to keep two systems' vectors
        in the same space — and quietly using a different model would defeat
        exactly that.
        """
        raise NotImplementedError(
            "MDDB embeds queries server-side, so it takes query text rather than "
            "a vector. Use similarity_search(). If you need to search with your "
            "own vectors, they must be produced by the same model the collection "
            "was embedded with, which MDDB does not expose."
        )

    @classmethod
    def from_texts(
        cls,
        texts: List[str],
        embedding: Optional[Embeddings] = None,
        metadatas: Optional[List[dict]] = None,
        *,
        collection: str = "langchain",
        **kwargs: Any,
    ) -> "MddbVectorStore":
        """Builds a store and writes the texts into it."""
        store = cls(collection=collection, embedding=embedding, **kwargs)
        store.add_texts(texts, metadatas)
        return store

    # -- beyond the interface ----------------------------------------------

    def recommended_settings(self) -> dict:
        """Asks the server how this collection should be searched (SRCH-010).

        Not part of LangChain's interface, and the reason it is here: an
        application choosing `search_type` by hand is guessing, and the server
        has measured. Returns the recommendation with its reasons.
        """
        from mddb_client import mddb_pb2

        return _message_to_dict(
            self._client.SearchAdvisor(
                mddb_pb2.SearchAdvisorRequest(collection=self.collection)
            )
        )

    def close(self) -> None:
        """Closes the channel, unless the caller supplied it."""
        if self._owns_client:
            self._client.close()

    def __enter__(self) -> "MddbVectorStore":
        return self

    def __exit__(self, *exc_info: Any) -> None:
        self.close()


# -- conversions ------------------------------------------------------------


def _to_document(doc: Any, text_key: str) -> Document:
    """Converts an MDDB document into a LangChain one.

    Everything that is not the text becomes metadata, including the key and
    language — a retrieved document that cannot be traced back to its source is
    hard to act on, and LangChain has nowhere else to put them.
    """
    metadata: dict = {
        "key": doc.key,
        "lang": doc.lang,
        "id": doc.id,
    }
    for name, values in doc.meta.items():
        # MDDB metadata is multi-valued; LangChain filters are usually written
        # against single values, so a one-element list is unwrapped.
        vals = list(values.values)
        metadata[name] = vals[0] if len(vals) == 1 else vals

    return Document(page_content=getattr(doc, text_key, "") or "", metadata=metadata)


def _to_meta(metadata: dict) -> dict:
    """Converts LangChain metadata into MDDB's multi-valued form."""
    from mddb_client import mddb_pb2

    out = {}
    for key, value in (metadata or {}).items():
        if isinstance(value, (list, tuple, set)):
            values = [str(v) for v in value]
        else:
            values = [str(value)]
        out[str(key)] = mddb_pb2.MetaValues(values=values)
    return out


def _to_meta_filter(filter_: Optional[dict]) -> dict:
    """Converts a LangChain filter into MDDB's metadata filter.

    Only equality and membership are translated. LangChain's filter dialect
    varies per store and its richer forms have no equivalent here; raising is
    better than dropping a condition, which would silently widen the search.
    """
    from mddb_client import mddb_pb2

    if not filter_:
        return {}

    out = {}
    for key, value in filter_.items():
        if isinstance(value, dict):
            raise ValueError(
                f"filter on {key!r} uses an operator form that MDDB does not "
                "translate. Use a value or a list of values; anything else "
                "would have to be dropped, which would silently widen the search."
            )
        if isinstance(value, (list, tuple, set)):
            values = [str(v) for v in value]
        else:
            values = [str(value)]
        out[str(key)] = mddb_pb2.MetaValues(values=values)
    return out


def _message_to_dict(message: Any) -> dict:
    """Renders a protobuf message as a plain dict."""
    try:
        from google.protobuf.json_format import MessageToDict

        return MessageToDict(message, preserving_proto_field_name=True)
    except Exception:  # pragma: no cover - depends on protobuf version
        return {"raw": str(message)}


def _generate_key(text: str, index: int) -> str:
    """Builds a stable key for a text with no id.

    Content-derived rather than random, so loading the same corpus twice
    updates the same documents instead of doubling the collection — which is
    what a caller re-running an ingest script almost always wants.
    """
    import hashlib

    digest = hashlib.sha256(text.encode("utf-8")).hexdigest()[:16]
    return f"lc-{index:06d}-{digest}"


__all__ = ["MddbVectorStore"]
