#!/usr/bin/env python3
"""Fail on every vulnerability govulncheck says our code reaches, except the
ones listed in .github/govulncheck-ignore.txt for the exact module version.

govulncheck has no way to say "this one is wrong". When the vulnerability
database is wrong about a version, the only choices were a red build on every
PR and every daily scan, or turning the scan off. This keeps it on: an entry
silences one advisory for one module at one version, so an upgrade, a
downgrade or any other advisory is judged afresh.

Usage: govulncheck -format json ./... | scripts/govulncheck-gate.py IGNOREFILE
"""
import json
import sys


def read_stream(text):
    """govulncheck -format json writes a stream of objects, not one array."""
    dec, i, out = json.JSONDecoder(), 0, []
    while True:
        while i < len(text) and text[i].isspace():
            i += 1
        if i >= len(text):
            return out
        obj, i = dec.raw_decode(text, i)
        out.append(obj)


def read_ignores(path):
    """Lines of `ID module version  # why`; blank lines and comments skipped."""
    ignores = {}
    with open(path, encoding="utf-8") as f:
        for line in f:
            body, _, why = line.partition("#")
            fields = body.split()
            if not fields:
                continue
            if len(fields) != 3 or not why.strip():
                sys.exit(f"{path}: every entry needs ID, module, version and a # reason: {line.strip()}")
            ignores[tuple(fields)] = why.strip()
    return ignores


def main():
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    ignores = read_ignores(sys.argv[1])
    called = set()
    for obj in read_stream(sys.stdin.read()):
        finding = obj.get("finding")
        if not finding:
            continue
        top = finding["trace"][0]
        if top.get("function"):  # reached from our code, not merely required
            called.add((finding["osv"], top["module"], top.get("version", "")))

    failing = []
    for key in sorted(called):
        if key in ignores:
            print(f"ignored: {' '.join(key)} — {ignores[key]}")
        else:
            failing.append(key)
    for key in failing:
        print(f"::error::{key[0]} in {key[1]}@{key[2]} is reachable from this module — https://pkg.go.dev/vuln/{key[0]}")
    if failing:
        return 3
    print(f"govulncheck: no unexcused reachable vulnerabilities ({len(called)} found, {len(called) - len(failing)} excused)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
