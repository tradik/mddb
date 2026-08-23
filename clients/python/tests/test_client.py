"""TEST-001. The package exported nothing and had no tests.

``mddb_client/__init__.py`` was empty and the generated stub used an absolute
``import mddb_pb2``, so ``import mddb_client`` raised ModuleNotFoundError — the
published package had never been importable. These tests start a real gRPC
server in-process and check what the client puts on the wire.
"""

from __future__ import annotations

from concurrent import futures

import grpc
import pytest

from mddb_client import (
    DEFAULT_ADDRESS,
    DEFAULT_TIMEOUT_SECONDS,
    MddbClient,
    mddb_pb2,
    mddb_pb2_grpc,
)


class RecordingServicer(mddb_pb2_grpc.MDDBServicer):
    """Records what it was called with, and answers plausibly."""

    def __init__(self) -> None:
        self.calls: list[tuple[str, object, dict]] = []

    def _record(self, method, request, context):
        self.calls.append((method, request, dict(context.invocation_metadata())))

    @property
    def last(self):
        assert self.calls, "the client sent no request"
        return self.calls[-1]

    def Add(self, request, context):
        self._record("Add", request, context)
        return mddb_pb2.Document(
            id=f"{request.collection}|{request.key}|{request.lang}",
            key=request.key,
            lang=request.lang,
            content_md=request.content_md,
        )

    def Get(self, request, context):
        self._record("Get", request, context)
        if request.key == "missing":
            context.abort(grpc.StatusCode.NOT_FOUND, "document not found")
        return mddb_pb2.Document(id="blog|post|en", key="post", content_md="# Hello")

    def Search(self, request, context):
        self._record("Search", request, context)
        return mddb_pb2.SearchResponse(
            documents=[mddb_pb2.Document(key="a"), mddb_pb2.Document(key="b")],
            total=2,
        )

    def Stats(self, request, context):
        self._record("Stats", request, context)
        return mddb_pb2.StatsResponse(total_documents=42)


@pytest.fixture
def server():
    servicer = RecordingServicer()
    srv = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    mddb_pb2_grpc.add_MDDBServicer_to_server(servicer, srv)
    port = srv.add_insecure_port("127.0.0.1:0")
    srv.start()
    try:
        yield f"127.0.0.1:{port}", servicer
    finally:
        srv.stop(grace=None)


@pytest.fixture
def client(server):
    address, _ = server
    with MddbClient(address) as c:
        yield c


# -- the API surface --------------------------------------------------------


def test_the_package_exports_a_client():
    # The regression that matters most: this import used to fail outright.
    assert callable(MddbClient)
    assert DEFAULT_ADDRESS == "localhost:11024"
    assert DEFAULT_TIMEOUT_SECONDS > 0


def test_every_rpc_on_the_stub_is_reachable(client):
    rpcs = [
        name
        for name in dir(client.stub)
        if not name.startswith("_") and callable(getattr(client.stub, name))
    ]
    assert len(rpcs) > 50, f"only {len(rpcs)} RPCs were found on the stub"

    for name in rpcs:
        assert callable(getattr(client, name)), f"{name} is not reachable"


def test_an_unknown_rpc_names_the_service(client):
    with pytest.raises(AttributeError, match="mddb.proto"):
        client.NoSuchRPC


def test_private_names_are_not_treated_as_rpcs(client):
    # __getattr__ must not turn a dunder lookup into an RPC call; that breaks
    # copy, pickle and anything else that probes for optional protocols.
    with pytest.raises(AttributeError):
        client.__deepcopy__


# -- calls ------------------------------------------------------------------


def test_add_sends_the_document(server, client):
    _, servicer = server

    doc = client.Add(
        mddb_pb2.AddRequest(
            collection="blog",
            key="post",
            lang="en",
            content_md="# Hello",
            meta={"tag": mddb_pb2.MetaValues(values=["go", "grpc"])},
        )
    )

    assert doc.id == "blog|post|en"

    method, request, _ = servicer.last
    assert method == "Add"
    assert request.collection == "blog"
    assert request.content_md == "# Hello"
    assert list(request.meta["tag"].values) == ["go", "grpc"]


def test_a_server_error_raises(client):
    with pytest.raises(grpc.RpcError) as excinfo:
        client.Get(mddb_pb2.GetRequest(collection="blog", key="missing", lang="en"))

    assert excinfo.value.code() == grpc.StatusCode.NOT_FOUND
    assert "not found" in excinfo.value.details()


def test_search_returns_what_the_server_sent(server, client):
    _, servicer = server

    response = client.Search(mddb_pb2.SearchRequest(collection="blog", limit=10))

    assert response.total == 2
    assert [d.key for d in response.documents] == ["a", "b"]
    assert servicer.last[1].limit == 10


# -- credentials ------------------------------------------------------------


def test_an_api_key_travels_as_metadata(server):
    address, servicer = server

    with MddbClient(address, api_key="mddb_secret") as c:
        c.Stats(mddb_pb2.StatsRequest())

    metadata = servicer.last[2]
    assert metadata["x-api-key"] == "mddb_secret"
    assert "authorization" not in metadata


def test_a_token_travels_as_a_bearer_header(server):
    address, servicer = server

    with MddbClient(address, token="jwt.value") as c:
        c.Stats(mddb_pb2.StatsRequest())

    assert servicer.last[2]["authorization"] == "Bearer jwt.value"


def test_no_credentials_means_no_credential_metadata(server):
    address, servicer = server

    with MddbClient(address) as c:
        c.Stats(mddb_pb2.StatsRequest())

    metadata = servicer.last[2]
    assert "x-api-key" not in metadata
    assert "authorization" not in metadata


def test_both_credentials_are_sent_when_both_are_given(server):
    address, servicer = server

    with MddbClient(address, api_key="k", token="t") as c:
        c.Stats(mddb_pb2.StatsRequest())

    metadata = servicer.last[2]
    assert metadata["x-api-key"] == "k"
    assert metadata["authorization"] == "Bearer t"


# -- channel lifecycle ------------------------------------------------------


def test_wait_for_ready_against_a_live_server(server):
    address, _ = server
    with MddbClient(address) as c:
        c.wait_for_ready(timeout=5)


def test_wait_for_ready_gives_up_on_a_dead_address():
    # Port 1 is reserved; nothing binds it. grpc raises its own
    # FutureTimeoutError, which is not the concurrent.futures one.
    with MddbClient("127.0.0.1:1") as c:
        with pytest.raises(grpc.FutureTimeoutError):
            c.wait_for_ready(timeout=0.5)


def test_a_call_to_a_dead_address_fails_rather_than_hanging():
    with MddbClient("127.0.0.1:1", timeout=0.5) as c:
        with pytest.raises(grpc.RpcError):
            c.Stats(mddb_pb2.StatsRequest())


def test_a_per_call_timeout_overrides_the_default():
    with MddbClient("127.0.0.1:1", timeout=30) as c:
        with pytest.raises(grpc.RpcError):
            # Without the override this would wait the full 30 seconds.
            c.Stats(mddb_pb2.StatsRequest(), timeout=0.4)


def test_an_injected_channel_is_not_closed_by_the_client(server):
    """A caller who supplied the channel owns it.

    Closing someone else's channel would break the other clients sharing it.
    """
    address, _ = server
    channel = grpc.insecure_channel(address)

    client = MddbClient(address, channel=channel)
    client.Stats(mddb_pb2.StatsRequest())
    client.close()

    # Still usable: the client did not close what it did not open.
    reused = MddbClient(address, channel=channel)
    assert reused.Stats(mddb_pb2.StatsRequest()).total_documents == 42
    channel.close()


def test_the_context_manager_closes_its_own_channel(server):
    address, _ = server

    with MddbClient(address) as c:
        c.Stats(mddb_pb2.StatsRequest())

    # A closed channel refuses further work rather than silently reconnecting.
    with pytest.raises(ValueError):
        c.Stats(mddb_pb2.StatsRequest())


def test_a_secure_channel_can_be_requested():
    """INT-011: insecure is the default, and asking for TLS must work.

    Not connected to anything — building the channel is what is under test,
    because a client that quietly stayed insecure would send the API key in
    cleartext.
    """
    with MddbClient("example.test:443", secure=True) as c:
        assert c.address == "example.test:443"
