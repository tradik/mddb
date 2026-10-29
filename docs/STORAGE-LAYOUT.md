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
embedding configurations, bulk-ingest jobs, collection aliases (`aliases`)
and the daily retrieval-gain counters (`stats`, one small entry per day).

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
puts the old collection files back if the main file fails. A backup from an
older release is migrated as it is installed ([Schema version](#schema-version));
the backup itself is copied, never changed. A backup taken in
the single-file layout has no `.d` directory and is refused in the
per-collection layout: restore it with the single layout, then split.

`-verify-db`, `-repair-db` and `-compact-db` take one file. Every file in
`mddb.db.d/` is an ordinary bbolt file and can be checked, repaired or
compacted on its own.

## Schema version

Since 2.18.9 every file — the main file and, in this layout, each collection
file — records the version of the on-disk format it holds: bucket
`schema_version`, key `version`, eight big-endian bytes. A file without it
is version 0, written by an earlier release.

| Version | Release | Change |
|---|---|---|
| 1 | 2.18.9 | Key-index entries (`bykey`) store the key and language with ASCII letters in lower case, as the document identifier does (DOC-017). Spellings of one key that differ only in case are merged into one entry. |

When a file is behind, the server brings it up to date **before it reads or
writes it**:

- at startup, before any listener opens (the main file), and as each
  collection file is opened, in parallel;
- when a restore installs a backup, and when a follower installs a
  replication snapshot — before the server serves from the new file.

A collection file the server creates starts unstamped and is stamped the
next time it is opened; for a file this release wrote that costs one read of
its key index and rewrites nothing. `-split-collections` and
`-merge-collections` copy entries as they find them, so they may be run on a
file not yet migrated: the files they write are migrated when the server
next opens them.

**Crash safety.** A step commits its work in chunks of 10 000 entries and
the version is recorded only after the last one. A step interrupted part-way
runs again at the next start; entries it already rewrote are left as they
are. When two spellings meet, the entry pointing at a document that exists
wins; when both or neither do, the one already in lower case stays. No entry
is dropped unless another one for the same key survives.

**Time.** A file already at the current version costs one read
transaction. The first start of 2.18.9 on an older file reads its key index
once — seconds for millions of documents. If that start follows a crash and
the freelist is being rebuilt in the background
([STORAGE-TUNING.md](STORAGE-TUNING.md)), the migration waits for the
rebuild, once.

**Going back.** An older release ignores the version and looks keys up as
written, so after 2.18.9 has migrated a file, 2.18.8 finds documents whose
key or language has upper-case letters only by the lower-case spelling. To
downgrade, restore a backup taken before the upgrade. A file whose version
is newer than the running build is opened as it is, with a warning, and not
rewritten.

**Replication.** Each server migrates its own files. A follower applying
entries from a leader on an older release stores key-index entries in the
new form. Upgrade the followers before the leader: a follower on an older
release receiving the leader's new entries finds those documents only by
their lower-case spelling.

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
