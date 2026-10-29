"""How LangGraph store items become MDDB documents and back (INT-020).

One item is one document. The layout, and why:

* **Document key** — the namespace labels and the item key, each escaped and
  joined with ``/``: ``("users", "u1")`` + ``"prefs"`` → ``users/u1/prefs``.
  Escaping ``%``, ``/`` and ``|`` keeps the mapping one-to-one — a label may
  itself contain ``/``, and without escaping ``("a/b",)`` and ``("a", "b")``
  would overwrite each other's items.
* **content_md** — the whole ``value`` as JSON. The value has to live in the
  document body: every metadata value is also a key in MDDB's metadata index,
  which caps its length and would make listing namespaces read every value.
  The body is also what the server embeds and full-text indexes.
* **meta.ns / meta.nsPrefix** — the escaped namespace path, and every prefix
  of it, so a namespace-prefix search is one equality filter on an index.
* **meta.value.<field>** — each top-level scalar of the value, JSON-encoded
  (``"fact"``, ``3``, ``true``), so ``filter={"type": "fact"}`` is answered by
  the metadata index and ``3`` never equals ``"3"``.
* **meta.indexed** — ``"false"`` for an item written with ``index=False``;
  such items are left out of query search.
* **meta.ttlSeconds** — the item's TTL, so a read can refresh it.
"""

from __future__ import annotations

import json
from datetime import datetime, timezone
from typing import Any, Dict, List, Optional, Tuple
from urllib.parse import unquote

from langgraph.store.base import Item, SearchItem

#: Metadata key prefix of the filterable value fields.
FIELD_PREFIX = "value."
#: Longest encoded scalar indexed for filtering; longer ones are not filterable.
MAX_FILTER_CHARS = 256


def escape(part: str) -> str:
    """Escapes the characters the key and the metadata index treat as structure."""
    return part.replace("%", "%25").replace("/", "%2F").replace("|", "%7C")


def ns_path(namespace: Tuple[str, ...]) -> str:
    """The namespace as one string: escaped labels joined with ``/``."""
    return "/".join(escape(label) for label in namespace)


def parse_ns(path: str) -> Tuple[str, ...]:
    """The inverse of ``ns_path``."""
    return tuple(unquote(part) for part in path.split("/"))


def doc_key(namespace: Tuple[str, ...], key: str) -> str:
    """The document key of an item."""
    return ns_path(namespace) + "/" + escape(key)


def encode_value(value: Dict[str, Any]) -> str:
    """The document body of an item: its value as JSON, Unicode kept as is."""
    return json.dumps(value, ensure_ascii=False)


def _scalar(value: Any) -> Optional[str]:
    """A scalar as it is stored for filtering, or None when it is not one.

    ``|`` becomes the JSON escape ``\\u007c``: the metadata index uses ``|`` as
    its separator, and the escaped form is still the same JSON value.
    """
    if value is not None and not isinstance(value, (str, int, float, bool)):
        return None
    encoded = json.dumps(value, ensure_ascii=False).replace("|", "\\u007c")
    return encoded if len(encoded) <= MAX_FILTER_CHARS else None


def item_meta(namespace: Tuple[str, ...], value: Dict[str, Any], *, indexed: bool,
              ttl_seconds: Optional[int]) -> Dict[str, List[str]]:
    """The metadata of an item's document."""
    meta = {
        "ns": [ns_path(namespace)],
        "nsPrefix": [ns_path(namespace[: i + 1]) for i in range(len(namespace))],
        "indexed": ["true" if indexed else "false"],
    }
    if ttl_seconds:
        meta["ttlSeconds"] = [str(ttl_seconds)]
    for field, raw in value.items():
        encoded = _scalar(raw)
        if encoded is not None:
            meta[FIELD_PREFIX + escape(str(field))] = [encoded]
    return meta


def filter_meta(filter_: Optional[Dict[str, Any]]) -> Dict[str, str]:
    """Translates a LangGraph filter into metadata equality conditions.

    Equality (``{"type": "fact"}``, or ``{"type": {"$eq": "fact"}}``) on a
    top-level scalar is what the metadata index answers. Anything else — the
    comparison operators, nested objects, lists, very long strings — raises
    NotImplementedError: a condition dropped in silence widens the search.
    """
    out: Dict[str, str] = {}
    for field, wanted in (filter_ or {}).items():
        if isinstance(wanted, dict) and set(wanted) == {"$eq"}:
            wanted = wanted["$eq"]
        encoded = _scalar(wanted)
        if encoded is None:
            raise NotImplementedError(
                f"MddbStore filters by equality on a top-level scalar field; the "
                f"condition on {field!r} ({wanted!r}) has no equivalent in MDDB's "
                "metadata index, and ignoring it would widen the search."
            )
        out[FIELD_PREFIX + escape(str(field))] = encoded
    return out


def ttl_seconds(minutes: Optional[float]) -> Optional[int]:
    """LangGraph TTLs are minutes, MDDB's are whole seconds (at least one)."""
    if minutes is None:
        return None
    return max(1, int(round(minutes * 60)))


def _first(doc: Any, name: str) -> Optional[str]:
    values = doc.meta.get(name)
    return values.values[0] if values is not None and values.values else None


def doc_ttl(doc: Any) -> Optional[int]:
    """The TTL an item was written with, in seconds, if any."""
    raw = _first(doc, "ttlSeconds")
    return int(raw) if raw else None


def to_item(doc: Any, score: Optional[float] = None, *, search: bool = False) -> Item:
    """Converts a document back into an ``Item`` (``SearchItem`` for search)."""
    path = _first(doc, "ns") or ""
    fields = dict(
        namespace=parse_ns(path),
        key=unquote(doc.key[len(path) + 1:]),
        value=json.loads(doc.content_md),
        created_at=datetime.fromtimestamp(doc.added_at, tz=timezone.utc),
        updated_at=datetime.fromtimestamp(doc.updated_at, tz=timezone.utc),
    )
    return SearchItem(score=score, **fields) if search else Item(**fields)


def match(namespace: Tuple[str, ...], condition: Any) -> bool:
    """Whether a namespace meets a prefix or suffix condition (``*`` = any label)."""
    path = tuple(condition.path)
    if len(namespace) < len(path):
        return False
    labels = namespace[: len(path)] if condition.match_type == "prefix" else namespace[len(namespace) - len(path):]
    return all(p == "*" or p == label for p, label in zip(path, labels, strict=True))
