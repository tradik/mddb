---
title: "MCP Protocol Revisions"
slug: "docs/mcp-revisions"
description: "MDDB serves two MCP revisions side by side: the stateless 2026-07-28 and the handshake-based 2025-11-25 - what each requires, and how a request picks its own."
status: publish
---

# MCP Protocol Revisions

MDDB serves two revisions of the Model Context Protocol on the same endpoints:

| Revision | Era | How a client opens | Status in MDDB |
|---|---|---|---|
| **2026-07-28** | stateless | sends the request it wants, with `_meta` | default for new clients |
| **2025-11-25** | handshake | `initialize`, then a session | fully supported |

Neither is deprecated here. Clients and servers do not upgrade on the same day,
and a client pinned to 2025-11-25 keeps working unchanged against a build that
also speaks 2026-07-28.

## How a request picks its revision

There is no negotiation and no connection state. **Each request declares its
own revision**, and MDDB decides per request:

- A request whose `params._meta` carries
  `io.modelcontextprotocol/protocolVersion` — or which names a method only the
  stateless era defines (`server/discover`, `subscriptions/listen`) — is served
  under 2026-07-28.
- Every other request is served under 2025-11-25, exactly as before.

The two can be interleaved on one connection, or on one stdio process. That is
the point of the newer revision: nothing is remembered between requests, so
nothing has to be established before one.

## Serving a stateless client (2026-07-28)

### Every request carries its own context

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "full_text_search",
    "arguments": {"collection": "docs", "query": "replication"},
    "_meta": {
      "io.modelcontextprotocol/protocolVersion": "2026-07-28",
      "io.modelcontextprotocol/clientCapabilities": {},
      "io.modelcontextprotocol/clientInfo": {"name": "my-agent", "version": "1.0.0"},
      "io.modelcontextprotocol/logLevel": "warning"
    }
  }
}
```

| `_meta` key | Required | Meaning |
|---|---|---|
| `io.modelcontextprotocol/protocolVersion` | yes | the revision this request is written in |
| `io.modelcontextprotocol/clientCapabilities` | yes | what the client can do for this request |
| `io.modelcontextprotocol/clientInfo` | no | who is calling; for logs and display only |
| `io.modelcontextprotocol/logLevel` | no | minimum level of `notifications/message` to emit |

A request missing a required field is rejected with `-32602` (and `400` over
HTTP). **`logLevel` is not a default**: a request that omits it receives no log
notifications at all, which is what replaced `logging/setLevel`.

### Every result carries the server's half

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "resultType": "complete",
    "content": [{"type": "text", "text": "…"}],
    "_meta": {
      "io.modelcontextprotocol/serverInfo": {"name": "mddbd", "version": "2.13.0"}
    }
  }
}
```

`resultType` is `"complete"` on every result MDDB returns. The other value the
revision defines, `"input_required"`, belongs to
[multi round-trip requests](https://modelcontextprotocol.io/specification/2026-07-28/basic/patterns/mrtr):
a server that needs sampling, elicitation or a root list from the client
returns one instead of issuing its own request. MDDB never asks the client for
anything — every tool answers out of the database — so it never returns one.

### server/discover

Required of every server in this revision. It is how a client learns what MDDB
is without guessing a revision first, and on stdio it doubles as the probe that
tells a dual-era client which era it reached.

```bash
curl -X POST http://localhost:9000/mcp \
  -H "Content-Type: application/json" \
  -H "MCP-Protocol-Version: 2026-07-28" \
  -H "Mcp-Method: server/discover" \
  -d '{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{
        "io.modelcontextprotocol/protocolVersion":"2026-07-28",
        "io.modelcontextprotocol/clientCapabilities":{}}}}'
```

```json
{
  "resultType": "complete",
  "supportedVersions": ["2026-07-28", "2025-11-25"],
  "capabilities": {
    "tools": {"listChanged": false},
    "resources": {"subscribe": false, "listChanged": false},
    "prompts": {"listChanged": false},
    "logging": {},
    "completions": {}
  },
  "instructions": "…",
  "ttlMs": 3600000,
  "cacheScope": "public",
  "_meta": {"io.modelcontextprotocol/serverInfo": {"name": "mddbd", "version": "2.13.0"}}
}
```

`supportedVersions` honours [the pin](MCP.md#pinning-a-revision): a pinned
server advertises only what it will actually serve, or a client would select a
revision from the list and be refused by its next call.

`listChanged` is `false` throughout, deliberately. MDDB's tool table, prompts
and resource catalogue are built once at startup and never change while the
process runs, so there is no moment at which a list-changed notification could
be sent. Clients rely on `ttlMs` instead.

### Required HTTP headers

Streamable HTTP mirrors three body fields into headers so an intermediary can
route without parsing JSON. MDDB checks that they agree with the body — a proxy
routing on the header while the server acts on the body must not be able to
authorize one call and perform another.

| Header | Mirrors | Required on |
|---|---|---|
| `MCP-Protocol-Version` | `_meta` protocol version | every POST |
| `Mcp-Method` | `method` | every POST |
| `Mcp-Name` | `params.name` or `params.uri` | `tools/call`, `prompts/get`, `resources/read` |

A missing or disagreeing header is `400` with JSON-RPC error `-32020`
(`HeaderMismatch`). A value that cannot be written as plain ASCII travels as
`=?base64?<base64>?=` and is decoded before comparison.

### Caching hints

`server/discover`, `tools/list`, `prompts/list`, `resources/list`,
`resources/templates/list` and `resources/read` carry `ttlMs` and `cacheScope`.

| Result | `ttlMs` | `cacheScope` | Why |
|---|---|---|---|
| the five lists | 3600000 | `public` | built at startup, identical for every caller |
| `resources/read` | 0 | `private` | live database content, scoped to the caller |

### subscriptions/listen

Replaces the GET stream and `resources/subscribe`. MDDB accepts it, replies
with `notifications/subscriptions/acknowledged` on an SSE stream, and closes
the subscription with a result on the same request.

The acknowledged filter is **empty**: nothing MDDB exposes changes while the
process runs, so there is no notification type it can honestly agree to
deliver. A client should rely on `ttlMs` rather than waiting on this stream.

### What the revision removed

| Removed | Use instead |
|---|---|
| `initialize` / `notifications/initialized` | send the request you want, with `_meta` |
| `Mcp-Session-Id`, `DELETE /mcp` | nothing — no state spans requests |
| `GET /mcp` (standalone SSE) | `subscriptions/listen` |
| `ping` | nothing — a request either answers or it does not |
| `logging/setLevel` | `io.modelcontextprotocol/logLevel` per request |
| `resources/subscribe` / `unsubscribe` | `subscriptions/listen` |
| SSE resumability (`Last-Event-ID`) | re-issue the request with a new id |

Sending one of these with stateless `_meta` returns `-32601` with a message
naming the replacement, because a client doing so has an era bug rather than a
typo. Sent without `_meta`, they are served as 2025-11-25 requests as always.

## Error codes

2026-07-28 partitions the JSON-RPC server-error range: `-32000`…`-32019` is
closed to new codes, `-32020`…`-32099` belongs to the specification.

| Code | Name | When |
|---|---|---|
| `-32020` | `HeaderMismatch` | a mirrored header disagrees with the body, or is missing |
| `-32021` | `MissingRequiredClientCapability` | never emitted by MDDB — no request of ours needs the client |
| `-32022` | `UnsupportedProtocolVersion` | the declared revision is not served; `data.supported` names what is |
| `-32602` | `Invalid params` | a required `_meta` field is missing; also "resource not found" |
| `-32002` | *(2025-11-25 only)* | "resource not found" for handshake clients; forbidden in 2026-07-28 |

Over HTTP, `-32601` is answered with `404` **and a JSON-RPC body** — that body
is what distinguishes "this endpoint exists, that method does not" from a `404`
returned by a URL hosting no MCP endpoint at all. The four errors above are
answered with `400`, so a dual-era client must read the body before concluding
a server is legacy.

## Serving a handshake client (2025-11-25)

Unchanged. `initialize` negotiates as it always did, sessions are minted and
echoed, `GET /mcp` opens a stream, `DELETE /mcp` ends a session, `ping` and
`logging/setLevel` work, and results carry no `resultType`, `ttlMs` or
`_meta`.

`initialize` only ever answers with a revision that *has* a handshake. A client
that sends `initialize` asking for 2026-07-28 is answered with 2025-11-25
rather than agreeing, in a handshake, to a protocol with no handshake in it.

If an operator pins the stateless revision
(`MDDB_MCP_PROTOCOL_VERSION=2026-07-28`), `initialize` is refused with `-32022`
naming what the server does serve — a legacy client has no way to fall forward,
so that error may be the only diagnostic its user sees.

## Checking what a server serves

```bash
curl -s localhost:8080/v1/config | jq .protocols.mcp
```

```json
{
  "enabled": true,
  "addr": ":9000",
  "stdio": false,
  "revision": "2026-07-28",
  "supportedRevisions": ["2026-07-28", "2025-11-25"],
  "revisionPinned": false,
  "handshakeRevision": "2025-11-25"
}
```

`handshakeRevision` is what `initialize` is answered with, or absent when the
server serves no handshake revision at all. Since 2026-07-28 there is no single
revision "in force": two clients on the same port can be in different eras, and
an operator debugging one needs to know which.

## See also

- [MCP Server Configuration](MCP.md) — transports, tools, API keys, access modes
- [LLM client setup](LLM_CONNECTIONS.md)
- [MCP 2026-07-28 changelog](https://modelcontextprotocol.io/specification/2026-07-28/changelog)
