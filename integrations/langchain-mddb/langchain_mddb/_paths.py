"""Path handling for MddbBackend (INT-021).

A Deep Agents backend speaks absolute POSIX paths ("/memories/notes.md").
MDDB speaks documents addressed by (collection, key, lang). These helpers are
the mapping, and the one place a path is checked: anything with a ".."
segment, or not starting with "/", is refused before it reaches a request.
"""

from __future__ import annotations

import posixpath
import re
from functools import lru_cache
from typing import Dict, List, Optional


class InvalidPath(ValueError):
    """A path the backend refuses: relative, or climbing with ".."."""


def normalize(path: Optional[str], *, default: str = "/") -> str:
    """Returns an absolute, clean path, or raises InvalidPath."""
    if path is None or path == "":
        path = default
    if not path.startswith("/"):
        raise InvalidPath(f"path must be absolute (start with '/'): {path!r}")
    if ".." in path.split("/"):
        raise InvalidPath(f"path must not contain '..': {path!r}")
    clean = posixpath.normpath(path)
    return "/" if clean in ("/", "//") else clean


def key_for(path: str) -> str:
    """The document key of a file path: the path without its leading "/"."""
    return path.lstrip("/")


def dir_of(path: str) -> str:
    """The directory holding a file path."""
    return posixpath.dirname(path) or "/"


def ancestors(path: str) -> List[str]:
    """Every directory above a file, root first: /a/b/c.md → /, /a, /a/b."""
    out = ["/"]
    parts = [p for p in dir_of(path).split("/") if p]
    for i in range(len(parts)):
        out.append("/" + "/".join(parts[: i + 1]))
    return out


def file_meta(path: str) -> Dict[str, List[str]]:
    """The metadata that makes a document findable as a file.

    ``dir`` answers ls (files directly in a directory); ``dirPrefix`` lists
    every ancestor, so "everything under /a" is one equality filter, which
    the server answers from its metadata index.
    """
    return {"path": [path], "dir": [dir_of(path)], "dirPrefix": ancestors(path)}


@lru_cache(maxsize=256)
def _glob_regex(pattern: str) -> "re.Pattern[str]":
    """Translates a glob into a regex: ** spans directories, * and ? do not."""
    out = []
    i = 0
    while i < len(pattern):
        c = pattern[i]
        if pattern.startswith("**/", i):
            out.append("(?:[^/]+/)*")
            i += 3
            continue
        if pattern.startswith("**", i):
            out.append(".*")
            i += 2
            continue
        if c == "*":
            out.append("[^/]*")
        elif c == "?":
            out.append("[^/]")
        elif c == "[":
            end = pattern.find("]", i + 1)
            if end < 0:
                out.append(re.escape(c))
            else:
                out.append("[" + pattern[i + 1 : end].replace("\\", "\\\\") + "]")
                i = end
        else:
            out.append(re.escape(c))
        i += 1
    return re.compile("".join(out) + r"\Z")


def glob_match(pattern: str, path: str, root: str) -> bool:
    """Matches a file against a glob the way Deep Agents' backends do.

    Without "/", the pattern matches the file name at any depth under root.
    With "/", it matches the path relative to root; a leading "/" anchors it
    to root. A name starting with "." matches only a segment that does too.
    """
    rel = path[len(root):].lstrip("/") if root != "/" else path.lstrip("/")
    if "/" not in pattern:
        name = posixpath.basename(path)
        if name.startswith(".") and not pattern.startswith("."):
            return False
        return bool(_glob_regex(pattern).match(name))
    pattern = pattern.lstrip("/")
    for seg_p, seg in zip(pattern.split("/"), rel.split("/")):
        if seg.startswith(".") and not seg_p.startswith("."):
            return False
    return bool(_glob_regex(pattern).match(rel))
