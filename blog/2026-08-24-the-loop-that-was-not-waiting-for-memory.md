---
title: "Flat Throughput Is a Diagnosis"
slug: "blog/the-loop-that-was-not-waiting-for-memory"
status: publish
type: post
date: 2026-08-24
tags: [performance, vector-search, simd, benchmarking]
excerpt: "Two optimisations to MDDB's vector loop predicted 33% and delivered 1.5%. The benchmark had been saying why for months, in a column nobody read as a diagnosis."
description: "How a constant 6.8 GB/s across every working-set size revealed that MDDB's vector comparison was instruction-bound, not memory-bound — and what fixed it."
---

MDDB compares vectors constantly. Every semantic search walks a set of
candidates and computes a similarity against each one. On amd64 that walk was a
plain Go loop, and it had resisted two attempts to speed it up.

Both attempts were reasonable. Both were measured. Both delivered almost
nothing, and the reason was in the benchmark output the whole time.

## Two failures

The first idea was to replace the per-vector loop with a batch kernel — one call
that walks the whole candidate matrix instead of one call per row. Fewer calls,
better locality, the sort of change that usually pays.

Measured difference: within noise. About 1%.

The second was better. Cosine similarity divides a dot product by the product of
two vector norms:

```
cos(a,b) = Σ(aᵢbᵢ) / √(Σaᵢ² · Σbᵢ²)
```

Computing that per candidate recomputes `Σaᵢ²` — the *query's* norm — every
single time. The query does not change across a search. Hoisting it out of the
loop removes a third of the arithmetic for free.

Predicted: 33%. Measured: **1.5%**.

That is not a small win. That is a wrong model.

## The column that was already saying so

Here is the benchmark, before any of this, at three working-set sizes:

| Candidates | Bytes touched | Throughput |
|---|---|---|
| 1,000 | ~3 MB | 6.8 GB/s |
| 10,000 | ~29 MB | 6.7 GB/s |
| 100,000 | ~293 MB | 6.8 GB/s |

Three hundred megabytes and three megabytes at the same rate.

**Memory-bound code cannot do that.** A working set that fits in cache and one
fifteen times larger than it read at identical speed means the memory system was
never the thing being waited on. If it had been, the large case would have been
slower — that is what "memory-bound" means.

So the loop was waiting on something that does not care how much data there is.
It was waiting on itself: the floating-point unit retiring one multiply-add at a
time, at a rate fixed by the instruction stream rather than by the bytes.

That single fact explains both failed optimisations at once. A batch kernel
reduces call overhead and improves locality — neither was the constraint.
Hoisting the norm removes arithmetic over the same bytes — but arithmetic *was*
the constraint, and removing a third of it while the loop still issues one
element per instruction changes the instruction count far less than it changes
the operation count. `a[i]` is already in a register when `a[i]*b[i]` is
computed; squaring it costs almost nothing extra.

The only thing that helps is doing more elements per instruction.

## Eight at a time

AVX2 registers hold eight 32-bit floats. One instruction multiplies eight pairs
and accumulates eight results. The inner loop keeps three of those accumulators
— the dot product and both norms — so a single pass over the two vectors
produces everything cosine similarity needs.

| Candidates | Before | After | |
|---|---|---|---|
| 1,000 | 6.8 GB/s | **43.0 GB/s** | 6.3× |
| 10,000 | 6.7 GB/s | 36.0 GB/s | 5.4× |
| 100,000 | 6.8 GB/s | 24.8 GB/s | 3.7× |

Look at the new column's shape rather than its magnitude. It is **not flat**. It
degrades as the working set grows, from 43 GB/s down to 25.

That degradation is the good news. It means the code is now waiting on memory —
which is where a routine that streams hundreds of megabytes and does three
arithmetic operations per element *should* be waiting. The ceiling moved from
one that scaling cannot help to one that hardware can.

## Two decisions worth explaining

**Assembly rather than C.** The arm64 path reaches NEON through cgo, which works
there. It would not work here, and the reason is one line in the release
pipeline: release binaries are built with cgo disabled. A C kernel would have
compiled on a developer's machine and never shipped. There is also a cost
argument — a 768-dimension comparison takes well under a microsecond, and a cgo
boundary crossing would be a visible fraction of that.

**Chosen at startup, not at build time.** The CPU is checked once when the
process starts. A machine without AVX2 takes the scalar path and works. There is
one amd64 build of MDDB, not one per microarchitecture, and it has to start
everywhere.

## The test that was wrong before the code was

The acceptance criterion was that the new implementation agree with the old one
to within 1e-5 relative. Written, run — and the AVX2 path failed two cases.

Before loosening the tolerance, the same test was run against the scalar build.
It failed **four**.

The vectorised kernel is *more* accurate than the loop it replaces, and
predictably so: eight accumulators each sum an eighth of the terms, so each
carries an eighth of the accumulated rounding error. The test was wrong, not the
code.

The failing cases were near-total cancellation — 384 products of magnitude 100
summing to 3.1. When the result is that much smaller than the terms, error
relative to the *result* measures the cancellation, not the arithmetic. The
criterion is now error relative to the sum of the term magnitudes, which is what
actually bounds float32 accumulation regardless of the order things are added
in. Both paths pass it comfortably.

## What to take from this

If a benchmark reports the same throughput across working sets that differ by an
order of magnitude, that is not a boring row of numbers. It is telling you which
resource you are short of, and it is worth reading before spending a week on the
wrong one.

Both failed optimisations were sound engineering aimed at the wrong constraint.
The measurement that would have redirected them was already on the screen.
