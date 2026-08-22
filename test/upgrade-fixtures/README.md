# Upgrade fixtures

A database file produced by an **older release**, kept so the current build can
prove it still opens one.

## Why this exists

The most painful failure a database can hand a user is not a wrong result — it
is a file the new version refuses to open. The user is then stuck between
versions: the old binary is gone, the new one will not read their data, and
nothing in the test suite ever asked whether it could (TEST-003).

Every other test in this repository writes with the current code and reads with
the current code. That proves the format is self-consistent, which is exactly
the property that stays true while compatibility quietly breaks.

## What is here

| Fixture | Written by | Contains |
|---|---|---|
| `v2.11.4.db.gz` | mddbd v2.11.4 | 4 documents in `upgrade-fixture`: three prose, one CSS with `kind: code`; metadata on every one; revisions; FTS index |

Stored gzipped because BoltDB pre-allocates its file — 16.8 MB of mostly zeros
becomes 20 KB.

## Adding a fixture for a new release

Do this **once per minor release**, from the release tag rather than from main:

```bash
git worktree add /tmp/vX.Y.Z vX.Y.Z
cd /tmp/vX.Y.Z/services/mddbd && go build -o /tmp/mddbd-X.Y.Z .

mkdir /tmp/fixgen && cd /tmp/fixgen && /tmp/mddbd-X.Y.Z &
# add documents covering whatever that release introduced
gzip -9 -c mddb.db > test/upgrade-fixtures/vX.Y.Z.db.gz
```

Then add the version to `TestOpensFixturesFromOlderReleases`. A fixture is
worth keeping for as long as anyone might still be running that version.
