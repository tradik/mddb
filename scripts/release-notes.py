#!/usr/bin/env python3
"""Build release notes for a tag from CHANGELOG.md (OPS-011).

The release workflow used to publish the same hand-written text for every tag,
carrying benchmark figures from one historical measurement. Notes that do not
come from the changelog cannot describe the release they are attached to.

This extracts the section for a version — from either "## [x.y.z] - date" or
the [Unreleased] section when the version has not been dated yet — and adds the
installation block, which is the one part that genuinely is the same every release.

Usage: scripts/release-notes.py <version> [--changelog PATH]
"""
import argparse
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

INSTALL = """
## Installation

### Debian / Ubuntu
```bash
curl -LO https://github.com/tradik/mddb/releases/download/v{v}/mddb_{v}_amd64.deb
sudo dpkg -i mddb_{v}_amd64.deb
```

### Fedora / RHEL
```bash
curl -LO https://github.com/tradik/mddb/releases/download/v{v}/mddb-{v}.x86_64.rpm
sudo rpm -i mddb-{v}.x86_64.rpm
```

### Homebrew
```bash
brew install tradik/tap/mddb
```

### Snap
```bash
sudo snap install mddb
```

### Docker
```bash
docker pull tradik/mddb:{v}
```

Every artifact is published with a `sha256` checksum and an SLSA provenance
attestation; see the assets below.
"""


def section_for(changelog: str, version: str, allow_unreleased: bool) -> str | None:
    """Return the body of the section describing version, or None."""
    lines = changelog.split("\n")
    dated = re.compile(rf"^## \[{re.escape(version)}\]")
    unreleased = re.compile(r"^## \[Unreleased\]")
    heading = re.compile(r"^## \[")

    start = None
    for i, line in enumerate(lines):
        if dated.match(line):
            start = i + 1
            break
    if start is None and allow_unreleased:
        # Not dated yet: the work is still under [Unreleased]. Only reachable
        # when the caller confirmed this version is the one the code claims —
        # otherwise a typo in the tag would publish another version's notes
        # under the wrong heading.
        for i, line in enumerate(lines):
            if unreleased.match(line):
                start = i + 1
                break
    if start is None:
        return None

    end = len(lines)
    for i in range(start, len(lines)):
        if heading.match(lines[i]):
            end = i
            break
    body = "\n".join(lines[start:end]).strip()
    return body or None


def declared_version() -> str | None:
    """The version the built binary will report, from services/mddbd/main.go."""
    src = ROOT / "services" / "mddbd" / "main.go"
    if not src.exists():
        return None
    m = re.search(r'^const VERSION = "([0-9]+\.[0-9]+\.[0-9]+)"', src.read_text(encoding="utf-8"), re.M)
    return m.group(1) if m else None


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("version", help="release version, with or without a leading v")
    ap.add_argument("--changelog", default=str(ROOT / "CHANGELOG.md"))
    args = ap.parse_args()

    version = args.version.lstrip("v")

    # Resolved and confined to the repository. A release-notes generator has no
    # business reading a file outside the tree it is describing, and --changelog
    # is a path this process is handed rather than one it chose.
    changelog = Path(args.changelog).resolve()
    try:
        changelog.relative_to(ROOT.resolve())
    except ValueError:
        print(f"--changelog must be inside {ROOT}: {changelog}", file=sys.stderr)
        return 2
    if not changelog.is_file():
        print(f"missing changelog: {changelog}", file=sys.stderr)
        return 2

    # Falling back to [Unreleased] is only safe for the version the code itself
    # reports: releasing 9.9.9 from a tree that says 2.12.0 would otherwise
    # publish this release's notes under a version nobody built.
    declared = declared_version()
    allow_unreleased = declared is not None and declared == version

    body = section_for(changelog.read_text(encoding="utf-8"), version, allow_unreleased)
    if not body:
        hint = (
            f"The repository reports {declared}." if declared else
            "Could not read the version from services/mddbd/main.go."
        )
        print(
            f"no CHANGELOG section for {version}. {hint}\n"
            f"Add a '## [{version}] - YYYY-MM-DD' section, or tag the version the "
            "tree declares while its work sits under [Unreleased].",
            file=sys.stderr,
        )
        return 1

    print(f"# MDDB {version}\n")
    print(body)
    print(INSTALL.format(v=version))
    return 0


if __name__ == "__main__":
    sys.exit(main())
