#!/usr/bin/env python3
"""The two SonarCloud configurations exclude the same files (OPS-021).

    check-sonar-exclusions.py [--print] [SCANNER_PROPERTIES AUTO_PROPERTIES]

sonar-project.properties is read by the scanner, which runs in CI since
2026-10-30. .sonarcloud.properties is read only by Automatic Analysis, which
is off, and matters again only if someone turns it back on. Each file had its
own exclusion list, and they drifted: the first scanner run raised seven
findings on workflow YAML and three on tools/bench that .sonarcloud.properties
had excluded with a written reason, and failed the 2.18.10 gate.

Rules:
1. sonar.exclusions name the same patterns in both files — except that a
   pattern Automatic Analysis excludes outright may instead be a resourceKey
   of a sonar.issue.ignore.multicriteria entry in the scanner file. Automatic
   Analysis ignores those entries, so whole-file exclusion is its only lever;
   the scanner can keep every other rule running on the file.
2. sonar.cpd.exclusions name the same patterns in both files.

Exits 1 and lists every pattern on one side only.
"""

import os
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def properties(path):
    """Parse a .properties file: comments, `key=value`, `\\` continuations."""
    props, buf = {}, ""
    with open(path, encoding="utf-8") as fh:
        for raw in fh:
            line = raw.strip()
            if not buf and (not line or line[0] in "#!"):
                continue
            if line.endswith("\\"):
                buf += line[:-1]
                continue
            line, buf = buf + line, ""
            key, sep, value = line.partition("=")
            if sep:
                props[key.strip()] = value.strip()
    return props


def patterns(props, key):
    return {p.strip() for p in props.get(key, "").split(",") if p.strip()}


def ignored_resources(props):
    names = patterns(props, "sonar.issue.ignore.multicriteria")
    return {props.get(f"sonar.issue.ignore.multicriteria.{n}.resourceKey", "") for n in names} - {""}


def problems(scanner, auto):
    out = []
    s_ex, a_ex = patterns(scanner, "sonar.exclusions"), patterns(auto, "sonar.exclusions")
    for p in sorted(a_ex - s_ex - ignored_resources(scanner)):
        out.append(f"sonar.exclusions: {p} only in .sonarcloud.properties")
    for p in sorted(s_ex - a_ex):
        out.append(f"sonar.exclusions: {p} only in sonar-project.properties")
    s_cpd, a_cpd = patterns(scanner, "sonar.cpd.exclusions"), patterns(auto, "sonar.cpd.exclusions")
    for p in sorted(a_cpd - s_cpd):
        out.append(f"sonar.cpd.exclusions: {p} only in .sonarcloud.properties")
    for p in sorted(s_cpd - a_cpd):
        out.append(f"sonar.cpd.exclusions: {p} only in sonar-project.properties")
    return out


def main(argv):
    args = [a for a in argv[1:] if a != "--print"]
    if len(args) not in (0, 2):
        sys.exit(__doc__)
    scanner_path, auto_path = args or (
        os.path.join(ROOT, "sonar-project.properties"),
        os.path.join(ROOT, ".sonarcloud.properties"),
    )
    scanner, auto = properties(scanner_path), properties(auto_path)
    found = problems(scanner, auto)
    for p in found:
        print(f"check-sonar-exclusions: {p}", file=sys.stderr)
    if found:
        print("Add the pattern to both files, with its reason (OPS-021).", file=sys.stderr)
        return 1
    if "--print" in argv:
        print(f"sonar.exclusions: {len(patterns(scanner, 'sonar.exclusions'))} patterns, "
              f"sonar.cpd.exclusions: {len(patterns(scanner, 'sonar.cpd.exclusions'))} — in step")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
