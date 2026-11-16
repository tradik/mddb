"""MDDB gRPC client for Python.

    from mddb_client import MddbClient, mddb_pb2

    with MddbClient("localhost:11024") as client:
        doc = client.Add(mddb_pb2.AddRequest(
            collection="blog", key="post", lang="en",
            content_md="# Hello",
        ))
"""

from . import mddb_pb2, mddb_pb2_grpc
from .client import (
    DEFAULT_ADDRESS,
    DEFAULT_TIMEOUT_SECONDS,
    MddbClient,
)
from .leader import NO_LEADER, NOT_LEADER, LeaderInfo, leader_info

__all__ = [
    "MddbClient",
    "DEFAULT_ADDRESS",
    "DEFAULT_TIMEOUT_SECONDS",
    "LeaderInfo",
    "leader_info",
    "NOT_LEADER",
    "NO_LEADER",
    "mddb_pb2",
    "mddb_pb2_grpc",
]
