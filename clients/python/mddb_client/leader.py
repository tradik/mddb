"""Cluster leader information carried by a refused write (GO-051).

A node of an mddbd cluster that is not the leader refuses a write with gRPC
status FAILED_PRECONDITION ("not the leader ...") and a ``google.rpc.ErrorInfo``
detail ``{reason: "NOT_LEADER", domain: "mddb", metadata: {leader_addr,
leader_id, term, leader_grpc}}``. While an election runs it answers UNAVAILABLE with reason
``NO_LEADER``. ``leader_addr`` is the leader's HTTP base URL, ``leader_grpc`` (when the server
sends it) its gRPC address.

The detail travels in the ``grpc-status-details-bin`` trailer as an encoded
``google.rpc.Status``. It is decoded here by hand — three small messages — so
the client needs no extra dependency for it.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Iterator, Optional, Tuple
from urllib.parse import urlsplit

import grpc

# ErrorInfo reasons the server sends.
NOT_LEADER = "NOT_LEADER"
NO_LEADER = "NO_LEADER"

_DETAILS_KEY = "grpc-status-details-bin"
_ERROR_INFO_TYPE = "type.googleapis.com/google.rpc.ErrorInfo"
_NOT_LEADER_MESSAGE = "not the leader"

# Wire types of the protobuf encoding.
_VARINT, _I64, _LEN, _I32 = 0, 1, 2, 5


@dataclass(frozen=True)
class LeaderInfo:
    """Why a write was refused, and where it belongs.

    ``reason`` is ``NOT_LEADER`` or ``NO_LEADER``. ``leader_addr`` is the
    leader's HTTP base URL (empty for ``NO_LEADER``), ``term`` the election
    term or ``None`` when the server did not say, ``leader_grpc`` the
    leader's gRPC address when the server sent it.
    """

    reason: str
    leader_addr: str = ""
    leader_id: str = ""
    term: Optional[int] = None
    leader_grpc: str = ""


def leader_info(error: Any) -> Optional[LeaderInfo]:
    """Returns the leader information of a refused write, or ``None``.

    Works on the ``grpc.RpcError`` a call raises (which is also a
    ``grpc.Call``); anything else, and any other status, gives ``None``.
    """
    if not (isinstance(error, grpc.RpcError) and isinstance(error, grpc.Call)):
        return None
    info = _from_details(_trailer(error, _DETAILS_KEY))
    if info is not None:
        return info
    # A proxy may drop the trailer; the status itself still says enough.
    if error.code() == grpc.StatusCode.FAILED_PRECONDITION and (error.details() or "").startswith(
        _NOT_LEADER_MESSAGE
    ):
        return LeaderInfo(reason=NOT_LEADER)
    return None


def annotate(error: grpc.RpcError) -> Optional[LeaderInfo]:
    """Sets ``leader_addr``, ``leader_grpc``, ``leader_id`` and ``term`` on a refused write.

    The exception stays the one gRPC raised, so existing ``except
    grpc.RpcError`` handlers keep working; a caller reads the leader from it.
    """
    info = leader_info(error)
    if info is not None:
        error.leader_addr = info.leader_addr
        error.leader_id = info.leader_id
        error.leader_grpc = info.leader_grpc
        error.term = info.term
    return info


def grpc_target(leader_addr: str, home_address: str) -> Optional[str]:
    """Maps a leader's HTTP base URL to a gRPC target.

    The host comes from the URL; the port from ``home_address``, the
    configured node, since the nodes of a cluster normally listen on the same
    gRPC port. Only an absolute ``http``/``https`` URL is accepted.
    """
    try:
        url = urlsplit(leader_addr)
        host = url.hostname
    except ValueError:
        return None
    if url.scheme not in ("http", "https") or not host:
        return None
    if ":" in host:
        host = f"[{host}]"
    _, _, port = home_address.rpartition(":")
    if not port.isdigit():
        port = "11024"
    return f"{host}:{port}"


def _trailer(error: grpc.Call, key: str) -> Optional[bytes]:
    for name, value in error.trailing_metadata() or ():
        if name == key and isinstance(value, bytes):
            return value
    return None


def _from_details(data: Optional[bytes]) -> Optional[LeaderInfo]:
    if not data:
        return None
    try:
        for num, wire, value in _fields(data):
            if num == 3 and wire == _LEN:
                info = _from_any(value)
                if info is not None:
                    return info
    except ValueError:
        return None
    return None


def _from_any(data: bytes) -> Optional[LeaderInfo]:
    """Decodes a google.protobuf.Any; only an ErrorInfo with a known reason counts."""
    fields = {num: value for num, wire, value in _fields(data) if wire == _LEN}
    if fields.get(1, b"").decode() != _ERROR_INFO_TYPE:
        return None
    reason, metadata = "", {}
    for num, wire, value in _fields(fields.get(2, b"")):
        if wire != _LEN:
            continue
        if num == 1:
            reason = value.decode()
        elif num == 3:
            entry = {n: v.decode() for n, w, v in _fields(value) if w == _LEN}
            metadata[entry.get(1, "")] = entry.get(2, "")
    if reason not in (NOT_LEADER, NO_LEADER):
        return None
    term = metadata.get("term", "")
    return LeaderInfo(
        reason=reason,
        leader_addr=metadata.get("leader_addr", ""),
        leader_id=metadata.get("leader_id", ""),
        term=int(term) if term.isdigit() else None,
        leader_grpc=metadata.get("leader_grpc", ""),
    )


def _fields(data: bytes) -> Iterator[Tuple[int, int, Any]]:
    """Yields (field number, wire type, value) of an encoded message."""
    pos = 0
    while pos < len(data):
        key, pos = _varint(data, pos)
        wire = key & 7
        if wire == _VARINT:
            value, pos = _varint(data, pos)
        elif wire == _LEN:
            size, pos = _varint(data, pos)
            value, pos = data[pos : pos + size], pos + size
        elif wire in (_I64, _I32):
            width = 8 if wire == _I64 else 4
            value, pos = data[pos : pos + width], pos + width
        else:
            raise ValueError(f"unsupported wire type {wire}")
        if pos > len(data):
            raise ValueError("truncated message")
        yield key >> 3, wire, value


def _varint(data: bytes, pos: int) -> Tuple[int, int]:
    result = shift = 0
    while True:
        if pos >= len(data):
            raise ValueError("truncated varint")
        byte = data[pos]
        pos += 1
        result |= (byte & 0x7F) << shift
        if not byte & 0x80:
            return result, pos
        shift += 7
