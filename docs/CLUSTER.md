---
title: "MDDB Cluster: Leader Election, Failover and Collection Placement"
slug: "docs/cluster"
description: "Automatic leader election in MDDB 2.19.8: terms, leases and majority votes, safe promotion and demotion, partitions, the rejected-writes file, quorum acknowledgements, how clients follow the leader, and whole collections placed across replicated node groups and moved under load."
status: publish
---

# MDDB Cluster: Leader Election and Failover

From 2.19.8 a group of MDDB nodes can elect their leader. Set
`MDDB_CLUSTER_PEERS` on every node and the nodes choose one leader, replace it
when it fails, and refuse writes on any node that is not it. Without
`MDDB_CLUSTER_PEERS` nothing here applies: `MDDB_REPLICATION_ROLE` works as
before ([REPLICATION.md](REPLICATION.md)).

Replication itself is unchanged: the leader streams its binlog to every other
node over gRPC. What the cluster adds is who the leader is.

Several clusters can share the collections between them, each holding whole
collections of its own: see [Placing collections across groups](#placing-collections-across-groups).

## Contents

- [How a leader is chosen](#how-a-leader-is-chosen)
- [How many nodes](#how-many-nodes)
- [Configuration](#configuration)
- [Acknowledgements](#acknowledgements)
- [Writes on a node that does not lead](#writes-on-a-node-that-does-not-lead)
- [Promotion and demotion](#promotion-and-demotion)
- [Partitions](#partitions)
- [Rejected writes](#rejected-writes)
- [Status](#status)
- [Upgrading](#upgrading)
- [Placing collections across groups](#placing-collections-across-groups)
- [Tests](#tests)

## How a leader is chosen

- **Terms.** Time is divided into numbered terms. Each node keeps its current
  term, and the vote it gave in it, in `mddb.cluster.json` next to the binlog,
  synced before it votes. Every binlog entry belongs to the term of the
  leader that wrote it; the log records where each term begins (the `terms`
  list of the binlog sidecar).
- **Election.** A node that hears nothing from a leader for the election
  timeout (drawn between `MDDB_CLUSTER_ELECTION_TIMEOUT` and twice it) stands
  in the next term and asks the others for a vote. A node gives one vote per
  term, and only to a candidate whose log is at least as far as its own:
  later last term, or the same last term and a last LSN no lower. A majority
  of votes, the candidate's own included, makes it leader. Two leaders in one
  term would need two majorities, which do not exist.
- **Lease.** The leader sends a heartbeat every `MDDB_CLUSTER_HEARTBEAT`. A
  heartbeat a majority answered extends its lease to three quarters of the
  election timeout after it was sent, and the leader takes writes only inside
  its lease. A node that heard from a leader within the election timeout gives
  no vote, so by the time anyone may be elected, the old leader's lease has
  run out — it has stopped taking writes even if it cannot hear the others.
  The lease does not depend on clocks agreeing, only on them running at about
  the same rate.
- **Taking over.** A new leader stops replicating, marks its log with the new
  term and opens writes. The others follow it from their own positions; they
  share its log (same log ID, same LSNs), so nobody resynchronises after an
  ordinary failover.

### Timings

| Setting | Default | Effect |
|---------|---------|--------|
| `MDDB_CLUSTER_HEARTBEAT` | `500ms` | How often the leader reaches every node |
| `MDDB_CLUSTER_ELECTION_TIMEOUT` | `3s` | Silence after which a node stands; must be at least twice the heartbeat |

With the defaults a dead leader is replaced within 3–6 s of its last
heartbeat plus one election round (measured 2.6–3.7 s after `kill -9`, see
[Tests](#tests)). The bound is twice the election timeout plus
a round trip; lower both settings for faster failover on a fast network.

## How many nodes

| Nodes | Majority | Survives | Automatic failover |
|-------|----------|----------|--------------------|
| 1 | 1 | — | no |
| 2 | 2 | — | **no**: neither node alone is a majority |
| 3 | 2 | 1 node | yes |
| 5 | 3 | 2 nodes | yes |

With two nodes `MDDB_CLUSTER_AUTO_FAILOVER` is switched off (the start-up log
says so). A leader is then made by promotion, and when the other node is down
only a **forced** promotion makes one ([Promotion](#promotion-and-demotion)).
Run three nodes — or two data nodes and a third, small node that only votes
and keeps a copy — for automatic failover.

## Configuration

Every node lists every node, itself included, by `MDDB_NODE_ID` and gRPC
address:

```bash
MDDB_NODE_ID=n1
MDDB_CLUSTER_PEERS=n1=10.0.0.1:11024,n2=10.0.0.2:11024,n3=10.0.0.3:11024
MDDB_CLUSTER_ADVERTISE_HTTP=http://10.0.0.1:11023   # where clients send writes
MDDB_REPLICATION_SECRET=change-me-to-32-random-characters
```

| Variable | Default | Description |
|----------|---------|-------------|
| `MDDB_CLUSTER_PEERS` | — | Turns cluster mode on: `id=host:port`, comma separated, every node once |
| `MDDB_CLUSTER_ADVERTISE_HTTP` | — | This node's HTTP base URL, given to clients that send it a write while it does not lead |
| `MDDB_CLUSTER_AUTO_FAILOVER` | `true` | Nodes stand on their own; forced off below three nodes |
| `MDDB_CLUSTER_BOOTSTRAP` | `false` | Lead a cluster that has never had a term at once. `MDDB_REPLICATION_ROLE=leader` means the same in cluster mode |
| `MDDB_CLUSTER_HEARTBEAT` / `MDDB_CLUSTER_ELECTION_TIMEOUT` | `500ms` / `3s` | [Timings](#timings) |
| `MDDB_REPLICATION_ACK` | `async` | `quorum`: a write returns once a majority holds it ([Acknowledgements](#acknowledgements)). Needs `MDDB_CLUSTER_PEERS` |
| `MDDB_REPLICATION_ACK_TIMEOUT` | `5s` | How long a quorum write waits |
| `MDDB_CLUSTER_REJECTED_DIR` | next to the binlog | Where [rejected writes](#rejected-writes) are saved |

Elections and replication authenticate as replication does: the shared
secret, mutual TLS (`MDDB_REPLICATION_TLS_*` on every node, a client CA on
every listener) or admin credentials ([Securing replication](REPLICATION.md#securing-replication)).
gRPC must be enabled. Every node needs the same `MDDB_STORAGE_LAYOUT`.

## Acknowledgements

| `MDDB_REPLICATION_ACK` | A write returns | A failover can lose |
|------------------------|-----------------|---------------------|
| `async` (default) | once the leader committed it | the writes the dead leader had not sent yet (they go to its rejected-writes file when it returns) |
| `quorum` | once a majority of nodes, the leader included, hold it | no acknowledged write: the next leader is elected by a majority, and every majority holds it |

A follower acknowledges each batch it applies. A quorum write that is not
confirmed in `MDDB_REPLICATION_ACK_TIMEOUT` fails with "the write is not yet
held by a majority of the cluster"; it is committed on the leader and may
survive or be cut by a later failover — that is what an unconfirmed write
means. Quorum costs one round trip to the nearest majority per write.

## Writes on a node that does not lead

| Surface | Answer |
|---------|--------|
| REST | `421 Misdirected Request`, header `X-MDDB-Leader: <leader URL>`, body `{"error":"not the leader","leaderAddr":"…","leaderId":"n2","term":5}`. While an election runs: `503` with `Retry-After: 1` |
| gRPC | `FAILED_PRECONDITION` with a `google.rpc.ErrorInfo` detail, reason `NOT_LEADER`, metadata `leader_addr`, `leader_grpc`, `leader_id`, `term`; during an election `UNAVAILABLE`, reason `NO_LEADER` |
| MCP | the tool fails with "not the leader: send writes to <URL> (leader n2, term 5)" |

The Go, Python and Node.js clients follow the leader when asked
(`WithFollowLeader(true)`, `follow_leader=True`, `followLeader: true`): one
retry against the leader, which they then remember for writes. Reads are
served by every node.

Inside the server the binlog is the last line: on a node that does not lead
it refuses every write's entries inside the write's transaction, so a write
that slipped past the guards rolls back instead of diverging. Since 2.19.8
that holds for every write path — documents, vectors, the metadata index
queue, tenant configurations, aliases, module switches, chat scenarios,
skills and MCP secrets, and the extreme-mode batch path — none appends after
its commit any more.

## Promotion and demotion

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" http://n2:11023/v1/replication/promote
curl -X POST -H "Authorization: Bearer $TOKEN" http://n1:11023/v1/replication/demote
mddb-cli replication promote        # on the node to promote
mddb-cli replication demote         # on the leader
```

`promote` (admin) asks every reachable node where its log ends and refuses
(`409`) when one is further. If the leader is reachable it is asked to step
down: it stops taking writes at once, tells the others they may vote, and
reports its last entry; the promoted node waits until it holds that entry and
then stands. `demote` (admin) makes the leader step down the same way; with
automatic failover another node is elected within the election timeout.

`{"force": true}` takes the leadership without votes. It is only allowed with
automatic failover off, for the case an operator has checked: the other nodes
are down. A forced leader beside a live one makes two leaders until they hear
of each other; the older one then steps down and its later writes are cut.

## Partitions

The side with a majority elects a leader; the side without one takes no
writes: its leader's lease runs out after three quarters of the election
timeout and it steps down. Writes the old leader took inside its last lease
that never reached the others are cut when the partition heals.

## Rejected writes

A node that comes back after another was elected compares its log with the
leader's term by term. If they part — it took writes as leader that no other
node received — it saves every entry after the last shared one to
`rejected-writes-<node>-term<term>-<UTC time>.jsonl`, one JSON object per line:

```json
{"lsn":1042,"term":4,"type":"Put","bucket":"docs","key":"posts|…","keyBase64":"…","valueBase64":"…","time":"2026-11-16T10:15:00Z"}
```

Values are as stored (compressed, encrypted when the collection is). Then it
replaces its data with a snapshot of the leader's. The last file is shown in
`/v1/replication/status` (`cluster.last_rejected_file`) and in the panel. An
operator reviews it and writes again what should be kept.

A node whose log was not closed cleanly (kill -9, power loss) cannot vouch
that its log and its database hold the same writes. As a follower it takes a
snapshot when it rejoins; elected leader, it moves its log ahead so every
follower takes a snapshot of its data. With `MDDB_BINLOG_SYNC=interval` the
last ~100 ms of writes of a crashed node may be in neither its log nor the
rejected-writes file; `commit` narrows that to writes not yet acknowledged.

## Status

`GET /v1/replication/status` adds `term` and, in a cluster, `cluster`: the
node's `state` (`leader`, `follower`, `candidate`), `leader_id`,
`leader_addr`, `lease_valid`, `writable`, `auto_failover`, `ack_mode`,
`majority`, `last_rejected_file` and `nodes` — each with its state, term,
last LSN and term, reachability and when it was last heard from (the leader
hears from everyone; a follower mostly from the leader). The panel's Cluster
page shows the same and offers promotion and step-down to admins.

## Upgrading

- A node without `MDDB_CLUSTER_PEERS` behaves as in 2.19.7–2.19.8 without
  elections; old binlogs open unchanged (the file layout is the same; the
  sidecar gains an optional `terms` list that older readers ignore).
- To move a leader/follower pair into a cluster: stop the followers, then the
  leader, cleanly; add `MDDB_CLUSTER_PEERS`, `MDDB_NODE_ID` and
  `MDDB_CLUSTER_ADVERTISE_HTTP` to every node (keep `MDDB_REPLICATION_ROLE=leader`
  on the old leader for the first start — it bootstraps the first term); start
  them. Followers continue from their positions.
- Every cluster node must run 2.19.8 or later: an older node knows no votes.
  The replication protocol is 3; a 2.19.8 node still streams to and from
  older followers and leaders outside a cluster.

## Placing collections across groups

From 2.19.8 the nodes can form several groups, each a cluster of its own (a
leader and followers, as above), with every collection placed in one of
them. This is how MDDB grows past what one node holds: by whole collection,
across replicated groups. Without `MDDB_PLACEMENT_GROUPS` nothing in this
section applies; a single node or a single cluster behaves as before.

What is placed is a whole collection: its file and its settings (collection
configuration, schemas, synonyms, stop words, spelling dictionaries, curation
rules). A collection is never split across groups or nodes, and splitting
one is not planned. Within a group every node holds all of the group's
collections, through replication.

### Setting up groups

Every node gets the same `MDDB_PLACEMENT_GROUPS`: the meta group's name and
every group's nodes, each with its `MDDB_NODE_ID`, gRPC address and REST URL.
The value is JSON, or `@/path/to/file.json`:

```json
{
  "meta": "g1",
  "groups": {
    "g1": [{"id": "n1", "grpc": "10.0.0.1:11024", "http": "http://10.0.0.1:11023"},
           {"id": "n2", "grpc": "10.0.0.2:11024", "http": "http://10.0.0.2:11023"},
           {"id": "n3", "grpc": "10.0.0.3:11024", "http": "http://10.0.0.3:11023"}],
    "g2": [{"id": "n4", "grpc": "10.0.1.1:11024", "http": "http://10.0.1.1:11023"},
           {"id": "n5", "grpc": "10.0.1.2:11024", "http": "http://10.0.1.2:11023"},
           {"id": "n6", "grpc": "10.0.1.3:11024", "http": "http://10.0.1.3:11023"}]
  }
}
```

```bash
# on n4
MDDB_NODE_ID=n4
MDDB_CLUSTER_PEERS=n4=10.0.1.1:11024,n5=10.0.1.2:11024,n6=10.0.1.3:11024
MDDB_PLACEMENT_GROUPS=@/etc/mddb/placement.json
MDDB_STORAGE_LAYOUT=per-collection
MDDB_REPLICATION_SECRET=change-me-to-32-random-characters
```

A node refuses to start when:

- its `MDDB_CLUSTER_PEERS` is not exactly its group's nodes (same ids, same
  gRPC addresses) — the group is the cluster;
- `MDDB_STORAGE_LAYOUT` is not `per-collection`: a collection is placed,
  copied and removed as its own file (`mddbd -split-collections` converts a
  database, [STORAGE-LAYOUT.md](STORAGE-LAYOUT.md));
- `MDDB_CLUSTER_PEERS` is not set, the node is in no group or in two, the
  meta group is not among the groups, or a node lacks an id, a gRPC address
  or an `http(s)` URL.

Everything a cluster needs still applies: `MDDB_NODE_ID`, gRPC enabled, and
the replication secret or mutual TLS ([Configuration](#configuration)).

| Variable | Default | Effect |
|----------|---------|--------|
| `MDDB_PLACEMENT_GROUPS` | — | The groups; turns placement on |
| `MDDB_PLACEMENT_ROUTE` | `forward` | `forward`: a node sends a request for another group's collection to that group and returns the answer. `redirect`: it answers `421` with the address |
| `MDDB_PLACEMENT_REFRESH` | `1s` | How often a node fetches the map and runs its group's duties |
| `MDDB_PLACEMENT_DROP_GRACE` | `30s` | How long the old group keeps a moved collection before dropping it |
| `MDDB_PLACEMENT_FENCE_TIMEOUT` | `10s` | How long writes to a collection being switched over are held, and how long the fence waits for running writes |

### The meta group

The leader of the meta group keeps two things:

- **The placement map**: which group holds each collection, and which
  collections are moving. It is versioned (ordered by the meta group's term,
  then its version), stored in the leader's main file under the bucket
  `placement` and replicated to the meta group's followers. Every node
  fetches it from the meta leader each refresh round.
- **The global settings**: users, groups, permissions, API keys, MCP keys,
  tenants, module switches (features), aliases, webhooks, automations,
  embedding configurations, chat scenarios, chat skills and chat MCP
  secrets. A write to any of them is routed to the meta leader. The leader
  of every other group copies the whole set from the meta leader every 5
  refresh rounds (about 5 s by default), applies what differs through its
  own log, so its followers have it too, and removes keys the meta group no
  longer has.

Run the meta group with `MDDB_REPLICATION_ACK=quorum`: with `async`
acknowledgements a map change the meta leader had not sent before a failover
is lost with it ([Acknowledgements](#acknowledgements)).

### Where a new collection goes

A collection the map does not name yet belongs to the group a consistent
hash of its name gives (a ring of 150 points per group). The leader of each
group records ("pins") in the map every collection it holds that the map
does not name, so adding a group later moves no existing collection: only
new names can hash to the new group.

To turn placement on for existing data, start the meta group first. Each
group's leader then pins the collections it holds; `GET /v1/placement` shows
them.

### Routing

Every node routes, so a client may send a request to any node.

**REST.** After authentication and the access check, on the node the
request reached:

- A request naming collections goes to the group holding them. A read is
  served by any node of that group (the receiving node, when it is in it; a
  request for another group goes to that group's leader). A write is served
  by the group's leader; a follower sends a write to its own leader. A
  leader that has briefly lost its lease (a heartbeat answered late under
  load) holds the write until it has the lease again, at most
  `MDDB_PLACEMENT_FENCE_TIMEOUT`, rather than refusing it.
- A write to a global setting, and everything under `/v1/placement`, goes to
  the meta group's leader.
- A node forwards only to the addresses in `MDDB_PLACEMENT_GROUPS`; the
  request never chooses where it is sent. The header `X-MDDB-Placement-Hops`
  limits a request to two hops; after that the answer is `503` with
  `Retry-After`.
- With `MDDB_PLACEMENT_ROUTE=redirect` the answer is `421 Misdirected
  Request` with `X-MDDB-Leader` (the group leader's REST URL) and
  `X-MDDB-Group`, body
  `{"error":"…","leaderAddr":"…","leaderId":"n4","group":"g2"}`. The
  clients' follow-leader option ([above](#writes-on-a-node-that-does-not-lead))
  follows it.
- A request naming collections of two groups gets `409` (cross-search
  excepted, below). Before a node has received the map, requests naming
  collections get `503` with `Retry-After: 1`.

**gRPC.** A unary call for another group's collection is made again on that
group's leader, with the caller's metadata. In redirect mode, and for
streaming calls, the answer is `FAILED_PRECONDITION` with a
`google.rpc.ErrorInfo` of reason `COLLECTION_ELSEWHERE` and metadata
`group`, `leader_id`, `leader_addr` and `leader_grpc`. A call naming
collections of several groups gets `FAILED_PRECONDITION`.

**Not routed.** GraphQL, the Elasticsearch-compatible API (`/es`), MCP and
multipart uploads (`/v1/upload`, `/v1/import-wiki`) are answered by the node
they reach. Send them to a node of the group holding the collection
(`GET /v1/placement` shows it). A write through them to another group's
collection is refused by the node's log with an error naming the group and
its leader's address, so no stray copy is created; a read there sees nothing
of other groups' collections.

### Requests over several groups

- **`/v1/cross-search`** over collections of several groups (or with
  `role:` targets) asks each group's leader for its part — hits before
  reranking, grouping and the final cut — and finishes the merged list on
  the receiving node. The results and their order are those of one node
  holding every target (tested with full-text, vector and `groupBy`
  searches). `limitCapped` is the strictest cap any group reported; the
  context budget, passages and model reranking are applied once, to the
  merged list. Not across groups: feedback examples (positive and negative
  documents, `422`) and a `sourceDocID` in a collection another group holds
  (`422`; send `queryVector`). With passages and `includeImages`, images of
  hits from other groups are not attached.
- **`/v1/stats`** asks every group and merges the answers: each group's
  collections (only those the map places in it), totals over all of them,
  and a `groups` list with each group's `name`, `totalDocuments` and, when
  it did not answer, `error`.
- Other listings over all collections — `/v1/vector-stats`,
  `/v1/collection-configs`, `/v1/geo-stats`, `/v1/storage`,
  `/v1/schema/list`, `/v1/corpus` — answer for the receiving node's group
  only.
- `/v1/checksum?collection=…` is routed to the group holding the
  collection; use it to check a move.

### Moving a collection

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"collection":"articles","group":"g2"}' "http://n1:11023/v1/placement/move?wait=true"
curl -H "Authorization: Bearer $TOKEN" http://n1:11023/v1/placement
mddb-cli placement                       # groups, collections, moves
mddb-cli placement move articles g2 --wait --timeout 1h
```

`POST /v1/placement/move` (admin) answers `202` with `{"move":{…}}`; with
`?wait=true` it answers when the move ends: `200` when it is done, `409`
with `{"error","move"}` when it failed. `400`: a missing field, an unknown
group, or placement not configured. `409`: the collection is already in
that group, or a move of it is running. `GET /v1/placement` (admin) shows
the groups and their leaders, every collection the map names (with
`movingTo` while it moves) and the recent moves with their phase (`copying`,
`catching-up`, `fenced`, `done`, `failed`), keys copied, source and applied
LSN, error and duration. Without placement it answers `{"enabled":false}`.
In the panel the Cluster page shows the same and has a Move action.

The meta leader runs a move:

1. The map marks the collection as moving. It stays where it is and keeps
   serving reads and writes.
2. The target group's leader imports it: it notes the source log's
   position, copies every key in short read transactions, then applies every
   source log entry that changes the collection, through its own log (its
   followers replicate it).
3. When the target is close behind (a catch-up round under 250 ms, at most
   three rounds), the source's leader fences the collection: routed writes
   to it wait (up to `MDDB_PLACEMENT_FENCE_TIMEOUT`), running ones finish,
   and then the source's log refuses every write to it. The source reports
   the LSN through which its writes are logged.
4. Once the target has applied that LSN, the map names the target. The
   target's leader learns it first — the meta leader adopts the new map
   only once the target has it (or after `MDDB_PLACEMENT_FENCE_TIMEOUT` if
   the target cannot be reached) — then the source's; the held writes go
   to the target.
5. The source group drops its copy after `MDDB_PLACEMENT_DROP_GRACE`,
   through its log so its followers drop it too, and removes the file.
   Document bodies kept in an external storage backend are not deleted.

No acknowledged write is lost: every write the source took is in the copy,
and it takes none after the fence. A write held longer than
`MDDB_PLACEMENT_FENCE_TIMEOUT` (a slow switch, on a loaded machine) is
refused with `503` and `{"error":"the collection is being moved to another
node group; retry shortly"}` (gRPC `UNAVAILABLE`); it was not written, and a
client should retry it. Reads are served throughout, by the
source until the switch and by the target after it. A failure before the
switch gives the move up: the map clears the mark, the fence lifts, the
collection stays where it was, and the target drops what it copied (after
the grace). A change of meta leader gives up any running move; the new meta
leader clears it.

Measured: an in-process move of a collection with 200 documents, while three
writers and one reader sent requests through every node, took 50–110 ms,
with no failed request and no lost acknowledged write.

Measured at the benchmark profile's corpus size (TEST-005 small: 20,000
documents, each with a 128-number vector — about 382,000 keys), from a group
of one node to a group of two, in-process on one desktop machine: the move
took 8.2–9.0 s in six runs, while three writers and one reader sent requests
through every node (about 13,000 writes and 22,000 reads per run), with no
failed request and no lost acknowledged write. Run it with
`MDDB_PLACEMENT_MEASURE=20000 go test -run TestPlacementMeasureMove -v .` in
`services/mddbd`.

### Limits

- A collection is placed whole and must fit on one node. Splitting one
  collection across groups is not supported and not planned.
- No automatic rebalancing: a move is an explicit admin action.
- Groups are fixed by configuration. To add a group, restart the nodes with
  the new `MDDB_PLACEMENT_GROUPS`; existing collections stay where they are
  pinned.
- Background work queued on the source for a collection being moved
  (embedding jobs not yet run, TTL expiries) is not carried over.
- A write made by a code path that appends to the log after its commit is
  not guaranteed through a switch (these paths are being converted).
- GraphQL, `/es`, MCP and multipart uploads are not routed
  ([Routing](#routing)).

## Tests

`cluster_e2e_test.go` runs three in-process nodes on loopback gRPC with a
150 ms election timeout: one leader, failover after a node is killed without
closing its log, a partitioned leader that stops taking writes, its cut write
in the rejected-writes file and its data equal to the new leader's by
`/v1/checksum`, quorum writes that survive the leader's death, and promotion,
demotion and forced promotion of two nodes. `cluster_process_test.go`
(`go test -tags clusterprocess`) does the same with three `mddbd` processes
and `kill -9`, with the default timings: on a desktop machine a new leader
took writes 2.6–3.7 s after the kill (four runs), and the old leader, started
again on its files, held the new leader's data by `/v1/checksum`. CI runs it
(`.github/workflows/cluster-process.yml`) only on pull requests that touch
`replication_*`, `cluster_*`, `internal/cluster/` or `internal/binlog/`.

`placement_e2e_test.go`, `placement_move_e2e_test.go` and
`placement_fanout_e2e_test.go` run groups of in-process nodes: every request
routed to the group holding its collection, a move under write load with no
failed request and no lost acknowledged write, cross-search over two groups
equal to one node holding every target, `/v1/stats` counting each group
once, global settings written on the meta group and copied to the others,
gRPC calls routed, and redirect mode naming the group's leader.
