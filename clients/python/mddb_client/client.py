"""MDDB Python client.

TEST-001: this package exported nothing. ``mddb_client/__init__.py`` was empty
and ``pyproject.toml`` pointed ``readme`` at ``example.py``, so ``import
mddb_client`` gave you a namespace with the generated protobuf modules in it
and no client. There was no public API to smoke-test.

Every unary RPC in ``mddb.proto`` is reachable as a method of the same name —
``Add``, ``Get``, ``Search``, and the seventy-odd others. They are taken from
the generated stub rather than written out, so a new RPC works here as soon as
the proto carries it.
"""

from __future__ import annotations

import threading
import time
from typing import Any, Callable, Iterable, Optional, Tuple

import grpc

from . import mddb_pb2_grpc
from .leader import NO_LEADER, NOT_LEADER, LeaderInfo, annotate, grpc_target

# Default gRPC port of a local mddbd.
DEFAULT_ADDRESS = "localhost:11024"

# Per-call deadline. Without one, a call to a server that accepted the
# connection and then stopped answering waits forever.
DEFAULT_TIMEOUT_SECONDS = 30.0

# GO-051: how often, and how long apart, a write refused while a cluster
# elects its leader is tried again when follow_leader is on.
NO_LEADER_RETRIES = 3
NO_LEADER_WAIT_SECONDS = 1.0


class MddbClient:
    """A thin, typed-by-protobuf wrapper over the generated MDDB stub.

    INT-011: the channel is insecure by default because the server's own
    default is a loopback port with no TLS. Anything reachable from another
    machine must pass ``secure=True``, or the API key travels in cleartext.

    GO-051: in a cluster only the leader accepts writes. A write refused by a
    follower raises the usual ``grpc.RpcError`` with ``leader_addr``,
    ``leader_id`` and ``term`` set on it. With ``follow_leader=True`` the
    client sends such a write once more, to the leader, and remembers it: an
    RPC that was redirected once is a write, so later calls of it go to the
    leader directly, while every other RPC keeps using ``address``. The
    leader's gRPC target is ``leader_resolver(leader_addr)`` when a resolver
    is given, else the ``leader_grpc`` address the server sent, else the host
    of its HTTP URL with the port of ``address``.
    """

    def __init__(
        self,
        address: str = DEFAULT_ADDRESS,
        *,
        secure: bool = False,
        root_certificates: Optional[bytes] = None,
        api_key: Optional[str] = None,
        token: Optional[str] = None,
        timeout: Optional[float] = DEFAULT_TIMEOUT_SECONDS,
        channel_options: Optional[Iterable[tuple]] = None,
        channel: Optional[grpc.Channel] = None,
        follow_leader: bool = False,
        leader_resolver: Optional[Callable[[str], Optional[str]]] = None,
    ) -> None:
        self.address = address
        self.timeout = timeout
        self.follow_leader = follow_leader
        self._leader_resolver = leader_resolver
        self._channel_args = {
            "secure": secure,
            "root_certificates": root_certificates,
            "options": list(channel_options or ()),
        }
        self._lock = threading.Lock()
        self._leader: Optional[str] = None
        self._leader_channel: Optional[grpc.Channel] = None
        self._leader_stub: Optional[mddb_pb2_grpc.MDDBStub] = None
        self._writes: set = set()
        self._sleep = time.sleep

        metadata = []
        if api_key:
            metadata.append(("x-api-key", api_key))
        if token:
            metadata.append(("authorization", f"Bearer {token}"))
        self._metadata = tuple(metadata)

        if channel is not None:
            # An injected channel is how a test reaches an in-process server,
            # and how a caller supplies interceptors or its own credentials.
            self._channel = channel
            self._owns_channel = False
        else:
            self._channel = _build_channel(address, **self._channel_args)
            self._owns_channel = True

        self._stub = mddb_pb2_grpc.MDDBStub(self._channel)

    # -- RPC access ---------------------------------------------------------

    def __getattr__(self, name: str) -> Any:
        """Exposes the stub's RPCs as methods carrying the client's metadata.

        Reached only for names the class itself does not define, so nothing
        here can shadow a real method. An unknown name raises AttributeError
        naming the client rather than the generated stub, which is what a
        caller can act on.
        """
        if name.startswith("_"):
            raise AttributeError(name)

        if getattr(self._stub, name, None) is None:
            raise AttributeError(
                f"MDDB has no RPC named {name!r}; "
                f"see mddb.proto for the service definition"
            )

        def call(request: Any, *, timeout: Optional[float] = None, **kwargs: Any) -> Any:
            kwargs["timeout"] = timeout if timeout is not None else self.timeout
            return self._invoke(name, request, kwargs)

        call.__name__ = name
        return call

    def _invoke(self, name: str, request: Any, kwargs: dict) -> Any:
        """Calls one RPC, following the leader when ``follow_leader`` is on.

        A NOT_LEADER refusal is followed at most once per call; a NO_LEADER
        one is retried at most NO_LEADER_RETRIES times. The request is a
        protobuf message, so it can be sent again unchanged.
        """
        stub = self._stub_for(name)
        redirected, waits = False, 0
        while True:
            try:
                return getattr(stub, name)(request, metadata=self._metadata, **kwargs)
            except grpc.RpcError as err:
                retry = self._retry(err, name, stub, redirected, waits)
                if retry is None:
                    raise
                stub, redirected, waits = retry

    def _retry(
        self, err: grpc.RpcError, name: str, stub: mddb_pb2_grpc.MDDBStub, redirected: bool, waits: int
    ) -> Optional[Tuple[mddb_pb2_grpc.MDDBStub, bool, int]]:
        """Decides what follows a failed call of ``_invoke``.

        Returns the stub, ``redirected`` and ``waits`` of the next attempt —
        the leader's stub once for NOT_LEADER, the same stub after a wait for
        NO_LEADER — or ``None`` when the refusal is raised to the caller.
        """
        info = annotate(err)
        if not self.follow_leader:
            return None
        if info is None:
            self._forget_leader(stub, err)
            return None
        if info.reason == NOT_LEADER and not redirected:
            leader_stub = self._follow(name, info)
            return None if leader_stub is None else (leader_stub, True, waits)
        if info.reason == NO_LEADER and waits < NO_LEADER_RETRIES:
            self._sleep(NO_LEADER_WAIT_SECONDS)
            return stub, redirected, waits + 1
        return None

    def _stub_for(self, name: str) -> mddb_pb2_grpc.MDDBStub:
        """The leader's stub for an RPC known to be a write, else the configured one."""
        with self._lock:
            if self.follow_leader and self._leader_stub is not None and name in self._writes:
                return self._leader_stub
            return self._stub

    def _follow(self, name: str, info: LeaderInfo) -> Optional[mddb_pb2_grpc.MDDBStub]:
        """Remembers the leader a refusal named and returns its stub.

        ``None`` when the address is not an http(s) URL or the resolver has no
        target for it; the refusal is then raised to the caller.
        """
        target = self._leader_target(info)
        if not target:
            return None
        with self._lock:
            self._writes.add(name)
            if target != self._leader:
                # The old leader's channel is left to the garbage collector:
                # another thread may still be using it.
                self._leader = target
                self._leader_channel = _build_channel(target, **self._channel_args)
                self._leader_stub = mddb_pb2_grpc.MDDBStub(self._leader_channel)
            return self._leader_stub

    def _leader_target(self, info: LeaderInfo) -> Optional[str]:
        """The gRPC target of the leader a refusal named.

        A caller's ``leader_resolver`` wins; then the server's ``leader_grpc``;
        then the HTTP URL's host with the port of ``address``.
        """
        if self._leader_resolver is not None:
            return self._leader_resolver(info.leader_addr) if info.leader_addr else None
        return info.leader_grpc or grpc_target(info.leader_addr, self.address)

    def _forget_leader(self, stub: mddb_pb2_grpc.MDDBStub, err: grpc.RpcError) -> None:
        """Drops a remembered leader that cannot be reached.

        The next write then asks the configured node again, which names the
        current leader.
        """
        if err.code() != grpc.StatusCode.UNAVAILABLE:
            return
        with self._lock:
            if stub is self._leader_stub:
                self._leader = self._leader_channel = self._leader_stub = None

    @property
    def leader(self) -> Optional[str]:
        """The gRPC target of the leader this client follows, if it learned one."""
        return self._leader

    @property
    def stub(self) -> mddb_pb2_grpc.MDDBStub:
        """The generated stub, for anything this wrapper does not cover."""
        return self._stub

    @property
    def metadata(self) -> tuple:
        """The credential metadata attached to every call."""
        return self._metadata

    # -- lifecycle ----------------------------------------------------------

    def wait_for_ready(self, timeout: float = 5.0) -> None:
        """Blocks until the channel is connected.

        Without this the first RPC is what discovers an unreachable server,
        and it reports the failure as a deadline rather than a refused
        connection.
        """
        grpc.channel_ready_future(self._channel).result(timeout=timeout)

    def close(self) -> None:
        """Closes the channel, unless it was supplied by the caller.

        A channel opened to a followed leader is always the client's own and
        is closed too.
        """
        if self._owns_channel:
            self._channel.close()
        if self._leader_channel is not None:
            self._leader_channel.close()

    def __enter__(self) -> "MddbClient":
        return self

    def __exit__(self, *exc_info: Any) -> None:
        self.close()


def _build_channel(
    address: str,
    *,
    secure: bool,
    root_certificates: Optional[bytes],
    options: list,
) -> grpc.Channel:
    if secure:
        credentials = grpc.ssl_channel_credentials(root_certificates=root_certificates)
        return grpc.secure_channel(address, credentials, options=options)
    return grpc.insecure_channel(address, options=options)
