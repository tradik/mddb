#!/usr/bin/env bash
# Merges Go coverage profiles from test shards into one profile.
#
#   scripts/merge-coverprofiles.sh OUT IN...
#
# The test suite of services/mddbd runs in shards (scripts/go-test-shard.py),
# and each shard writes its own profile. Concatenating them would count a
# block once per shard, so the per-area floors (check-coverage-areas.sh) and
# Codecov would see the same statements several times over. Here a block is
# kept once, with its counts added up: covered if any shard covered it.
#
# All inputs must share one mode (`go test -race` writes "atomic").
set -euo pipefail

if [[ $# -lt 2 ]]; then
	echo "usage: $0 OUT IN..." >&2
	exit 2
fi
out="$1"
shift

awk '
	FNR == 1 {
		if ($1 != "mode:") { print FILENAME ": not a coverage profile" > "/dev/stderr"; exit 2 }
		if (mode == "") mode = $2
		else if ($2 != mode) { print FILENAME ": mode " $2 ", expected " mode > "/dev/stderr"; exit 2 }
		next
	}
	NF == 3 {
		key = $1 " " $2
		if (!(key in count)) order[++n] = key
		count[key] += $3
	}
	END {
		if (mode == "") exit 2
		print "mode: " mode
		for (i = 1; i <= n; i++) print order[i], count[order[i]]
	}
' "$@" >"${out}"
