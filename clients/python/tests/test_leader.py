"""GO-051: following the cluster leader.

A fake follower refuses writes the way mddbd does — FAILED_PRECONDITION with
a google.rpc.ErrorInfo detail in ``grpc-status-details-bin`` — and a fake
leader accepts them. The detail is encoded with protobuf's own encoder, from
descriptors built here, so the client's hand-written decoder is checked
against the real wire format.
"""

from __future__ import annotations

from concurrent import futures

import grpc
import pytest
from google.protobuf import any_pb2, descriptor_pb2, descriptor_pool, message_factory

from mddb_client import (
    NO_LEADER,
    NOT_LEADER,
    LeaderInfo,
    MddbClient,
    leader_info,
    mddb_pb2,
    mddb_pb2_grpc,
)
from mddb_client import client as client_module
from mddb_client.leader import _fields, _from_any, _from_details, annotate, grpc_target


# -- google.rpc.Status / ErrorInfo, built from descriptors -----------------


def _rpc_messages():
    pool = descriptor_pool.DescriptorPool()
    pool.AddSerializedFile(any_pb2.DESCRIPTOR.serialized_pb)
    f = descriptor_pb2.FileDescriptorProto(
        name="test_google_rpc.proto",
        package="google.rpc",
        syntax="proto3",
        dependency=["google/protobuf/any.proto"],
    )
    T = descriptor_pb2.FieldDescriptorProto
    status = f.message_type.add(name="Status")
    status.field.add(name="code", number=1, type=T.TYPE_INT32, label=T.LABEL_OPTIONAL)
    status.field.add(name="message", number=2, type=T.TYPE_STRING, label=T.LABEL_OPTIONAL)
    status.field.add(
        name="details", number=3, type=T.TYPE_MESSAGE, label=T.LABEL_REPEATED, type_name=".google.protobuf.Any"
    )
    info = f.message_type.add(name="ErrorInfo")
    info.field.add(name="reason", number=1, type=T.TYPE_STRING, label=T.LABEL_OPTIONAL)
    info.field.add(name="domain", number=2, type=T.TYPE_STRING, label=T.LABEL_OPTIONAL)
    entry = info.nested_type.add(name="MetadataEntry")
    entry.options.map_entry = True
    entry.field.add(name="key", number=1, type=T.TYPE_STRING, label=T.LABEL_OPTIONAL)
    entry.field.add(name="value", number=2, type=T.TYPE_STRING, label=T.LABEL_OPTIONAL)
    info.field.add(
        name="metadata",
        number=3,
        type=T.TYPE_MESSAGE,
        label=T.LABEL_REPEATED,
        type_name=".google.rpc.ErrorInfo.MetadataEntry",
    )
    pool.Add(f)
    return (
        message_factory.GetMessageClass(pool.FindMessageTypeByName("google.rpc.Status")),
        message_factory.GetMessageClass(pool.FindMessageTypeByName("google.rpc.ErrorInfo")),
    )


Status, ErrorInfo = _rpc_messages()


def status_details(code: grpc.StatusCode, message: str, reason: str, **metadata: str) -> bytes:
    detail = any_pb2.Any()
    detail.Pack(ErrorInfo(reason=reason, domain="mddb", metadata=metadata))
    status = Status(code=code.value[0], message=message)
    status.details.add(type_url="type.googleapis.com/google.rpc.RetryInfo", value=b"\x0a\x00")
    status.details.add(type_url=detail.type_url, value=detail.value)
    return status.SerializeToString()


# -- fake cluster nodes ------------------------------------------------------


class Node(mddb_pb2_grpc.MDDBServicer):
    """Answers Add per ``refuse`` (a callable returning None to accept)."""

    def __init__(self, refuse=None) -> None:
        self.refuse = refuse or (lambda hit: None)
        self.adds: list[str] = []
        self.gets = 0

    def Add(self, request, context):
        self.adds.append(request.content_md)
        refusal = self.refuse(len(self.adds))
        if refusal is not None:
            code, message, trailer = refusal
            if trailer is not None:
                context.set_trailing_metadata((("grpc-status-details-bin", trailer),))
            context.abort(code, message)
        return mddb_pb2.Document(key=request.key, content_md=request.content_md)

    def Get(self, request, context):
        self.gets += 1
        return mddb_pb2.Document(key=request.key)


def not_leader(leader_addr: str, **extra: str):
    trailer = status_details(
        grpc.StatusCode.FAILED_PRECONDITION,
        "not the leader",
        NOT_LEADER,
        leader_addr=leader_addr,
        leader_id="n2",
        term="5",
        **extra,
    )
    return lambda hit: (grpc.StatusCode.FAILED_PRECONDITION, "not the leader; leader is n2", trailer)


def no_leader(times: int):
    trailer = status_details(grpc.StatusCode.UNAVAILABLE, "no leader", NO_LEADER, term="7")
    return lambda hit: (grpc.StatusCode.UNAVAILABLE, "no leader: an election is in progress", trailer) if hit <= times else None


@pytest.fixture
def cluster():
    servers = []

    def start(node: Node) -> str:
        srv = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
        mddb_pb2_grpc.add_MDDBServicer_to_server(node, srv)
        port = srv.add_insecure_port("127.0.0.1:0")
        srv.start()
        servers.append(srv)
        return f"127.0.0.1:{port}"

    yield start
    for srv in servers:
        srv.stop(grace=None)


def to(target: str):
    """A resolver sending every leader URL to one gRPC target."""
    return lambda url: target


def add(client: MddbClient, body: str = "# hi"):
    return client.Add(mddb_pb2.AddRequest(collection="blog", key="k", lang="en", content_md=body), timeout=5)


# -- follow_leader off -------------------------------------------------------


def test_off_a_refused_write_carries_the_leader(cluster):
    leader = Node()
    follower = Node(not_leader("http://10.0.0.2:11023"))
    with MddbClient(cluster(follower), leader_resolver=to(cluster(leader))) as c:
        with pytest.raises(grpc.RpcError) as caught:
            add(c)
    err = caught.value
    assert err.code() == grpc.StatusCode.FAILED_PRECONDITION
    assert (err.leader_addr, err.leader_id, err.term) == ("http://10.0.0.2:11023", "n2", 5)
    assert leader_info(err) == LeaderInfo(NOT_LEADER, "http://10.0.0.2:11023", "n2", 5)
    assert leader.adds == [] and c.leader is None


def test_off_no_leader_is_not_retried(cluster):
    node = Node(no_leader(100))
    with MddbClient(cluster(node)) as c:
        with pytest.raises(grpc.RpcError) as caught:
            add(c)
    assert caught.value.term == 7 and caught.value.leader_addr == ""
    assert len(node.adds) == 1


# -- follow_leader on --------------------------------------------------------


def test_on_a_write_follows_the_leader_and_remembers_it(cluster):
    leader = Node()
    follower = Node(not_leader("http://10.0.0.2:11023"))
    leader_target = cluster(leader)
    with MddbClient(cluster(follower), follow_leader=True, leader_resolver=to(leader_target)) as c:
        assert add(c, "first").content_md == "first"
        assert c.leader == leader_target
        assert follower.adds == ["first"] and leader.adds == ["first"]

        add(c, "second")  # straight to the leader
        assert follower.adds == ["first"] and leader.adds == ["first", "second"]

        c.Get(mddb_pb2.GetRequest(collection="blog", key="k"))  # reads stay home
        assert follower.gets == 1 and leader.gets == 0

        # A new leader named by the old one replaces it.
        newer = Node()
        newer_target = cluster(newer)
        leader.refuse = not_leader("http://10.0.0.3:11023")
        c._leader_resolver = to(newer_target)
        add(c, "third")
        assert c.leader == newer_target and newer.adds == ["third"]


def test_on_only_one_redirect_per_call(cluster):
    second = Node(not_leader("http://10.0.0.3:11023"))
    first = Node(not_leader("http://10.0.0.2:11023"))
    with MddbClient(cluster(first), follow_leader=True, leader_resolver=to(cluster(second))) as c:
        with pytest.raises(grpc.RpcError) as caught:
            add(c)
    assert caught.value.leader_addr == "http://10.0.0.3:11023"
    assert len(first.adds) == 1 and len(second.adds) == 1


@pytest.mark.parametrize("addr", ["", "ftp://10.0.0.2", "10.0.0.2:11023"])
def test_on_an_unusable_leader_address_is_not_followed(cluster, addr):
    follower = Node(not_leader(addr))
    with MddbClient(cluster(follower), follow_leader=True) as c:
        with pytest.raises(grpc.RpcError):
            add(c)
    assert len(follower.adds) == 1 and c.leader is None


def test_on_no_leader_is_retried_a_bounded_number_of_times(cluster, monkeypatch):
    node = Node(no_leader(2))
    with MddbClient(cluster(node), follow_leader=True) as c:
        waits = []
        c._sleep = waits.append
        add(c)
        assert waits == [client_module.NO_LEADER_WAIT_SECONDS] * 2 and len(node.adds) == 3

        node.refuse = no_leader(100)
        node.adds.clear()
        with pytest.raises(grpc.RpcError) as caught:
            add(c)
    assert leader_info(caught.value).reason == NO_LEADER
    assert len(node.adds) == client_module.NO_LEADER_RETRIES + 1


def test_on_an_unreachable_leader_is_forgotten(cluster):
    follower = Node(not_leader("http://10.0.0.2:11023"))
    with MddbClient(cluster(follower), follow_leader=True, leader_resolver=to("127.0.0.1:1")) as c:
        with pytest.raises(grpc.RpcError) as caught:
            add(c)
        assert caught.value.code() == grpc.StatusCode.UNAVAILABLE
        assert c.leader is None
        # Any other error of the leader keeps it.
        c._follow("Add", LeaderInfo(NOT_LEADER, "http://x"))
        c._forget_leader(c._leader_stub, _FakeCall(grpc.StatusCode.INTERNAL, ""))
        assert c.leader == "127.0.0.1:1"
        # A failure of the configured node does not forget the leader.
        c._forget_leader(c.stub, _FakeCall(grpc.StatusCode.UNAVAILABLE, ""))
        assert c.leader == "127.0.0.1:1"


def test_on_an_ordinary_error_is_raised_unchanged(cluster):
    node = Node(lambda hit: (grpc.StatusCode.INVALID_ARGUMENT, "bad", None))
    with MddbClient(cluster(node), follow_leader=True) as c:
        with pytest.raises(grpc.RpcError) as caught:
            add(c)
    assert caught.value.code() == grpc.StatusCode.INVALID_ARGUMENT
    assert not hasattr(caught.value, "leader_addr")


def test_close_closes_the_leader_channel(cluster):
    leader = Node()
    follower = Node(not_leader("http://10.0.0.2:11023"))
    c = MddbClient(cluster(follower), follow_leader=True, leader_resolver=to(cluster(leader)))
    add(c)
    c.close()
    with pytest.raises(ValueError):
        c._leader_stub.Add(mddb_pb2.AddRequest())


def test_the_servers_grpc_address_is_preferred(cluster):
    leader = Node()
    leader_target = cluster(leader)
    follower = Node(not_leader("http://10.0.0.2:11023", leader_grpc=leader_target))
    with MddbClient(cluster(follower), follow_leader=True) as c:
        add(c)
        assert c.leader == leader_target and leader.adds == ["# hi"]


def test_a_resolver_wins_over_the_servers_grpc_address(cluster):
    leader = Node()
    follower = Node(not_leader("http://10.0.0.2:11023", leader_grpc="127.0.0.1:1"))
    with MddbClient(cluster(follower), follow_leader=True, leader_resolver=to(cluster(leader))) as c:
        add(c)
        assert leader.adds == ["# hi"]


def test_without_a_grpc_address_the_configured_port_is_used():
    c = MddbClient("node1:12000", follow_leader=True)
    assert c._leader_target(LeaderInfo(NOT_LEADER, "http://10.0.0.2:11023")) == "10.0.0.2:12000"
    assert c._leader_target(LeaderInfo(NOT_LEADER, "http://x", leader_grpc="x:9")) == "x:9"
    assert c._leader_target(LeaderInfo(NOT_LEADER)) is None
    assert c.metadata == ()
    c.close()


# -- the decoder -------------------------------------------------------------


class _FakeCall(grpc.RpcError, grpc.Call):
    """A minimal raised-call error for the decoder's edge cases."""

    def __init__(self, code, details, trailers=None):
        self._code, self._details, self._trailers = code, details, trailers

    def code(self):
        return self._code

    def details(self):
        return self._details

    def trailing_metadata(self):
        return self._trailers

    def initial_metadata(self):  # pragma: no cover - grpc.Call interface
        return None

    def is_active(self):  # pragma: no cover - grpc.Call interface
        return False

    def time_remaining(self):  # pragma: no cover - grpc.Call interface
        return None

    def cancel(self):  # pragma: no cover - grpc.Call interface
        return False

    def add_callback(self, callback):  # pragma: no cover - grpc.Call interface
        return False


def test_leader_info_without_a_detail_falls_back_to_the_message():
    err = _FakeCall(grpc.StatusCode.FAILED_PRECONDITION, "not the leader")
    assert leader_info(err) == LeaderInfo(NOT_LEADER)
    assert annotate(err) == LeaderInfo(NOT_LEADER) and err.leader_addr == ""
    assert leader_info(_FakeCall(grpc.StatusCode.FAILED_PRECONDITION, None)) is None
    assert leader_info(_FakeCall(grpc.StatusCode.UNAVAILABLE, "not the leader")) is None
    assert leader_info(ValueError("x")) is None
    assert leader_info(grpc.RpcError()) is None


def test_leader_info_ignores_other_trailers():
    trailers = (("other-bin", b"x"), ("grpc-status-details-bin", "text"))
    assert leader_info(_FakeCall(grpc.StatusCode.ABORTED, "x", trailers)) is None


def test_the_decoder_rejects_what_it_cannot_read():
    assert _from_details(b"") is None
    assert _from_details(b"\x1a\x05ab") is None  # truncated length
    assert _from_details(b"\x80") is None  # truncated varint
    assert _from_details(b"\x0b") is None  # group wire type
    assert _from_details(b"\x08\x05\x1a\x00") is None  # an empty Any
    # Fixed-width fields are skipped.
    assert [n for n, _, _ in _fields(b"\x09" + b"\x00" * 8 + b"\x15" + b"\x00" * 4)] == [1, 2]


def test_the_decoder_requires_a_known_reason():
    other = any_pb2.Any()
    other.Pack(ErrorInfo(reason="QUOTA", metadata={"leader_addr": "http://x"}))
    assert _from_any(other.SerializeToString()) is None

    odd = any_pb2.Any()
    odd.Pack(ErrorInfo(reason=NO_LEADER, metadata={"term": "soon"}))
    # A varint field inside ErrorInfo is skipped.
    data = any_pb2.Any(type_url=odd.type_url, value=odd.value + b"\x20\x01").SerializeToString()
    assert _from_any(data) == LeaderInfo(NO_LEADER, term=None)


def test_grpc_target():
    assert grpc_target("https://leader.internal:11023/", "node1:11024") == "leader.internal:11024"
    assert grpc_target("http://[::1]:11023", "dns:///node1:9000") == "[::1]:9000"
    assert grpc_target("http://10.0.0.2", "node1") == "10.0.0.2:11024"
    assert grpc_target("http://[::1", "node1:1") is None
    assert grpc_target("http://", "node1:1") is None
