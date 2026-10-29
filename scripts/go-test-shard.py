#!/usr/bin/env python3
"""Print a `go test -run` pattern selecting one shard of a package's tests.

    go test -list '.*' . | python3 scripts/go-test-shard.py SHARDS INDEX

INDEX counts from 1. Every listed test lands in exactly one shard, and the
shards hold about the same number of tests.

Why prefixes and not names: services/mddbd has over 3,000 top-level tests,
107 KB of names. A pattern listing a third of them would not fit Windows'
32 KB command line. Tests are grouped by name prefix instead, and a prefix
that holds more than its share is split into longer ones. A whole shard is a
few hundred short prefixes.

The groups are disjoint. A name shorter than the prefix length of its level
becomes its own group, matched exactly with `$`. Longer names never match it,
and it never matches a longer prefix.
"""

import re
import sys


def groups(names, limit, plen=1):
    """Split names into (pattern, count) groups of at most `limit` names."""
    buckets = {}
    for n in names:
        buckets.setdefault(n[:plen], []).append(n)
    out = []
    for key in sorted(buckets):
        members = buckets[key]
        exact = len(key) < plen  # key is a whole name, shorter than this level
        if exact:
            out.append((re.escape(key) + "$", len(members)))
        elif len(members) > limit and any(len(m) > plen for m in members):
            out.extend(groups(members, limit, plen + 1))
        else:
            out.append((re.escape(key), len(members)))
    return out


def shard_pattern(names, shards, index):
    """The -run pattern for shard `index` (1-based) of `shards`."""
    names = sorted(set(names))
    limit = max(1, len(names) // (shards * 4))
    loads = [0] * shards
    picked = [[] for _ in range(shards)]
    # Largest groups first, each to the lightest shard: deterministic, and
    # within one group's size of an even split.
    for pattern, count in sorted(groups(names, limit), key=lambda g: (-g[1], g[0])):
        i = loads.index(min(loads))
        loads[i] += count
        picked[i].append(pattern)
    chosen = sorted(picked[index - 1])
    if not chosen:
        return "^$"  # nothing to run in this shard
    return "^(?:" + "|".join(chosen) + ")"


def main(argv):
    if len(argv) != 3 or not argv[1].isdigit() or not argv[2].isdigit():
        print(__doc__.strip().splitlines()[2].strip(), file=sys.stderr)
        return 2
    shards, index = int(argv[1]), int(argv[2])
    if shards < 1 or not 1 <= index <= shards:
        print("INDEX must be between 1 and SHARDS", file=sys.stderr)
        return 2
    names = [line.strip() for line in sys.stdin
             if re.match(r"^(Test|Fuzz|Example)\w*$", line.strip())]
    print(shard_pattern(names, shards, index))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
