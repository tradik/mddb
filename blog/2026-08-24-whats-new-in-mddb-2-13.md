---
title: "What's New in MDDB 2.13"
slug: "blog/whats-new-in-mddb-2-13"
status: publish
type: post
date: 2026-08-24
tags: [release, windows, performance, security, operations]
excerpt: "MDDB 2.13 runs its test suite on Windows, makes vector comparison 3.7–6.3× faster on amd64, and fixes two ways a restore could destroy the database it was restoring."
description: "MDDB 2.13: native Windows testing, AVX2 vector maths, two data-loss fixes in restore, and one breaking change to user deletion."
---

MDDB 2.13 is mostly about two things: making the server run somewhere it never
had, and making the part of it that runs most often considerably faster.

It also fixes two ways a restore could destroy the database it was meant to
restore, both found while looking for something else.

This article covers what affects deployment and day-to-day use. The full list is
in the [changelog](https://github.com/tradik/mddb/blob/2.13.0/CHANGELOG.md).

## Windows: the tests pass there now

The full test suite runs on `windows-latest` on every push. That is a narrower
claim than "Windows support", and the difference matters.

What you get: the server and CLI build for `windows/amd64` from source, and the
suite — more than 3,000 test functions across 32 packages — passes there on
every commit.

What you do not get: release binaries. There are none for Windows, and
`docs/INSTALLATION.md` says so in a table rather than leaving it to be inferred.
Build from source:

```bash
make build-windows      # → dist/windows-amd64/{mddbd.exe,mddb-cli.exe}
```

WSL2 remains the supported route, and it runs the same Linux binary every
release is built and tested against.

One capability differs deliberately. **Unix domain sockets are refused on
Windows.** Not because they fail to compile — Windows 10 supports `AF_UNIX` and
the listener would come up — but because the listener's security model is
owner-only file permissions, and `os.Chmod` on Windows cannot express them. A
socket there would serve happily with the access restriction it documents
silently absent. It says so and points at `tcp://127.0.0.1` instead.

### What the Windows job found on its first run

Nine failures, and none of them in the server. Seven were real bugs in tests and
helpers — and **six of those were present on Linux too**. Windows did not break
anything; it stopped forgiving.

The pattern behind most of them: a test waited for a signal that arrives
*before* the state it then asserts on. An embedding worker writes the vector
store and then the index; a test waited on the store and read the index. On
Linux that gap is too small to lose. On a slower machine it is not.

That is a good argument for running your own test suite somewhere unfamiliar,
whether or not you ship there.

## Vector comparison is 3.7–6.3× faster on amd64

Vector maths had two paths: NEON on arm64, and a plain Go loop everywhere else.
There was no third, so on amd64 — most of the servers MDDB runs on — every
comparison went through the scalar loop. The file is called
`vector_math_scalar.go`, which reads like a fallback and was the production path
for the dominant architecture.

It now has an AVX2 implementation, selected by a runtime CPU check. Measured
with the same benchmark as before, 768 dimensions, median of five runs:

| Candidates | Before | After | |
|---|---|---|---|
| 1,000 | 6.8 GB/s | **43.0 GB/s** | 6.3× |
| 10,000 | 6.7 GB/s | 36.0 GB/s | 5.4× |
| 100,000 | 6.8 GB/s | 24.8 GB/s | 3.7× |

Nothing to configure. A machine without AVX2 takes the scalar path at startup —
there is one amd64 build, not one per microarchitecture.

The shape of those numbers is discussed in [a separate
post](/blog/the-loop-that-was-not-waiting-for-memory/), because "flat throughput"
turned out to say something specific about where the time was going.

## Two ways a restore could destroy your database

Both were found while auditing file replacement for the Windows port, and
neither was on any list.

`POST /v1/restore` and the gRPC `Restore` RPC share one careful implementation:
validate the backup before touching the live file, keep the current database as
a rollback snapshot, never return with the server holding anything but an open,
serving database.

Two other entry points had their own copies of that logic, and both were wrong.

**The MCP `restore` tool** did not validate the file, take the restore lock,
keep a rollback or rebuild the caches. Any readable file inside the backup
directory would overwrite the live database — it did not have to *be* a
database, because copying bytes always succeeds and only the reopen fails, by
which point the original is gone.

Worth knowing how it presented: after the failure, reads still worked. They were
being served from cache. The database underneath was closed, and the server
looked healthy until the first write.

**The replication snapshot install** had the same shape: a truncated stream was
indistinguishable from a complete one until the reopen, after the follower's
data had already been replaced.

All three callers now share one implementation.

While testing that, coverage showed something worth repeating: **the rollback
had never run.** Not once since it was written. Every existing failure test
supplied a backup that validation rejected, so the swap returned before it ever
touched the live file. The code whose entire job is to save your database when a
restore goes wrong had never executed.

If you have a safety net you have never triggered on purpose, it is worth
checking whether your tests reach it.

## Three fixes from the field

**A fresh Docker volume no longer restart-loops on first start.** The image
created `/app/data`; the published `docker-compose.yml` mounts a volume at
`/data`. Docker copies an image directory's ownership into a fresh named volume
only where that path exists in the image — `/data` did not, so it arrived owned
by root while the server runs as an unprivileged user. First start died with
`permission denied`. The image now creates it.

**An API key sent as `Authorization: Bearer` is accepted.** It used to be parsed
as a JWT and refused with `invalid token` — a message about the credential when
the problem was the header. The MCP endpoint had always accepted either header,
so two surfaces of the same server disagreed, and clients meet the stricter one
first.

**Deleting a user now deletes it.** See below; this one changes behaviour.

## Is it leaking?

A question inherited from a downstream fork, which reported memory growing from
42 MB to 153 MB under sustained load.

Measured here over 45 minutes and 1.26 million mixed operations: resident memory
grew from 190 MB to 247 MB, and the Go heap went from 43 MB to 47 MB. The growth
is the memory-mapped database file, which is file-backed and reclaimable — not a
leak.

`docs/DEPLOYMENT.md` now explains which number to watch, and
[a separate post](/blog/rss-is-the-wrong-number/) covers why the obvious one
misleads. `tools/bench/soak` is in the repository if you want to run it against
your own workload.

## Before upgrading

**Deleting a user is now a real deletion.** `DELETE /v1/auth/users/:username`
used to disable the account and keep it, which held the username against every
later registration — `register` answered `409 user already exists` — while the
response said `deleted`. Rotating a tenant's credentials by deleting and
re-registering the same name could not work.

It now removes the account, its API keys, its per-collection permissions and its
group memberships, and the name is free immediately.

If you have a client that reads the disabled record back after deleting, it will
no longer find one, and `GET /v1/auth/users` no longer lists deleted accounts.

The alternative — keeping the soft delete and letting `register` re-enable the
account — was the smaller change and the more dangerous one. The record carries
permissions and group memberships, so a name that looked free would have handed
whoever claimed it the privileges of whoever held it before.

Your audit log is unaffected. That is where the record of who existed, and who
removed them, belongs.

**Everything else is compatible.** A database written by 2.12 opens unchanged,
existing configuration keeps working, and no other API changes shape.
