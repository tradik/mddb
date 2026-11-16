# @tradik/mddb-client (Node.js)

gRPC client for [MDDB](https://github.com/tradik/mddb), a markdown database.

## Install

```bash
npm install @tradik/mddb-client
```

## Use

```js
const { MddbClient } = require('@tradik/mddb-client');

const client = new MddbClient('localhost:11024');

const doc = await client.Add({
  collection: 'blog',
  key: 'hello',
  lang: 'en',
  content_md: '# Hello\n\nWritten through the Node.js client.',
  meta: { tag: { values: ['example'] } },
});
console.log(doc.id);

const found = await client.Search({ collection: 'blog', limit: 10 });
console.log(`${found.total} documents`);

client.close();
```

Every unary RPC in [`mddb.proto`](../../proto/mddb.proto) is a
promise-returning method of the same name — `Add`, `Get`, `Search`, `FTS`,
`VectorSearch`, and the rest. They come from the service definition rather than
being written out, so a new RPC is usable as soon as the proto carries it.

Streaming RPCs are deliberately not promisified: turning `Export` into a single
promise would buffer an entire export in memory, which is what streaming exists
to avoid. Reach for `client.stub.Export(...)`.

## Authentication

```js
new MddbClient('mddb.internal:11024', { secure: true, apiKey: 'mddb_…' });
new MddbClient('mddb.internal:11024', { secure: true, token: 'eyJ…' });
```

The API key travels as `x-api-key` metadata, the token as
`authorization: Bearer …`.

> **`secure` defaults to `false` and disables TLS.** It matches the server's own
> default — a loopback port with no certificate. Anything reachable from another
> machine must pass `secure: true`, or the credential is sent in cleartext
> (INT-011).

## Timeouts

Every call carries a 30-second deadline unless told otherwise. Without one, a
call to a server that accepted the connection and then stopped answering waits
forever.

```js
new MddbClient('localhost:11024', { deadlineMs: 5000 });  // for every call
new MddbClient('localhost:11024', { deadlineMs: 0 });     // no deadline
client.Stats({}, { deadline: new Date(Date.now() + 500) }); // for this one
```

`await client.waitForReady()` blocks until the channel connects, so an
unreachable server is reported as a refused connection rather than as a deadline
on the first RPC.

## Clusters: following the leader

In a cluster with automatic leader election (mddbd 2.19.8+) every node serves
reads, but only the leader accepts writes. A follower refuses a write with
`FAILED_PRECONDITION` ("not the leader") and a `google.rpc.ErrorInfo` detail
(reason `NOT_LEADER`) naming the leader's HTTP URL; while an election runs it
answers `UNAVAILABLE` with reason `NO_LEADER`.

Off by default (`followLeader: false`), the call rejects with the usual gRPC
error, with `leaderAddr`, `leaderGrpc`, `leaderId`, `term` and `leaderReason`
set on it, so
the caller can act itself. `leaderInfo(err)` returns the same as an object (or
`null` for any other error):

```js
const { leaderInfo } = require('@tradik/mddb-client');

try {
  await client.Add(req);
} catch (err) {
  if (err.leaderAddr) console.error('write belongs to', err.leaderAddr);
  throw err;
}
```

With `new MddbClient(address, { followLeader: true })`:

- a refused write is sent **once** more, to the leader. A second refusal
  rejects; there is no redirect loop. Only an `http`/`https` leader URL is
  followed.
- the leader's gRPC target is, in order: `leaderResolver(leaderAddr)` when
  you pass `leaderResolver: (url) => 'host:port'` (returning `null` refuses
  to follow); else the `leader_grpc` address the server sends in the
  ErrorInfo metadata; else the host of the leader's HTTP URL with the port of
  `address`.
- the client remembers the leader (`client.leader`). An RPC that was
  redirected once is a write, so later calls of it go to the leader directly;
  every other RPC, reads included, keeps using `address`. If the remembered
  leader becomes unreachable (`UNAVAILABLE`) it is forgotten and the next
  write asks `address` again.
- a `NO_LEADER` refusal is retried up to three times, one second apart. Each
  attempt gets its own `deadlineMs`.
- the leader's channel uses the same TLS settings and channel options as the
  client's own, and `close()` closes it too.

## Development

```bash
npm install
npm test        # node --test, gRPC server runs in-process
npm run example # the demo in example.js, needs a live MDDB
```
