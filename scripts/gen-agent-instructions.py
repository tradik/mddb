#!/usr/bin/env python3
"""Generate per-tool agent instruction files from one source (INT-018).

AGENTS.md is the source of truth. Every other format is a wrapper around the
same prose: a Claude Code skill, a Cursor rule and a Windsurf rule differ only
in their front matter and where they live. Keeping four hand-written copies is
how they drift apart, so they are generated instead.

Usage: scripts/gen-agent-instructions.py [--check]
  --check  exit non-zero if any generated file is out of date (for CI)
"""
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
BASE = ROOT / "integrations" / "agent-instructions"
SOURCE = BASE / "AGENTS.md"

GENERATED_NOTE = (
    "<!-- Generated from integrations/agent-instructions/AGENTS.md by\n"
    "     scripts/gen-agent-instructions.py — edit the source, not this file. -->\n"
)

DESCRIPTION = (
    "Use MDDB's MCP tools well: which search tool fits a question, how to ask "
    "for chunks and projections instead of whole documents, and how to use the "
    "memory_* tools across sessions."
)


def claude_skill(body: str) -> str:
    """Claude Code skill: YAML front matter with a trigger description."""
    return (
        "---\n"
        "name: mddb\n"
        f"description: {DESCRIPTION}\n"
        "---\n\n"
        f"{GENERATED_NOTE}\n"
        f"{body}"
    )


def cursor_rule(body: str) -> str:
    """Cursor rule (.mdc): front matter decides when the rule is attached."""
    return (
        "---\n"
        f"description: {DESCRIPTION}\n"
        "globs:\n"
        "alwaysApply: false\n"
        "---\n\n"
        f"{GENERATED_NOTE}\n"
        f"{body}"
    )


def windsurf_rule(body: str) -> str:
    """Windsurf rule: plain markdown with an activation hint at the top."""
    return (
        "---\n"
        "trigger: model_decision\n"
        f"description: {DESCRIPTION}\n"
        "---\n\n"
        f"{GENERATED_NOTE}\n"
        f"{body}"
    )


TARGETS = {
    BASE / "claude-code" / "SKILL.md": claude_skill,
    BASE / "cursor" / "mddb.mdc": cursor_rule,
    BASE / "windsurf" / "mddb.md": windsurf_rule,
}


def main() -> int:
    check = "--check" in sys.argv
    if not SOURCE.exists():
        print(f"missing source: {SOURCE}", file=sys.stderr)
        return 2

    body = SOURCE.read_text(encoding="utf-8")
    stale = []

    for path, render in TARGETS.items():
        want = render(body)
        if check:
            if not path.exists() or path.read_text(encoding="utf-8") != want:
                stale.append(path.relative_to(ROOT))
            continue
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(want, encoding="utf-8")
        print(f"wrote {path.relative_to(ROOT)}")

    if check:
        if stale:
            print("agent instructions are out of date:", file=sys.stderr)
            for p in stale:
                print(f"  {p}", file=sys.stderr)
            print("\nRun: make agent-instructions", file=sys.stderr)
            return 1
        print(f"✓ agent instructions match the source ({len(TARGETS)} files)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
