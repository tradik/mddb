---
title: "Is It Leaking? Read a Different Number"
slug: "blog/rss-is-the-wrong-number"
status: publish
type: post
date: 2026-08-24
tags: [operations, memory, profiling, monitoring]
excerpt: "A report said MDDB grew from 42 MB to 153 MB under load. The observation was probably right and the conclusion wrong — and the difference is one field you already have."
description: "Why resident memory grows under sustained load in MDDB, which figure separates a cache from a leak, and a 45-minute measurement that settles it."
---

A downstream fork of MDDB reported memory growing from 42 MB to 153 MB under
sustained load, filed as something to keep an eye on. That is a reasonable thing
to notice and a reasonable thing to worry about.

It is also not enough information to conclude anything, and the reason is worth
knowing whether or not you run MDDB.

## What resident memory includes

MDDB stores data in a single file and memory-maps it. The pages the operating
system has faulted in count towards the process's resident set — they are part
of the number `top` shows you — but they are **file-backed**. The kernel can
drop them under pressure and read them back from disk. They are not memory the
process is holding onto.

So a server whose database has grown to 160 MB will show a couple of hundred
megabytes resident while using very little of it as heap. The number went up.
Nothing leaked.

The figure that distinguishes the two is the Go heap: what the program has
allocated and not returned. MDDB reports it:

```bash
curl -s http://localhost:11023/v1/system/info | jq '{memoryHeap, memorySystem, numGoroutines}'
```

```json
{
  "memoryHeap": 56172544,
  "memorySystem": 93505816,
  "numGoroutines": 42
}
```

Those are the real figures from the final sample of the run described below,
not an illustration.

`memoryHeap` is the one to watch. A cache filling to its limit shows up there
and stops. A leak shows up there and does not.

## Forty-five minutes of mixed traffic

Rather than reason about it, we measured. `tools/bench/soak` sustains a mixed
workload — writes, updates, reads, keyword search, hybrid search, deletes — and
samples both figures alongside the goroutine count.

The run: 45 minutes, **1,261,570 operations, zero errors**, against a database
that grew to 160 MB.

| | Start | After 45 min |
|---|---|---|
| Resident memory | 190 MB | 247 MB |
| **Heap in use** | **43 MB** | **47 MB** |
| Goroutines | 38 | 42 |

Resident memory rose 57 MB and then flattened — it moved 7 MB across the final
ten minutes. The heap did not move outside its normal oscillation, which spans
roughly 36–52 MB as garbage collection runs.

That is the answer, and it matches the arithmetic: a database that grew to
160 MB pulled 57 MB of its pages into residence.

## The profile detail that settles it

Sampling can show a flat heap and still miss a slow leak, so the run also takes
heap profiles — three of them, at the start, the middle and the end.

Three rather than two, deliberately. A single start-to-end comparison cannot
distinguish "grew early, then settled" from "grew steadily throughout", and
those have opposite conclusions.

The largest single allocator over the whole run gained 6.7 MB across the first
half — and **lost 2.6 MB across the second**. Total heap movement was 16.5 MB
spread across 75 call sites with none above 4 MB, and one significant allocator
finished *below* where it started.

Leaks do not shrink. That is a buffer pool reaching steady state.

## What to actually watch

**Not resident memory.** It tracks your database file, which is the design
working.

**The heap, across readings hours apart.** One sample tells you nothing; a
number that climbs across successive readings is worth reporting.

**The goroutine count.** It is in the same response, it is cheap, and a count
that only goes up is its own finding — one that a flat heap will not reveal,
because a blocked goroutine can be holding very little.

If you want to check your own workload rather than trust ours, the harness is in
the repository:

```bash
make soak SOAK_URL=http://localhost:11023 SOAK_PID=$(pgrep -x mddbd) SOAK_DURATION=45m
```

It needs `MDDB_PPROF_ENABLED=true` on the server, writes a CSV of every sample,
and leaves the three profiles behind for `go tool pprof -base` to compare.

## On the original report

The fork's observation was almost certainly accurate. Their memory did grow from
42 MB to 153 MB, and if their database grew similarly, that is what should have
happened.

The conclusion was the problem, and it was not carelessness — it is that
resident memory *cannot* answer the question being asked of it. Their
measurement did not separate heap from file-backed pages, so it could not have
distinguished a cache from a leak. Neither could ours, until we stopped looking
at the number that was easiest to get.

Full guidance is in
[the deployment docs](https://github.com/tradik/mddb/blob/2.13.0/docs/DEPLOYMENT.md),
in the troubleshooting section, with these numbers behind it.
