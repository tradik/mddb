"""Deep Agents backend over an MDDB collection (INT-021).

Deep Agents gives an agent a virtual filesystem — ls, read, write, edit, glob,
grep, delete — through ``BackendProtocol``. ``MddbBackend`` keeps those files
in MDDB: every file is a document keyed by its path, with ``path``, ``dir`` and
``dirPrefix`` metadata so a directory listing is one indexed query. What the
built-in backends do not have comes with it: revision history for every file,
full-text and semantic search over the same documents, and edits that cannot
silently overwrite one another.

That last point is the server-side half of this ticket. ``edit`` reads a file,
changes it and writes it back conditionally — ``expected_revision`` — so when
two subagents edit one file at once, one of them gets an error to retry
instead of a lost change.

    from deepagents.backends import CompositeBackend, StateBackend
    from langchain_mddb import MddbBackend

    backend = CompositeBackend(
        default=StateBackend(),
        routes={"/memories/": MddbBackend(collection="agent-memory")},
    )

Errors are returned in the result's ``error`` field, as the protocol expects;
nothing here raises for a missing file or a bad path.
"""

from __future__ import annotations

import base64
from typing import Any, Iterator, List, Optional

import grpc
from deepagents.backends.protocol import (
    BackendProtocol,
    DeleteResult,
    EditResult,
    FileDownloadResponse,
    FileInfo,
    FileUploadResponse,
    GlobResult,
    GrepMatch,
    GrepResult,
    LsResult,
    ReadResult,
    WriteResult,
)

from ._paths import InvalidPath, dir_of, file_meta, glob_match, key_for, normalize

#: Matches returned by one grep at most, as the built-in backends cap theirs.
MAX_GREP_MATCHES = 1000
#: Documents fetched per listing request.
PAGE_SIZE = 500


class MddbBackend(BackendProtocol):
    """A Deep Agents filesystem backend stored in one MDDB collection."""

    def __init__(self, collection: str, client: Any = None, *, address: str = "localhost:11024",
                 lang: str = "en", **client_kwargs: Any) -> None:
        """
        Args:
            collection: the MDDB collection that holds the files.
            client: an existing ``MddbClient``; without one the backend opens
                its own to ``address`` (the gRPC listener).
            lang: language recorded on every file document.
        """
        if client is None:  # pragma: no cover - needs a live server
            from mddb_client import MddbClient

            client = MddbClient(address, **client_kwargs)
        self._client = client
        self.collection = collection
        self.lang = lang

    # -- helpers -------------------------------------------------------------

    def _pb2(self) -> Any:
        from mddb_client import mddb_pb2

        return mddb_pb2

    def _get(self, path: str) -> Any:
        """The file's document, or None when there is none."""
        pb2 = self._pb2()
        try:
            return self._client.Get(pb2.GetRequest(collection=self.collection, key=key_for(path), lang=self.lang))
        except grpc.RpcError as exc:
            if exc.code() == grpc.StatusCode.NOT_FOUND:
                return None
            raise

    def _under(self, directory: str) -> Iterator[Any]:
        """Every file document at or below a directory, page by page."""
        pb2 = self._pb2()
        offset = 0
        while True:
            resp = self._client.Search(pb2.SearchRequest(
                collection=self.collection, sort="key", asc=True, limit=PAGE_SIZE, offset=offset,
                filter_meta={"dirPrefix": pb2.MetaValues(values=[directory])},
            ))
            docs = list(resp.documents)
            yield from docs
            if len(docs) < PAGE_SIZE:
                return
            offset += PAGE_SIZE

    @staticmethod
    def _path_of(doc: Any) -> str:
        values = doc.meta.get("path")
        return values.values[0] if values is not None and values.values else "/" + doc.key

    @staticmethod
    def _info(doc: Any) -> FileInfo:
        info: FileInfo = {"path": MddbBackend._path_of(doc), "is_dir": False, "size": len(doc.content_md.encode())}
        return info

    def _put(self, path: str, content: str, extra: Optional[dict] = None) -> None:
        pb2 = self._pb2()
        meta = {**file_meta(path), **(extra or {})}
        self._client.Add(pb2.AddRequest(
            collection=self.collection, key=key_for(path), lang=self.lang, content_md=content, save_revision=True,
            meta={k: pb2.MetaValues(values=v) for k, v in meta.items()},
        ))

    # -- BackendProtocol -----------------------------------------------------

    def ls(self, path: str) -> LsResult:
        try:
            directory = normalize(path)
        except InvalidPath as exc:
            return LsResult(error=str(exc))
        entries: dict = {}
        for doc in self._under(directory):
            p = self._path_of(doc)
            parent = dir_of(p)
            if parent == directory:
                entries[p] = self._info(doc)
                continue
            child = directory.rstrip("/") + "/" + p[len(directory):].lstrip("/").split("/")[0]
            entries.setdefault(child, {"path": child, "is_dir": True})
        return LsResult(entries=[entries[k] for k in sorted(entries)])

    def read(self, file_path: str, offset: int = 0, limit: int = 2000) -> ReadResult:
        try:
            path = normalize(file_path)
        except InvalidPath as exc:
            return ReadResult(error=str(exc))
        if limit <= 0:
            return ReadResult(file_data={"content": "", "encoding": "utf-8"}, no_lines_requested=True)
        doc = self._get(path)
        if doc is None:
            return ReadResult(error=f"File '{path}' not found")
        lines = doc.content_md.split("\n")
        start = max(offset, 0)
        window = lines[start:start + limit]
        data = {"content": "\n".join(window), "encoding": "utf-8"}
        if not window:  # past the end: no window to describe
            return ReadResult(file_data=data)
        end = start + len(window)
        return ReadResult(file_data=data, total_lines=len(lines), start_line=start + 1, end_line=end,
                          next_offset=end if end < len(lines) else None)

    def write(self, file_path: str, content: str) -> WriteResult:
        try:
            path = normalize(file_path)
        except InvalidPath as exc:
            return WriteResult(error=str(exc))
        self._put(path, content)
        return WriteResult(path=path)

    def edit(self, file_path: str, old_string: str, new_string: str, replace_all: bool = False) -> EditResult:
        try:
            path = normalize(file_path)
        except InvalidPath as exc:
            return EditResult(error=str(exc))
        if old_string == new_string:
            return EditResult(error="old_string and new_string are the same")
        doc = self._get(path)
        if doc is None:
            return EditResult(error=f"File '{path}' not found")
        count = doc.content_md.count(old_string) if old_string else 0
        if count == 0:
            return EditResult(error=f"String not found in file: {old_string!r}")
        if count > 1 and not replace_all:
            return EditResult(error=f"String appears {count} times in {path}; make it unique or set replace_all")
        updated = doc.content_md.replace(old_string, new_string, -1 if replace_all else 1)
        pb2 = self._pb2()
        try:
            self._client.UpdateDocument(pb2.UpdateDocumentRequest(
                collection=self.collection, key=key_for(path), lang=self.lang,
                content_md=updated, update_content=True, expected_revision=doc.revision,
            ))
        except grpc.RpcError as exc:
            if exc.code() == grpc.StatusCode.ABORTED:
                return EditResult(error=f"{path} changed since it was read; read it again and retry the edit")
            raise
        return EditResult(path=path, occurrences=count if replace_all else 1)

    def glob(self, pattern: str, path: Optional[str] = None) -> GlobResult:
        try:
            root = normalize(path)
        except InvalidPath as exc:
            return GlobResult(error=str(exc))
        matches = [self._info(d) for d in self._under(root) if glob_match(pattern, self._path_of(d), root)]
        return GlobResult(matches=sorted(matches, key=lambda m: m["path"]))

    def grep(self, pattern: str, path: Optional[str] = None, glob: Optional[str] = None, *,
             max_count: Optional[int] = None) -> GrepResult:
        """Literal substring search, as the protocol specifies (not regex).

        Exact by construction: every file under ``path`` is scanned, rather
        than trusting the full-text index, which tokenises and would miss a
        pattern such as ``TODO(`` or half a word.
        """
        try:
            root = normalize(path)
        except InvalidPath as exc:
            return GrepResult(error=str(exc))
        cap = min(max_count, MAX_GREP_MATCHES) if max_count else MAX_GREP_MATCHES
        found: List[GrepMatch] = []
        for doc in self._under(root):
            p = self._path_of(doc)
            if glob and not glob_match(glob, p, root):
                continue
            for n, line in enumerate(doc.content_md.split("\n"), start=1):
                if pattern in line:
                    if len(found) == cap:
                        return GrepResult(matches=found, truncated=True)
                    found.append({"path": p, "line": n, "text": line})
        return GrepResult(matches=found)

    def delete(self, file_path: str) -> DeleteResult:
        try:
            path = normalize(file_path)
        except InvalidPath as exc:
            return DeleteResult(error=str(exc))
        pb2 = self._pb2()
        targets = [] if path == "/" else ([key_for(path)] if self._get(path) is not None else [])
        targets += [d.key for d in self._under(path)]
        if not targets:
            return DeleteResult(error=f"Nothing exists at or under '{path}'")
        self._client.DeleteBatch(pb2.DeleteBatchRequest(collection=self.collection, documents=[
            pb2.DeleteDocument(key=k, lang=self.lang) for k in dict.fromkeys(targets)]))
        return DeleteResult(path=path)

    def upload_files(self, files: List[tuple]) -> List[FileUploadResponse]:
        """Text is stored as is; anything else base64-encoded and marked so."""
        out = []
        for raw_path, data in files:
            try:
                path = normalize(raw_path)
            except InvalidPath:
                out.append(FileUploadResponse(path=raw_path, error="invalid_path"))
                continue
            try:
                self._put(path, data.decode("utf-8"))
            except UnicodeDecodeError:
                self._put(path, base64.b64encode(data).decode("ascii"), {"encoding": ["base64"]})
            out.append(FileUploadResponse(path=path))
        return out

    def download_files(self, paths: List[str]) -> List[FileDownloadResponse]:
        out = []
        for raw_path in paths:
            try:
                path = normalize(raw_path)
            except InvalidPath:
                out.append(FileDownloadResponse(path=raw_path, content=None, error="invalid_path"))
                continue
            doc = self._get(path)
            if doc is None:
                out.append(FileDownloadResponse(path=path, content=None, error="file_not_found"))
                continue
            enc = doc.meta.get("encoding")
            binary = enc is not None and "base64" in enc.values
            content = base64.b64decode(doc.content_md) if binary else doc.content_md.encode("utf-8")
            out.append(FileDownloadResponse(path=path, content=content))
        return out

    def history(self, file_path: str) -> List[Any]:
        """Earlier versions of a file, newest first — the revision history
        MDDB keeps for every write, which no built-in backend has."""
        pb2 = self._pb2()
        resp = self._client.ListRevisions(pb2.ListRevisionsRequest(
            collection=self.collection, key=key_for(normalize(file_path)), lang=self.lang))
        return list(resp.revisions)
