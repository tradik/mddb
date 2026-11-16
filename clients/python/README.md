# mddb-client (Python)

gRPC client for [MDDB](https://github.com/tradik/mddb), a markdown database.

## Install

```bash
pip install mddb-client
```

## Use

```python
from mddb_client import MddbClient, mddb_pb2

with MddbClient("localhost:11024") as client:
    doc = client.Add(mddb_pb2.AddRequest(
        collection="blog",
        key="hello",
        lang="en",
        content_md="# Hello\n\nWritten through the Python client.",
        meta={"tag": mddb_pb2.MetaValues(values=["example"])},
    ))
    print(doc.id)

    found = client.Search(mddb_pb2.SearchRequest(collection="blog", limit=10))
    print(f"{found.total} documents")
```

Every unary RPC in [`mddb.proto`](../../proto/mddb.proto) is a method of the
same name — `Add`, `Get`, `Search`, `FTS`, `VectorSearch`, and the rest. They
come from the generated stub rather than being written out, so a new RPC is
usable as soon as the proto carries it. For anything this wrapper does not
cover, `client.stub` is the generated stub itself.

## Authentication

```python
MddbClient("mddb.internal:11024", secure=True, api_key="mddb_…")   # API key
MddbClient("mddb.internal:11024", secure=True, token="eyJ…")       # JWT
```

The API key travels as `x-api-key` metadata, the token as
`authorization: Bearer …`.

> **`secure=False` is the default and disables TLS.** It matches the server's
> own default — a loopback port with no certificate. Anything reachable from
> another machine must pass `secure=True`, or the credential is sent in
> cleartext (INT-011).

## Timeouts

Every call carries a 30-second deadline unless told otherwise. Without one, a
call to a server that accepted the connection and then stopped answering waits
forever.

```python
client = MddbClient("localhost:11024", timeout=5)      # for every call
client.Stats(mddb_pb2.StatsRequest(), timeout=0.5)     # for this one
```

`client.wait_for_ready()` blocks until the channel connects, so an unreachable
server is reported as a refused connection rather than as a deadline on the
first RPC.

## Clusters: following the leader

In a cluster with automatic leader election (mddbd 2.19.8+) every node serves
reads, but only the leader accepts writes. A follower refuses a write with
`FAILED_PRECONDITION` ("not the leader") and a `google.rpc.ErrorInfo` detail
(reason `NOT_LEADER`) naming the leader's HTTP URL; while an election runs it
answers `UNAVAILABLE` with reason `NO_LEADER`.

Off by default (`follow_leader=False`), the client raises the usual
`grpc.RpcError` with `leader_addr`, `leader_grpc`, `leader_id` and `term`
set on it, so the
caller can act itself. `leader_info(err)` returns the same as a `LeaderInfo`
(or `None` for any other error):

```python
from mddb_client import leader_info

try:
    client.Add(req)
except grpc.RpcError as err:
    if getattr(err, "leader_addr", ""):
        print("write belongs to", err.leader_addr)
    raise
```

With `MddbClient(address, follow_leader=True)`:

- a refused write is sent **once** more, to the leader. A second refusal is
  raised; there is no redirect loop. Only an `http`/`https` leader URL is
  followed.
- the leader's gRPC target is, in order: `leader_resolver(leader_addr)` when
  you pass `leader_resolver=lambda url: "host:port"` (returning `None`
  refuses to follow); else the `leader_grpc` address the server sends in the
  ErrorInfo metadata; else the host of the leader's HTTP URL with the port of
  `address`.
- the client remembers the leader (`client.leader`). An RPC that was
  redirected once is a write, so later calls of it go to the leader directly;
  every other RPC, reads included, keeps using `address`. If the remembered
  leader becomes unreachable (`UNAVAILABLE`) it is forgotten and the next
  write asks `address` again.
- a `NO_LEADER` refusal is retried up to three times, one second apart. Each
  attempt gets the call's full timeout.
- the leader's channel uses the same TLS settings and channel options as the
  client's own (with an injected `channel`, set `secure=` to match it), and
  `close()` closes it too.

## Development

```bash
pip install -e ".[test]"
pytest
```

The tests run a gRPC server in-process; no MDDB instance is needed.
