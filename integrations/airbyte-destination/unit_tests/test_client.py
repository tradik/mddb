"""Unit tests for destination_mddb.client."""

from __future__ import annotations

import json
import logging
import unittest
from unittest.mock import MagicMock, patch

import requests

from destination_mddb.client import (
    MddbClient,
    _RedactingFilter,
    _extractKey,
    _flattenToStringLists,
    _hashFallback,
    _renderContentMd,
    _scalarToString,
    _stringifyValue,
    buildDocument,
)


class TestKeyExtraction(unittest.TestCase):
    def test_extractsConfiguredField(self):
        self.assertEqual(_extractKey({"id": "abc"}, "id"), "abc")

    def test_castsNonStringToString(self):
        self.assertEqual(_extractKey({"id": 42}, "id"), "42")

    def test_fallsBackToHashWhenMissing(self):
        record = {"name": "alice"}
        key = _extractKey(record, "id")
        self.assertEqual(key, _hashFallback(record))
        self.assertEqual(len(key), 40)

    def test_fallsBackToHashWhenEmptyString(self):
        key = _extractKey({"id": "   "}, "id")
        self.assertEqual(len(key), 40)

    def test_fallsBackToHashWhenNull(self):
        key = _extractKey({"id": None, "name": "bob"}, "id")
        self.assertEqual(len(key), 40)

    def test_hashIsDeterministic(self):
        record = {"b": 2, "a": 1}
        self.assertEqual(_hashFallback(record), _hashFallback({"a": 1, "b": 2}))

    def test_hashFallbackKeyIsUnchangedAcrossVersions(self):
        # Keys written by earlier versions must keep matching, or a re-sync
        # would create a second copy of every keyless record.
        self.assertEqual(_hashFallback({"b": 2, "a": 1}), "1744f53e00fc23bd3e515b298e42936485061dba")

    def test_oversizeKeyFallsBackToHash(self):
        # INT-006: a multi-KB key field must not become the document key.
        record = {"id": "x" * 5000}
        key = _extractKey(record, "id")
        self.assertEqual(key, _hashFallback(record))
        self.assertEqual(len(key), 40)

    def test_controlCharKeyFallsBackToHash(self):
        # INT-006: control characters (e.g. NUL/newline) -> hash fallback.
        record = {"id": "line1\nline2\x00"}
        key = _extractKey(record, "id")
        self.assertEqual(key, _hashFallback(record))


class TestMetaKeySanitization(unittest.TestCase):
    def test_metaKeyStripsPipeAndControlChars(self):
        # INT-006: the '|' index-key separator and control chars are removed.
        flat = _flattenToStringLists({"a|b\tc": "v"})
        self.assertIn("abc", flat)
        self.assertNotIn("a|b\tc", flat)

    def test_metaKeyLengthBounded(self):
        flat = _flattenToStringLists({"k" * 500: "v"})
        (only_key,) = flat.keys()
        self.assertLessEqual(len(only_key), 128)

    def test_emptyAfterSanitizationIsDropped(self):
        flat = _flattenToStringLists({"|||": "v", "ok": "w"})
        self.assertNotIn("", flat)
        self.assertIn("ok", flat)


class TestStringification(unittest.TestCase):
    def test_intToString(self):
        self.assertEqual(_scalarToString(42), "42")

    def test_floatToString(self):
        self.assertEqual(_scalarToString(3.14), "3.14")

    def test_boolLowercase(self):
        self.assertEqual(_scalarToString(True), "true")
        self.assertEqual(_scalarToString(False), "false")

    def test_dictBecomesJson(self):
        self.assertEqual(_scalarToString({"k": "v"}), '{"k": "v"}')

    def test_listInsideScalarBecomesJson(self):
        self.assertEqual(_scalarToString([1, 2]), "[1, 2]")

    def test_stringifyScalar(self):
        self.assertEqual(_stringifyValue("hello"), ["hello"])

    def test_stringifyList(self):
        self.assertEqual(_stringifyValue([1, "two", 3]), ["1", "two", "3"])

    def test_stringifyListDropsNone(self):
        self.assertEqual(_stringifyValue([1, None, 2]), ["1", "2"])


class TestFlatten(unittest.TestCase):
    def test_basicFlatten(self):
        result = _flattenToStringLists({"id": 1, "name": "alice", "active": True})
        self.assertEqual(result, {"id": ["1"], "name": ["alice"], "active": ["true"]})

    def test_dropsNoneValues(self):
        result = _flattenToStringLists({"id": 1, "deletedAt": None})
        self.assertNotIn("deletedAt", result)

    def test_nestedDictBecomesJsonString(self):
        result = _flattenToStringLists({"meta": {"x": 1}})
        self.assertEqual(result["meta"], ['{"x": 1}'])


class TestContentMd(unittest.TestCase):
    def test_wrapsInJsonFence(self):
        md = _renderContentMd({"a": 1}, emittedAt=None)
        self.assertIn("```json", md)
        self.assertIn('"a": 1', md)
        self.assertTrue(md.endswith("```\n"))

    def test_emittedAtComment(self):
        md = _renderContentMd({"a": 1}, emittedAt=1700000000)
        self.assertIn("<!-- emittedAt=1700000000 -->", md)


class TestBuildDocument(unittest.TestCase):
    def test_assemblesFullDocument(self):
        doc = buildDocument(
            collection="users",
            record={"id": "u-1", "name": "Alice"},
            keyField="id",
            language="en_US",
            emittedAt=123,
        )
        self.assertEqual(doc["collection"], "users")
        self.assertEqual(doc["key"], "u-1")
        self.assertEqual(doc["lang"], "en_US")
        self.assertEqual(doc["meta"]["name"], ["Alice"])
        self.assertIn("emittedAt=123", doc["contentMd"])

    def test_keyFallsBackToHashWhenFieldMissing(self):
        doc = buildDocument(
            collection="users",
            record={"name": "Bob"},
            keyField="id",
            language="en_US",
        )
        self.assertEqual(len(doc["key"]), 40)


class TestMddbClient(unittest.TestCase):
    def setUp(self):
        self.client = MddbClient(baseUrl="http://mddb.test/", apiKey="vk_test")

    def test_normalisesBaseUrl(self):
        self.assertEqual(self.client.baseUrl, "http://mddb.test")

    def test_setsBearerHeader(self):
        self.assertEqual(self.client.session.headers["Authorization"], "Bearer vk_test")

    def test_noAuthHeaderWhenKeyMissing(self):
        c = MddbClient(baseUrl="http://x", apiKey=None)
        self.assertNotIn("Authorization", c.session.headers)

    def test_pingHits404Gracefully(self):
        with patch.object(self.client.session, "post") as post:
            post.return_value = MagicMock(status_code=404, ok=False, text="not found")
            self.client.ping()
            post.assert_called_once()
            url = post.call_args[0][0]
            self.assertEqual(url, "http://mddb.test/v1/search")

    def test_pingHits405Gracefully(self):
        with patch.object(self.client.session, "post") as post:
            post.return_value = MagicMock(status_code=405, ok=False, text="method not allowed")
            self.client.ping()

    def test_pingSurfacesAuthFailure(self):
        with patch.object(self.client.session, "post") as post:
            post.return_value = MagicMock(status_code=401, ok=False, text="nope")
            with self.assertRaises(PermissionError):
                self.client.ping()

    def test_pingAcceptsOkAndRaisesOnOtherClientErrors(self):
        # Any other status goes through raise_for_status: 200 passes, a 400
        # surfaces as the HTTP error requests raises.
        with patch.object(self.client.session, "post") as post:
            ok = MagicMock(status_code=200, ok=True)
            post.return_value = ok
            self.client.ping()
            ok.raise_for_status.assert_called_once()

            bad = MagicMock(status_code=400, ok=False)
            bad.raise_for_status.side_effect = requests.HTTPError("400 Client Error")
            post.return_value = bad
            with self.assertRaises(requests.HTTPError):
                self.client.ping()

    def test_pingSurfacesServerError(self):
        with patch.object(self.client.session, "post") as post:
            post.return_value = MagicMock(status_code=500, ok=False, text="boom")
            with self.assertRaises(RuntimeError):
                self.client.ping()

    def test_pingErrorOmitsResponseBody(self):
        # INT-007: the untrusted server body must not appear in the exception.
        secret = "SECRET-token=vk_should_never_be_logged"
        with patch.object(self.client.session, "post") as post:
            post.return_value = MagicMock(status_code=500, ok=False, text=secret)
            with self.assertRaises(RuntimeError) as ctx:
                self.client.ping()
            self.assertNotIn(secret, str(ctx.exception))
            self.assertIn("HTTP 500", str(ctx.exception))

    def test_addDocumentErrorOmitsResponseBody(self):
        secret = "SECRET-body-internal-data"
        with patch.object(self.client.session, "post") as post:
            post.return_value = MagicMock(status_code=422, ok=False, text=secret)
            with self.assertRaises(RuntimeError) as ctx:
                self.client.addDocument({"key": "k"})
            self.assertNotIn(secret, str(ctx.exception))
            self.assertIn("HTTP 422", str(ctx.exception))

    def test_addDocumentSendsJson(self):
        with patch.object(self.client.session, "post") as post:
            post.return_value = MagicMock(status_code=200, ok=True, text="{}")
            self.client.addDocument({"collection": "c", "key": "k"})
            body = json.loads(post.call_args[1]["data"])
            self.assertEqual(body, {"collection": "c", "key": "k"})

    def test_addDocumentRaisesOnError(self):
        with patch.object(self.client.session, "post") as post:
            post.return_value = MagicMock(status_code=422, ok=False, text="bad payload")
            with self.assertRaises(RuntimeError):
                self.client.addDocument({"key": "k"})

    def test_addBatchCountsAccepted(self):
        with patch.object(self.client, "addDocument") as add:
            count = self.client.addBatch([{"key": "1"}, {"key": "2"}, {"key": "3"}])
            self.assertEqual(count, 3)
            self.assertEqual(add.call_count, 3)


if __name__ == "__main__":
    unittest.main()


class TestRedactingFilter(unittest.TestCase):
    """INT-007: the logging filter scrubs Bearer tokens from log records."""

    def _scrub(self, msg, args=None):
        rec = logging.LogRecord("airbyte", logging.INFO, "p", 1, msg, args, None)
        _RedactingFilter().filter(rec)
        return rec

    def test_redactsBearerInMessage(self):
        rec = self._scrub("Authorization: Bearer vk_super_secret done")
        self.assertNotIn("vk_super_secret", rec.msg)
        self.assertIn("Bearer ***", rec.msg)

    def test_redactsBearerInArgs(self):
        rec = self._scrub("header=%s", ("Bearer vk_arg_secret",))
        self.assertNotIn("vk_arg_secret", rec.args[0])

    def test_leavesPlainMessagesUntouched(self):
        rec = self._scrub("no token here")
        self.assertEqual(rec.msg, "no token here")
