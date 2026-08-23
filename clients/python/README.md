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

## Development

```bash
pip install -e ".[test]"
pytest
```

The tests run a gRPC server in-process; no MDDB instance is needed.
