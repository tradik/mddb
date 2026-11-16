---
title: "MDDB Cluster: Leader Election and Failover"
slug: "docs/cluster"
description: "Automatic leader election in MDDB 2.19.8: terms, leases and majority votes, safe promotion and demotion, partitions, the rejected-writes file, quorum acknowledgements and how clients follow the leader."
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
