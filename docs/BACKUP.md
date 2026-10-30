---
title: "MDDB Backup, Restore and Repair"
slug: "docs/backup"
description: "Consistent, integrity-checked MDDB backups, safe restores, and how to verify and repair a damaged bbolt database file with mddbd -verify-db and -repair-db."
status: publish
---

# Backup, restore and repair

How MDDB copies its database, how it decides a copy is safe to restore, and
what to do when the database file itself is damaged. Everything here applies
from **v2.15.4**.

## Taking a backup

```bash
curl "http://localhost:11023/v1/backup?to=nightly.db"     # HTTP
mddb-cli backup nightly.db                                # CLI (gRPC)
```

The MCP `backup` tool does the same. All three:

1. copy the database **through a read transaction**. The copy holds exactly
   one committed state, however many writes land while it is being written.
   Before 2.15.4 the HTTP and MCP backups copied the file itself. A copy taken
   during writes held pages from different commits, and bbolt refused it at
   restore with `freepages: ... needs to be < than key of the next element`
   ([#266](https://github.com/tradik/mddb/issues/266)).
2. **check the copy** with bbolt's full integrity check, the same check a
   restore runs, before keeping it.
3. move it into place under the requested name only when both steps
   succeeded. A failed backup leaves no file behind.

A consistent copy of a healthy database always passes the check. **If a
backup fails the check, the live database is damaged.** The response says so
while the server is still serving. A damaged database usually keeps serving
until it is next opened, so do not restart it: see
[Repairing a damaged database](#repairing-a-damaged-database).

A large database takes a while to copy and check, so the HTTP backup lifts
the server's 30-second write timeout for its own response. Before 2.15.4 the
client got no response at all (`curl: (52) Empty reply`), even though the
file was still being written.

| Variable | Default | Effect |
|---|---|---|
| `MDDB_BACKUP_VERIFY` | `true` | `false` skips the check after a backup. Restores are always checked. |
| `MDDB_VERIFY_TIMEOUT` | `1h` | Longest a check may run. Walking a multi-gigabyte file on a cold spinning disk has been reported at over 40 minutes. |

## Scheduled backups

Since 2.18.9 the server takes backups on a schedule itself: a cron
automation ([AUTOMATIONS.md](AUTOMATIONS.md#scheduled-backups)) with a
`backup` action instead of a webhook. It needs `MDDB_CRONS=true`.

```bash
mddb-cli backup schedule add --name nightly --cron "0 3 * * *" --path nightly --keep 7
mddb-cli backup schedule list
mddb-cli backup schedule remove <id>      # the backups it took stay
```

or, through the API:

```bash
curl -X POST http://localhost:11023/v1/automation -H 'Content-Type: application/json' -d '{
  "type": "cron", "name": "nightly", "schedule": "0 3 * * *", "enabled": true,
  "backup": {"target": "local", "path": "nightly", "keep": 7}
}'
```

Over gRPC, `CreateAutomation` takes the same block as
`AutomationRuleProto.backup` ([GRPC.md](GRPC.md#new-in-v2189)).

Each run:

1. writes the backup with the same code as `GET /v1/backup`: a consistent
   copy, checked by opening it as a restore would (unless
   `MDDB_BACKUP_VERIFY=false`);
2. writes `<name>.sha256`, the sha256 of every file of the backup in
   `sha256sum` format (`sha256sum -c` checks a copy anywhere);
3. stores it on the target. For S3 every object is read back and its sha256
   compared with the file's;
4. only then deletes the oldest backups until `keep` remain. A failed run
   deletes nothing, so a schedule that keeps failing never eats into the
   backups it already has.

Backups are named `<prefix><UTC time>.db`, for example
`mddb-20261030T030000Z.db`. Retention only ever looks at names of that
form under the rule's prefix. Two rules writing to one place should have
different prefixes. Automations replicate, and every node with
`MDDB_CRONS=true` runs them: in a replicated setup enable crons on one node,
or the nodes write — and prune — the same names.

| Field | Default | Meaning |
|---|---|---|
| `target` | `local` | `local` or `s3` |
| `path` | | `local`: directory inside `MDDB_BACKUP_DIR`, so `POST /v1/restore` finds the backups there |
| `bucket` | `storageConfig.bucket` | `s3`: the bucket, created if missing |
| `prefix` | `storageConfig.prefix`, then `mddb-` | the start of every name (local) or key (S3; `/` allowed, e.g. `prod-eu/`) |
| `keep` | `0` | how many backups the target keeps; `0` keeps all |
| `collections` | `["*"]` | in the per-collection layout, the collections whose files go into the backup. A restore puts back exactly what the backup holds, so a collection left out is not in the restored database. In the single-file layout only `["*"]` is accepted. |
| `storageConfig` | | `s3`: `endpoint`, `region`, `accessKey`, `secretKey`, `useTLS` — the block an S3-backed collection uses ([config.md](config.md)). Reads blank `secretKey` and set `secretKeySet`; an update without `secretKey` keeps the stored one. |

**S3-compatible targets.** Anything that speaks the S3 API works through the
same client as S3-backed collections: AWS S3, MinIO, Cloudflare R2, Google
Cloud Storage and Azure through their S3 interfaces. The objects mirror the
local files: `<prefix><time>.db`, `<prefix><time>.db.sha256` and, in the
per-collection layout, `<prefix><time>.db.d/<collection>.db`. A run is
written to `MDDB_BACKUP_DIR/.scheduled-staging/` first and the staging copy
is removed afterwards, so the backup directory needs room for one backup.

**Restoring from S3.** Fetch the backup's objects into `MDDB_BACKUP_DIR`
with any S3 tool, check them, and restore as usual:

```bash
cd "$MDDB_BACKUP_DIR"
aws s3 cp --recursive s3://mddb-backups/prod-eu/ fetched/ \
  --exclude '*' --include 'mddb-20261030T030000Z.db*'
(cd fetched && sha256sum -c mddb-20261030T030000Z.db.sha256)
curl -X POST http://localhost:11023/v1/restore \
  -H 'Content-Type: application/json' -d '{"from": "fetched/mddb-20261030T030000Z.db"}'
```

**Watching it.**

- Every run is in the automation log (`GET /v1/automation-logs?ruleId=<id>`)
  with `action: "backup"`, `target` (the path or `s3://bucket/key`),
  `status`, `error` and `removed` (what retention deleted).
- `mddb_backup_last_success_timestamp{rule="<id>"}` on `/metrics` is the time
  of the last stored backup. Alert on it, e.g.
  `time() - mddb_backup_last_success_timestamp > 26 * 3600`.
- A failed run fires the `backup.failed` webhook event, with
  `{ruleId, ruleName, target, backup, error}` in `detail`.
- Each run is a job on the SSE stream (`GET /v1/events`): `job.started`,
  `job.progress` for each step (written, hashed, stored, pruned; `total` 4),
  then `job.completed` or `job.failed`. Its `collection` is `*`, so a client
  needs read permission on every collection to see it.

A run still going when its next one is due makes that one skip (logged as
`skipped`). A run that stored its backup but could not prune reports an
error and still sets the metric: the backup is there.

## Restoring

```bash
curl -X POST http://localhost:11023/v1/restore \
  -H 'Content-Type: application/json' -d '{"from": "nightly.db"}'
```

Before the live database is touched, the backup must pass the full integrity
check. A backup that fails is refused with bbolt's own description of the
damage, and the server keeps serving the database it had. The same check
applies to the snapshot a follower receives from its leader
([REPLICATION.md](REPLICATION.md)).

A backup written by an older release is brought to this release's on-disk
format as it is installed, before the server serves from it
([STORAGE-LAYOUT.md](STORAGE-LAYOUT.md#schema-version)). The backup file
itself is copied, never changed, so it can still be restored into the
release that wrote it. A database migrated by 2.18.9 is not fully readable by
2.18.8 (keys written with upper-case letters are found only by their
lower-case spelling): to go back, restore a backup taken before the upgrade.

Before 2.15.4 the check was only a read-only open. That open skips the
freelist walk, which is where a damaged file fails, so a damaged backup passed
it. The failure came later, after the live database had been moved aside, and
the server was left with no open database.

## Checking a file by hand

```bash
mddbd -verify-db /data/mddb.db
```

This prints `ok` and exits 0, or prints what is wrong and exits 1. Check a
copy, or check while the server is stopped: bbolt locks the file, so a
running server makes the check time out.

### Why the check runs in its own process

MDDB opens its database without a stored freelist (see below), so every open
rebuilds the freelist by walking every page. On a damaged file that walk
panics, and the panic cannot be safely contained inside the process. bbolt's
walk runs in a goroutine that can fault on its own after the panic, where no
`recover` reaches it. When it does not fault, it stays blocked holding a read
transaction, which is also why `bbolt compact` hangs on a damaged file. The
server therefore runs every check as a child process, a second copy of
`mddbd`, and only reads its exit status.

## Repairing a damaged database

```bash
mddbd -repair-db /data/mddb.db -repair-to /data/mddb-repaired.db
```

The repair opens the damaged file read-only, which skips the walk that fails,
and copies every bucket and key it can still read into a new file. That
includes documents, indexes, users, API keys and configuration. It then checks
the new file.

| Exit code | Meaning |
|---|---|
| `0` | Everything was copied and the new file passes the check. |
| `3` | The new file passes the check, but some buckets could not be read to the end. They are listed as `damaged:`. |
| `1` | The repair failed. |
| `2` | `-repair-to` is missing. |

`-repair-to` must not exist. The repair never writes over a file. To put the
result into service:

1. stop the server
2. move the damaged file aside
3. move the repaired file to the database path
4. start the server

Vector indexes are rebuilt from the copied vectors at startup.

## The freelist and startup time

By default, bbolt does not write its freelist on each commit
(`NoFreelistSync`). Writes are faster, and every open rebuilds the freelist by
walking every page. That takes seconds on a warm SSD, but has been reported at
over 40 minutes on a busy spinning disk
([#270](https://github.com/tradik/mddb/issues/270)).

| Variable | Default | Effect |
|---|---|---|
| `MDDB_FREELIST_SYNC` | on from 4 GiB | `true` writes the freelist on every commit, so an open reads one page instead of walking the file. Writes are slightly slower. The first open with it set still walks once, to write the freelist. Unset, it is on for files of 4 GiB or more (`MDDB_FREELIST_SYNC_THRESHOLD`, v2.18.2+). |
| `MDDB_PREWARM` | `auto` | (v2.18.2+) Before a walk, read the file sequentially into the page cache if it fits in half the available memory. |

Since v2.18.2 a clean shutdown stores the freelist whatever the setting, so a
planned restart reads one page. `MDDB_FREELIST_SYNC=true` is what covers the
start after a crash. See [STORAGE-TUNING.md](STORAGE-TUNING.md).

## One file per collection

With `MDDB_STORAGE_LAYOUT=per-collection` a backup is the main file plus a
`.d` directory holding one file per collection, and a restore puts both back.
See [STORAGE-LAYOUT.md](STORAGE-LAYOUT.md#backup-and-restore).

## A corpus pack is not a backup

A corpus pack (`mddb-cli corpus build`, since 2.18.10) is taken with the same
consistent copy, but of **one collection**, without any of the server's own
data, and it is installed **beside** the other collections, read-only, on any
server — a backup replaces the database it is restored into. Use a backup to
recover a server and a pack to share a collection between servers. See
[CORPUS-PACKS.md](CORPUS-PACKS.md#pack-or-backup).

## Compacting a database

A bbolt file never shrinks: deleting a large collection frees pages for
reuse but does not give them back to the filesystem. `GET /v1/storage`
reports the free space (`freeBytes`) and what each collection takes. To
reclaim it, copy the database into a new file with the server stopped:

```bash
mddbd -compact-db /data/mddb.db -compact-to /data/mddb.compact.db
# then: stop the server, move mddb.compact.db over mddb.db, start it
```

The copy holds every key in order with full pages, passes bbolt's integrity
check before the command reports success, and starts without a freelist walk.
Key order also helps cold reads: on a 2.5 GB file the walk after a crash fell
from 57 s to 44 s. `-compact-page-size` (4096–65536) sets a larger page; on
the same file 16 KiB made it 24% smaller but the walk slower (58 s), so
measure before using it.

## When the database is damaged at startup

The server stops and logs `startup step failed step=bolt.Open` with a
`remedy` field that points here. Before 2.15.4 it stopped with a raw panic
trace.
