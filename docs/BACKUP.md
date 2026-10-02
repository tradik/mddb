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
| `MDDB_FREELIST_SYNC` | `false` | `true` writes the freelist on every commit, so an open reads one page instead of walking the file. Writes are slightly slower. The first open with it set still walks once, to write the freelist. |

## When the database is damaged at startup

The server stops and logs `startup step failed step=bolt.Open` with a
`remedy` field that points here. Before 2.15.4 it stopped with a raw panic
trace.
