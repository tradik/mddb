"""INT-021. MddbBackend maps Deep Agents' filesystem onto MDDB documents.

The fake below is a tiny in-memory MDDB: it keeps documents, answers the
dirPrefix filter the backend lists with, raises NOT_FOUND for a missing
document and ABORTED for a stale expected_revision — the server behaviours the
backend depends on.
"""

from __future__ import annotations

import hashlib
import sys
import threading
import types
from typing import Any, Dict, List, Tuple

import grpc
import pytest

pytest.importorskip("deepagents")

from _fakes import _Msg, _MetaValues, _RpcError  # noqa: E402


def _pb2() -> types.ModuleType:
    pb2 = types.ModuleType("mddb_client.mddb_pb2")
    for name in ("GetRequest", "SearchRequest", "AddRequest", "UpdateDocumentRequest", "DeleteBatchRequest",
                 "DeleteDocument", "ListRevisionsRequest"):
        setattr(pb2, name, _Msg)
    pb2.MetaValues = _MetaValues
    return pb2


class FakeMddb:
    """Documents by key, with revisions, like the server keeps them."""

    def __init__(self) -> None:
        self.docs: Dict[str, Dict[str, Any]] = {}
        self.history: Dict[str, List[str]] = {}
        self.lock = threading.Lock()
        self.fail_get = False

    def _doc(self, key: str) -> _Msg:
        d = self.docs[key]
        return _Msg(key=key, content_md=d["content"], meta={k: _MetaValues(v) for k, v in d["meta"].items()},
                    revision=d["rev"])

    def Get(self, req: Any) -> _Msg:
        if self.fail_get:
            raise _RpcError(grpc.StatusCode.UNAVAILABLE)
        if req.key not in self.docs:
            raise _RpcError(grpc.StatusCode.NOT_FOUND)
        return self._doc(req.key)

    def Add(self, req: Any) -> _Msg:
        with self.lock:
            meta = {k: list(v.values) for k, v in req.meta.items()}
            self._store(req.key, req.content_md, meta)
        return self._doc(req.key)

    def _store(self, key: str, content: str, meta: Dict[str, List[str]]) -> None:
        rev = hashlib.sha256((key + content + str(len(self.history.get(key, [])))).encode()).hexdigest()[:16]
        self.docs[key] = {"content": content, "meta": meta, "rev": rev}
        self.history.setdefault(key, []).append(content)

    def UpdateDocument(self, req: Any) -> _Msg:
        with self.lock:
            cur = self.docs.get(req.key)
            if cur is None:
                raise _RpcError(grpc.StatusCode.NOT_FOUND)
            if req.expected_revision and req.expected_revision != cur["rev"]:
                raise _RpcError(grpc.StatusCode.ABORTED)
            self._store(req.key, req.content_md, cur["meta"])
        return self._doc(req.key)

    def Search(self, req: Any) -> _Msg:
        want = req.filter_meta["dirPrefix"].values[0]
        keys = sorted(k for k, d in self.docs.items() if want in d["meta"].get("dirPrefix", []))
        page = keys[req.offset:req.offset + req.limit]
        return _Msg(documents=[self._doc(k) for k in page])

    def DeleteBatch(self, req: Any) -> _Msg:
        for d in req.documents:
            self.docs.pop(d.key, None)
        return _Msg(deleted=len(req.documents))

    def ListRevisions(self, req: Any) -> _Msg:
        return _Msg(revisions=list(reversed(self.history.get(req.key, []))))


@pytest.fixture(autouse=True)
def fake_client_module(monkeypatch):
    module = types.ModuleType("mddb_client")
    module.mddb_pb2 = _pb2()
    monkeypatch.setitem(sys.modules, "mddb_client", module)
    monkeypatch.setitem(sys.modules, "mddb_client.mddb_pb2", module.mddb_pb2)


@pytest.fixture
def fs() -> Tuple[Any, FakeMddb]:
    from langchain_mddb import MddbBackend

    server = FakeMddb()
    return MddbBackend(collection="mem", client=server), server


def test_write_read_ls(fs):
    backend, server = fs
    assert backend.write("/memories/notes.md", "one\ntwo\nthree").path == "/memories/notes.md"
    backend.write("/memories/deep/plan.md", "p")
    backend.write("/top.txt", "t")
    assert server.docs["memories/notes.md"]["meta"]["dirPrefix"] == ["/", "/memories"]

    r = backend.read("/memories/notes.md", offset=1, limit=1)
    assert r.file_data["content"] == "two" and (r.start_line, r.end_line, r.next_offset, r.total_lines) == (2, 2, 2, 3)
    assert backend.read("/memories/notes.md").next_offset is None
    assert backend.read("/memories/notes.md", limit=0).no_lines_requested
    assert "not found" in backend.read("/nope.md").error
    assert backend.read("/memories/notes.md", offset=10).start_line is None

    entries = backend.ls("/memories").entries
    assert [(e["path"], e["is_dir"]) for e in entries] == [("/memories/deep", True), ("/memories/notes.md", False)]
    assert [e["path"] for e in backend.ls("/").entries] == ["/memories", "/top.txt"]


def test_paths_are_checked(fs):
    backend, _ = fs
    for result in (backend.read("relative.md"), backend.write("/a/../b", "x"), backend.ls("x"),
                   backend.edit("../x", "a", "b"), backend.glob("*", "rel"), backend.grep("x", "rel"),
                   backend.delete("/a/../../etc")):
        assert result.error
        assert "path" in result.error


def test_edit_and_concurrent_edits(fs):
    backend, server = fs
    backend.write("/f.md", "alpha beta alpha")
    assert "2 times" in backend.edit("/f.md", "alpha", "x").error
    assert backend.edit("/f.md", "alpha", "x", replace_all=True).occurrences == 2
    assert "not found" in backend.edit("/f.md", "zzz", "y").error.lower()
    assert "same" in backend.edit("/f.md", "x", "x").error
    assert "not found" in backend.edit("/missing.md", "a", "b").error
    assert server.docs["f.md"]["content"] == "x beta x"

    # Two edits racing from the same read: one wins, one is told to retry.
    backend.write("/race.md", "counter 0")
    stale = server.docs["race.md"]["rev"]
    assert backend.edit("/race.md", "0", "1").path == "/race.md"
    original_get = server.Get

    def stale_get(req):
        doc = original_get(req)
        doc.revision = stale
        return doc

    server.Get = stale_get
    result = backend.edit("/race.md", "1", "2")
    assert "changed since it was read" in result.error
    assert server.docs["race.md"]["content"] == "counter 1"


def test_glob_grep(fs):
    backend, _ = fs
    backend.write("/src/app/main.py", "import os\n# TODO( fix\n")
    backend.write("/src/util.py", "x = 1  # TODO(\n")
    backend.write("/src/.hidden/a.py", "TODO(\n")
    backend.write("/docs/readme.md", "TODO( here too\n")
    # A bare pattern matches the file name at any depth, dot-directories included.
    assert [m["path"] for m in backend.glob("*.py").matches] == ["/src/.hidden/a.py", "/src/app/main.py", "/src/util.py"]
    assert [m["path"] for m in backend.glob("src/**/*.py").matches] == ["/src/app/main.py", "/src/util.py"]
    assert [m["path"] for m in backend.glob("/*.py", "/src").matches] == ["/src/util.py"]
    assert [m["path"] for m in backend.glob(".hidden/*.py", "/src").matches] == ["/src/.hidden/a.py"]
    assert backend.glob("[mu]*.py").matches[0]["path"] == "/src/app/main.py"

    found = backend.grep("TODO(", "/src", glob="*.py").matches
    assert [(m["path"], m["line"]) for m in found] == [("/src/.hidden/a.py", 1), ("/src/app/main.py", 2), ("/src/util.py", 1)]
    assert len(backend.grep("TODO(").matches) == 4
    capped = backend.grep("TODO(", max_count=2)
    assert len(capped.matches) == 2 and capped.truncated


def test_delete_upload_download_history(fs):
    backend, server = fs
    backend.write("/a/one.md", "1")
    backend.write("/a/b/two.md", "2")
    backend.write("/keep.md", "k")
    assert backend.delete("/a").path == "/a"
    assert list(server.docs) == ["keep.md"]
    assert "Nothing exists" in backend.delete("/a").error
    assert backend.delete("/keep.md").path == "/keep.md"

    up = backend.upload_files([("/t.txt", b"hello"), ("/b.bin", b"\xff\x00"), ("rel", b"x")])
    assert [u.error for u in up] == [None, None, "invalid_path"]
    down = backend.download_files(["/t.txt", "/b.bin", "/missing", "rel"])
    assert [d.content for d in down[:2]] == [b"hello", b"\xff\x00"]
    assert [d.error for d in down[2:]] == ["file_not_found", "invalid_path"]

    backend.write("/h.md", "v1")
    backend.write("/h.md", "v2")
    assert backend.history("/h.md") == ["v2", "v1"]


def test_paging_and_errors(fs, monkeypatch):
    from langchain_mddb import backend as module

    backend, server = fs
    monkeypatch.setattr(module, "PAGE_SIZE", 2)
    for i in range(5):
        backend.write(f"/p/{i}.md", str(i))
    assert len(backend.ls("/p").entries) == 5
    server.fail_get = True
    with pytest.raises(grpc.RpcError):
        backend.read("/p/0.md")
    server.fail_get = False

    def broken_update(req):
        raise _RpcError(grpc.StatusCode.UNAVAILABLE)

    server.UpdateDocument = broken_update
    with pytest.raises(grpc.RpcError):
        backend.edit("/p/1.md", "1", "one")


def test_paths_module():
    from langchain_mddb._paths import ancestors, glob_match, normalize

    assert normalize(None) == "/" and normalize("//") == "/" and normalize("/a//b/") == "/a/b"
    assert ancestors("/a/b/c.md") == ["/", "/a", "/a/b"] and ancestors("/c.md") == ["/"]
    assert glob_match("**/*.md", "/x/y.md", "/") and not glob_match("*.md", "/x/.y.md", "/")
    assert glob_match("a[", "/a[", "/")  # an unclosed class is literal
    assert glob_match("src/**", "/src/a/b.py", "/") and glob_match("?.md", "/x/a.md", "/")
    assert glob_match("a/*.md", "/r/a/x.md", "/r") and not glob_match("a/*.md", "/r/.a/x.md", "/r")


def test_lazy_export():
    import langchain_mddb

    assert langchain_mddb.MddbBackend.__name__ == "MddbBackend"
    with pytest.raises(AttributeError):
        langchain_mddb.Missing
