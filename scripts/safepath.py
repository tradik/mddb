"""Path confinement for the repository's helper scripts.

The Sonar helpers (check-sonar-exclusions.py, sonar-coverage-paths.py,
sonar-scan-owner.py) take file paths on the command line. A path is accepted
only when it resolves, symlinks followed, inside the repository or the current
working directory; anything else stops the script. This keeps a script from
reading or writing outside the tree it was run for (Sonar pythonsecurity:S8707).
"""

import os

ROOT = os.path.realpath(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))


def confined(path):
    """The real path of `path`, or SystemExit when it lies outside the
    repository and outside the current working directory."""
    real = os.path.realpath(path)
    for base in (ROOT, os.path.realpath(os.getcwd())):
        if real == base or real.startswith(base + os.sep):
            return real
    raise SystemExit(f"{path}: outside the repository and the working directory")
