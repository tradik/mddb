#!/usr/bin/env python3
"""Say which workflow runs the SonarCloud scan for a change.

    git diff --name-only RANGE | sonar-scan-owner.py EVENT BRANCH

EVENT is `push` or `pull_request`; BRANCH is the pushed branch, or the base
branch of the pull request. Prints `tests` when the Tests workflow
(.github/workflows/test.yml) runs for the change, and `sonarcloud` otherwise.

Why (OPS-021): the scan needs the coverage reports, and they come from the
Tests workflow, so that is where the scan runs. But Tests has a path filter —
a change to the WordPress plugin or the docs does not start it — and every
change still has to be scanned. .github/workflows/sonarcloud.yml starts on
every push and pull request, asks this script, and scans only when Tests will
not. One scan per push either way; a second one would replace the first, and
whichever finished last would decide the coverage on the pull request.

The answer is read from test.yml's own `on:` block, so the filter has one
source. GitHub's glob rules are reproduced for what that block uses (`*`,
`**`); anything else — `?`, `[...]`, `!` negation, `paths-ignore` — is
refused rather than guessed at, and scripts/tests/test-sonar.sh runs this
against the real file.
"""

import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
TESTS = os.path.join(ROOT, ".github", "workflows", "test.yml")


def glob_regex(pattern):
    """GitHub filter glob to a regex: `**` crosses `/`, `*` does not."""
    if pattern.startswith("!") or re.search(r"[?\[\]+]", pattern):
        raise ValueError(f"unsupported filter pattern {pattern!r}")
    out, i = "", 0
    while i < len(pattern):
        if pattern.startswith("**/", i):
            out, i = out + "(?:.*/)?", i + 3
        elif pattern.startswith("**", i):
            out, i = out + ".*", i + 2
        elif pattern[i] == "*":
            out, i = out + "[^/]*", i + 1
        else:
            out, i = out + re.escape(pattern[i]), i + 1
    return re.compile(out + r"\Z")


def unquote(value):
    value = value.split(" #", 1)[0].strip()
    return value[1:-1] if value[:1] in "'\"" and value[-1:] == value[:1] else value


def triggers(path):
    """{event: {"branches": [...], "paths": [...]}} from a workflow's on: block."""
    events, event, key, inside = {}, None, None, False
    with open(path, encoding="utf-8") as fh:
        for raw in fh:
            line = raw.rstrip("\n")
            stripped = line.strip()
            if not stripped or stripped.startswith("#"):
                continue
            indent = len(line) - len(line.lstrip(" "))
            if indent == 0:
                inside = stripped in ("on:", '"on":', "'on':")
                continue
            if not inside:
                continue
            if indent == 2:
                event = stripped.rstrip(":")
                events[event] = {}
            elif indent == 4:
                key, _, value = stripped.partition(":")
                if key == "paths-ignore":
                    raise ValueError("paths-ignore is not supported")
                value = value.strip()
                if value.startswith("["):
                    events[event][key] = [unquote(v) for v in value.strip("[]").split(",") if v.strip()]
                else:
                    events[event][key] = []
            elif stripped.startswith("- ") and event and key:
                events[event][key].append(unquote(stripped[2:]))
    return events


def owner(event, branch, files, workflow=TESTS):
    spec = triggers(workflow).get(event)
    if spec is None:
        return "sonarcloud"
    branches = spec.get("branches")
    if branches and not any(glob_regex(b).match(branch) for b in branches):
        return "sonarcloud"
    paths = spec.get("paths")
    if not paths:
        return "tests"
    regexes = [glob_regex(p) for p in paths]
    return "tests" if any(r.match(f) for f in files for r in regexes) else "sonarcloud"


def main(argv):
    if len(argv) not in (3, 4):
        sys.exit(__doc__)
    workflow = argv[3] if len(argv) == 4 else TESTS
    files = [f.strip() for f in sys.stdin if f.strip()]
    try:
        print(owner(argv[1], argv[2], files, workflow))
    except ValueError as err:
        sys.exit(f"sonar-scan-owner: {err}")


if __name__ == "__main__":
    main(sys.argv)
