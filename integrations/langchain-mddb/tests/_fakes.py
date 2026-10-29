"""Stand-ins for the protobuf messages and gRPC errors the fake servers use."""

from __future__ import annotations

from typing import Any, List

import grpc


class _Msg:
    def __init__(self, **kwargs: Any) -> None:
        self.__dict__.update(kwargs)

    def __getattr__(self, name: str) -> Any:
        return ""


class _MetaValues:
    def __init__(self, values: List[str]) -> None:
        self.values = list(values)


class _RpcError(grpc.RpcError):
    def __init__(self, code: grpc.StatusCode) -> None:
        super().__init__()
        self._code = code

    def code(self) -> grpc.StatusCode:
        return self._code
