#!/usr/bin/env python3
"""Rewrite a coverage report so every file path is absolute in this checkout.

    sonar-coverage-paths.py go        ROOT IN OUT
    sonar-coverage-paths.py lcov      ROOT IN OUT PROJECT_DIR
    sonar-coverage-paths.py cobertura ROOT IN OUT PROJECT_DIR

ROOT is the repository checkout the scanner reads (in CI: $GITHUB_WORKSPACE).
PROJECT_DIR is the repository-relative directory the tests ran in, for the
formats that do not name it themselves.

Why (OPS-021): the reports are written by other jobs and name files the way
each tool sees them, not the way the scanner does.

- A Go profile names files by import path: `mddb/util.go`,
  `mddb-cli/main.go`, `github.com/tradik/mddb/clients/go/mddb/v2/admin.go`.
  The module path is mapped back to its directory through the go.mod files
  under ROOT, so a new module needs no change here.
- vitest and `node --test` write LCOV `SF:` paths relative to the package
  (`src/lib/download.js`, `index.js`). The panel and the Node client both
  have files with such short names, and the scanner would have to guess.
- pytest-cov writes the absolute directory of the runner that ran the tests
  into `<source>`. It matches the scan runner only by coincidence.

Every path the scanner is given is absolute and points into ROOT, so none of
that guessing happens. A Go file whose module is not in ROOT is an error: a
report that silently matches nothing is the failure this script exists to
prevent.
"""

import os
import re
import sys
import xml.etree.ElementTree as ET

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from safepath import confined  # noqa: E402

GO_LINE = re.compile(r"^(?P<file>.+\.go):(?P<rest>\d+\.\d+,\d+\.\d+ \d+ \d+)$")
SKIP_DIRS = {".git", "node_modules", "vendor", ".venv", "target"}


def go_modules(root):
    """Map every module path declared under root to its directory."""
    modules = {}
    for dirpath, dirnames, filenames in os.walk(root):
        # Hidden directories (.git, .claude worktrees, caches) hold copies of
        # the tree, never the modules being scanned.
        dirnames[:] = [d for d in dirnames if d not in SKIP_DIRS and not d.startswith(".")]
        if "go.mod" not in filenames:
            continue
        with open(os.path.join(dirpath, "go.mod"), encoding="utf-8") as fh:
            for line in fh:
                parts = line.split()
                if len(parts) == 2 and parts[0] == "module":
                    modules[parts[1]] = dirpath
                    break
    return modules


def go_path(path, modules):
    """Resolve one import-path file name; the longest module path wins."""
    best = None
    for module in modules:
        if path.startswith(module + "/") and (best is None or len(module) > len(best)):
            best = module
    if best is None:
        raise ValueError(f"no go.mod under the checkout declares the module of {path}")
    return os.path.join(modules[best], path[len(best) + 1:])


def rewrite_go(root, lines):
    modules = go_modules(root)
    out = []
    for line in lines:
        m = GO_LINE.match(line)
        if line.startswith("mode:") or not m:
            out.append(line)
            continue
        out.append(f"{go_path(m.group('file'), modules)}:{m.group('rest')}")
    return out


def project_path(root, project, path):
    """A path the tests saw, relative or absolute, as an absolute path in root."""
    project = project.strip("/")
    if not os.path.isabs(path):
        return os.path.normpath(os.path.join(root, project, path))
    marker = "/" + project + "/"
    if path.endswith("/" + project):
        return os.path.join(root, project)
    if marker in path:
        return os.path.join(root, project, path.rsplit(marker, 1)[1])
    return path


def rewrite_lcov(root, project, lines):
    return ["SF:" + project_path(root, project, line[3:]) if line.startswith("SF:") else line
            for line in lines]


def rewrite_cobertura(root, project, src, dst):
    tree = ET.parse(src)
    for source in tree.getroot().iter("source"):
        source.text = project_path(root, project, (source.text or "").strip())
    tree.write(dst, encoding="utf-8", xml_declaration=True)


def main(argv):
    if len(argv) < 5 or argv[1] not in ("go", "lcov", "cobertura"):
        sys.exit(__doc__)
    fmt, root, src, dst = argv[1], confined(argv[2]), confined(argv[3]), confined(argv[4])
    project = argv[5] if len(argv) > 5 else None
    if fmt != "go" and not project:
        sys.exit(f"{fmt} needs PROJECT_DIR")
    if fmt == "cobertura":
        rewrite_cobertura(root, project, src, dst)
        return
    with open(src, encoding="utf-8") as fh:
        lines = fh.read().splitlines()
    try:
        out = rewrite_go(root, lines) if fmt == "go" else rewrite_lcov(root, project, lines)
    except ValueError as err:
        sys.exit(f"sonar-coverage-paths: {err}")
    with open(dst, "w", encoding="utf-8") as fh:
        fh.write("\n".join(out) + "\n")


if __name__ == "__main__":
    main(sys.argv)
