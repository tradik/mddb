"""HTTP client for MDDB REST API."""

from __future__ import annotations

import hashlib
import json
import logging
import re
from typing import Any, Dict, Iterable, List, Mapping, Optional

import requests
from requests.adapters import HTTPAdapter
from urllib3.util.retry import Retry

logger = logging.getLogger("airbyte")


class _RedactingFilter(logging.Filter):
    """INT-007: redact `Bearer <token>` from any log record so the API key can't
    leak into Airbyte platform logs (debug request dumps, future refactors)."""

    _BEARER = re.compile(r"Bearer\s+\S+", re.IGNORECASE)

    def filter(self, record: logging.LogRecord) -> bool:  # noqa: A003
        if isinstance(record.msg, str):
            record.msg = self._BEARER.sub("Bearer ***", record.msg)
        if record.args:
            record.args = tuple(
                self._BEARER.sub("Bearer ***", a) if isinstance(a, str) else a for a in record.args
            )
        return True


# Attach once so token-bearing strings are scrubbed wherever this logger is used.
if not any(isinstance(f, _RedactingFilter) for f in logger.filters):
    logger.addFilter(_RedactingFilter())

# INT-006: bound document keys and meta-key names derived from untrusted upstream
# records so a multi-megabyte/binary key field or a hostile field name can't
# create a pathological document key or pollute the MDDB meta schema.
MAX_KEY_LEN = 512
MAX_META_KEY_LEN = 128


class MddbClient:
    """Thin wrapper over MDDB /v1/* endpoints."""

    def __init__(
        self,
        baseUrl: str,
        apiKey: Optional[str] = None,
        timeoutSeconds: int = 30,
        verifySsl: bool = True,
    ) -> None:
        self.baseUrl = baseUrl.rstrip("/")
        self.timeout = timeoutSeconds
        self.verifySsl = verifySsl

        self.session = requests.Session()
        self.session.headers.update({"Content-Type": "application/json"})
        if apiKey:
            self.session.headers["Authorization"] = f"Bearer {apiKey}"

        retry = Retry(
            total=3,
            backoff_factor=0.5,
            status_forcelist=(429, 500, 502, 503, 504),
            allowed_methods=frozenset(["GET", "POST"]),
            raise_on_status=False,
        )
        adapter = HTTPAdapter(max_retries=retry)
        self.session.mount("http://", adapter)
        self.session.mount("https://", adapter)

    def ping(self) -> None:
        """Cheapest possible call that confirms URL + auth.

        Accepts: 2xx, 404 (path missing — instance is still alive), 405 (wrong method — alive too).
        Rejects: 401/403 (auth), 5xx (server unhealthy), connection errors.
        """
        url = f"{self.baseUrl}/v1/search"
        body = {"collection": "_airbyte_probe", "query": "*", "limit": 1}
        resp = self.session.post(url, data=json.dumps(body), timeout=self.timeout, verify=self.verifySsl)
        # INT-007: never echo the server response body — it is untrusted and
        # ends up in Airbyte platform logs / the connection UI. Status only.
        if resp.status_code in (401, 403):
            raise PermissionError(f"MDDB rejected credentials: HTTP {resp.status_code}")
        if resp.status_code >= 500:
            raise RuntimeError(f"MDDB server error: HTTP {resp.status_code}")
        if resp.status_code in (404, 405):
            return
        resp.raise_for_status()

    def addDocument(self, document: Mapping[str, Any]) -> None:
        url = f"{self.baseUrl}/v1/add"
        resp = self.session.post(
            url,
            data=json.dumps(document),
            timeout=self.timeout,
            verify=self.verifySsl,
        )
        if not resp.ok:
            # INT-007: status + our own key context only, never the server body.
            raise RuntimeError(
                f"MDDB /v1/add failed (HTTP {resp.status_code}) for key={document.get('key')!r}"
            )

    def addBatch(self, documents: Iterable[Mapping[str, Any]]) -> int:
        """Sequential per-doc upsert. MDDB exposes single-doc /v1/add only.

        Returns count of documents accepted.
        """
        count = 0
        for doc in documents:
            self.addDocument(doc)
            count += 1
        return count


def buildDocument(
    *,
    collection: str,
    record: Mapping[str, Any],
    keyField: str,
    language: str,
    emittedAt: Optional[int] = None,
) -> Dict[str, Any]:
    """Map an Airbyte record to an MDDB document.

    - `key` taken from record[keyField]; falls back to SHA-1 of the JSON-serialised record.
    - `meta` flattens record values to string lists (MDDB schema).
    - `contentMd` is the record serialised as a JSON code block (full-text search target).
    """
    key = _extractKey(record, keyField)
    return {
        "collection": collection,
        "key": key,
        "lang": language,
        "meta": _flattenToStringLists(record),
        "contentMd": _renderContentMd(record, emittedAt=emittedAt),
    }


def _extractKey(record: Mapping[str, Any], keyField: str) -> str:
    raw = record.get(keyField)
    if raw is None or (isinstance(raw, str) and not raw.strip()):
        return _hashFallback(record)
    key = str(raw)
    # INT-006: a huge/binary or control-character key field would create a
    # pathological document key — fall back to a stable content hash rather than
    # silently mutating the caller's key.
    if len(key) > MAX_KEY_LEN or not key.isprintable():
        return _hashFallback(record)
    return key


def _hashFallback(record: Mapping[str, Any]) -> str:
    """A stable document key for a record without a usable key field.

    SHA-1 names the record, it does not protect anything, hence
    usedforsecurity=False. It stays SHA-1 so keys written by earlier versions
    keep matching and a re-sync updates documents instead of duplicating them.
    """
    canonical = json.dumps(record, sort_keys=True, default=str, ensure_ascii=False)
    return hashlib.sha1(canonical.encode("utf-8"), usedforsecurity=False).hexdigest()


def _flattenToStringLists(record: Mapping[str, Any]) -> Dict[str, List[str]]:
    """MDDB meta is map[string][]string. Lists/scalars become string lists; dicts become a single JSON string."""
    flat: Dict[str, List[str]] = {}
    for key, value in record.items():
        if value is None:
            continue
        metaKey = _sanitizeMetaKey(str(key))
        if not metaKey:
            continue
        flat[metaKey] = _stringifyValue(value)
    return flat


def _sanitizeMetaKey(key: str) -> str:
    """INT-006: untrusted upstream field names become MDDB meta keys. Strip
    control characters and the '|' index-key separator, and bound the length, so
    a hostile field name can't break index keys or pollute the meta schema.
    """
    cleaned = "".join(ch for ch in key if ch.isprintable() and ch != "|").strip()
    return cleaned[:MAX_META_KEY_LEN]


def _stringifyValue(value: Any) -> List[str]:
    if isinstance(value, list):
        return [_scalarToString(item) for item in value if item is not None]
    return [_scalarToString(value)]


def _scalarToString(value: Any) -> str:
    if isinstance(value, (dict, list)):
        return json.dumps(value, sort_keys=True, default=str, ensure_ascii=False)
    if isinstance(value, bool):
        return "true" if value else "false"
    return str(value)


def _renderContentMd(record: Mapping[str, Any], *, emittedAt: Optional[int]) -> str:
    body = json.dumps(record, sort_keys=True, indent=2, default=str, ensure_ascii=False)
    if emittedAt is not None:
        return f"<!-- emittedAt={emittedAt} -->\n```json\n{body}\n```\n"
    return f"```json\n{body}\n```\n"
