---
title: "MDDB Storage Layout"
slug: "docs/storage-layout"
description: "One file or one file per collection: MDDB_STORAGE_LAYOUT, moving between layouts, backups, restores and the limits of each."
status: publish
---

# Storage Layout: One File or One File per Collection

Since 2.18.2 mddb can keep each collection in a bbolt file of its own
(#336). The default is unchanged: everything in one file.

| | `single` (default) | `per-collection` |
|---|---|---|
| Files | `mddb.db` | `mddb.db` plus `mddb.db.d/<collection>.db` |
| Deleting a collection | frees pages inside the file; the file keeps its size | removes the collection's file: the space is back at once |
| Start after a crash | one freelist walk over the whole file | each file is walked on its own, in the background, so writes to a small collection do not wait for a large one |
| Backup | one file | the main file plus one file per collection |
| Replication snapshot | yes | no (see [Limits](#limits)) |

Planned restarts are fast in both layouts: a clean shutdown stores the
freelist of every file ([STORAGE-TUNING.md](STORAGE-TUNING.md)).

## What lives where

A collection file holds everything that grows with the collection:
documents, the metadata index, revisions, key lookups, full-text postings
and statistics, vectors and their provenance, geo points, TTL entries and
temporal events.

The main file holds the server: users, API keys and permissions,
collection configurations, schemas, synonyms, stop words, spelling
dictionaries, automations and their logs, webhooks, the audit log, MCP keys,
embedding configurations, bulk-ingest jobs and the daily retrieval-gain
counters (`stats`, one small entry per day).

The keys and buckets are the same in both layouts; only the file differs.

## Switching

Stop the server first: both commands need the database to themselves.

```bash
# single -> per-collection
mddbd -split-collections /data/mddb.db
MDDB_STORAGE_LAYOUT=per-collection mddbd ...

# per-collection -> single
mddbd -merge-collections /data/mddb.db
mddbd ...            # MDDB_STORAGE_LAYOUT unset or "single"
```

`-split-collections` copies every collection's keys into its file in
batches, checks that each one arrived, and only then deletes it from the
main file. Run it again after an interruption: what was already moved is not
moved twice. Keys of a collection bucket that name no collection stay in the
main file and are counted in the report; the server refuses to start in the
per-collection layout while any collection data is left in the main file.
The main file keeps its size; `mddbd -compact-db` gives the space back
([BACKUP.md](BACKUP.md#compacting-a-database)).

`-merge-collections` copies each collection file back, checks the copy, and
removes the file.

## File names

A collection's file name is its name with everything except lower-case
letters, digits, `-` and `_` percent-encoded: `blog` is `blog.db`, `Blog` is
`%42log.db`, `a/b` is `a%2Fb.db`. Upper case is encoded because macOS and
Windows do not tell `Blog` from `blog`, and Windows' reserved names (`CON`,
`NUL`, `COM1`...) have their first letter encoded. A small collection's file
takes tens of kilobytes (64 KB measured after the first document) and grows
as it needs to.

## Backup and restore

`GET /v1/backup?to=nightly.db` writes `nightly.db` and `nightly.db.d/`.
Every file is a consistent copy and is verified, but the files are copied
one after another: a write that lands between two of them is in one and not
the other. Stop writes during the backup if the collections must agree to the
transaction.

A restore puts the collection files in place first, then the main file, and
puts the old collection files back if the main file fails. A backup taken in
the single-file layout has no `.d` directory and is refused in the
per-collection layout: restore it with the single layout, then split.

`-verify-db`, `-repair-db` and `-compact-db` take one file. Every file in
`mddb.db.d/` is an ordinary bbolt file and can be checked, repaired or
compacted on its own.

## Limits

- **Replication snapshots** stream one file, so a leader in this layout
  refuses them and a follower in it does not ask. The binlog works as before:
  each entry is applied to the file its key belongs to. Start a follower from
  a backup of the leader (the main file and its `.d` directory) and let it
  catch up from the binlog.
- **Write transactions do not span files.** A replicated batch that touches
  several collections is applied as one transaction per file, in order.
- Global managers (users, automations, webhooks...) are unaffected by the
  layout; they live in the main file in both.

## Settings

| Variable | Default | Meaning |
|---|---|---|
| `MDDB_STORAGE_LAYOUT` | `single` | `per-collection` keeps each collection in `<database>.d/<collection>.db` |
