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

from typing import Any, Iterable, Optional

import grpc

from . import mddb_pb2_grpc

# Default gRPC port of a local mddbd.
DEFAULT_ADDRESS = "localhost:11024"

# Per-call deadline. Without one, a call to a server that accepted the
# connection and then stopped answering waits forever.
DEFAULT_TIMEOUT_SECONDS = 30.0


class MddbClient:
    """A thin, typed-by-protobuf wrapper over the generated MDDB stub.

    INT-011: the channel is insecure by default because the server's own
    default is a loopback port with no TLS. Anything reachable from another
    machine must pass ``secure=True``, or the API key travels in cleartext.
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
    ) -> None:
        self.address = address
        self.timeout = timeout

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
            self._channel = _build_channel(
                address,
                secure=secure,
                root_certificates=root_certificates,
                options=list(channel_options or ()),
            )
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

        rpc = getattr(self._stub, name, None)
        if rpc is None:
            raise AttributeError(
                f"MDDB has no RPC named {name!r}; "
                f"see mddb.proto for the service definition"
            )

        def call(request: Any, *, timeout: Optional[float] = None, **kwargs: Any) -> Any:
            return rpc(
                request,
                timeout=timeout if timeout is not None else self.timeout,
                metadata=self._metadata,
                **kwargs,
            )

        call.__name__ = name
        return call

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
        """Closes the channel, unless it was supplied by the caller."""
        if self._owns_channel:
            self._channel.close()

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
