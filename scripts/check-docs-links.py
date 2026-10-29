#!/usr/bin/env python3
"""Fail the docs build on internal links that would hit a redirect.

SSG validates the generated output itself — `check_links`, `check_images`,
`check_meta` and `check_orphans` in `.ssg.yaml` cover dead links, missing alt
attributes, missing title/description and orphan pages. This script covers the
one thing it cannot: those checks resolve a URL against the output directory,
which is not how Cloudflare Pages serves it.

Pages rewrites two shapes before answering, with a 308 each time:

  * `/docs/api/reference/index.html` → `/docs/api/reference/`  (`.html` stripped)
  * `/docs/config`                 → `/docs/config/`       (slash appended)

Both work for a visitor, so nothing is broken — they simply cost a round trip
and a hop of crawl budget on every request. To the output directory they look
perfectly valid, so only a check that models the deploy target sees them.

External hosts are never fetched: this gates the deploy and must stay offline
and deterministic.

Usage: python3 scripts/check-docs-links.py [output_dir]
"""

from __future__ import annotations

import os
import sys
from html.parser import HTMLParser
from urllib.parse import urlsplit

from docs_output import read_within, site_path, within_root

# Cloudflare injects /cdn-cgi/ endpoints (email obfuscation, RUM beacon) at the
# edge. They are not build artefacts and are served by Cloudflare, not Pages.
SKIP_PATHS = ("/cdn-cgi/",)

# Attributes that carry a URL a crawler will follow or fetch.
URL_ATTRS = ("href", "src")


class LinkCollector(HTMLParser):
    """Collects href/src values from a built page.

    Markup shown inside <code>/<pre> arrives escaped in the generated HTML, so
    documented examples are text to the parser and never counted as elements.
    """

    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self.links: list[str] = []

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        for name, value in attrs:
            if name in URL_ATTRS and value:
                self.links.append(value)


def normalise(url: str) -> str | None:
    """Reduce a raw attribute value to a checkable path, or None to skip it."""
    url = url.strip()
    parts = urlsplit(url)

    if parts.scheme or parts.netloc:
        # Absolute, or protocol-relative. Only this host is served from the
        # output; everything else is somebody else's server.
        path = site_path(url)
        if path is None:
            return None
    else:
        path = parts.path

    if not path or path.startswith(SKIP_PATHS):
        return None
    return path


def resolve(root: str, page: str, url: str) -> str:
    """Map a link to the filesystem path it would be served from.

    The result can point outside `root` — `../../../etc/passwd` is a link a
    page is free to contain — so callers check `within_root` before use.
    """
    if url.startswith("/"):
        return os.path.join(root, url.lstrip("/"))
    return os.path.normpath(os.path.join(os.path.dirname(page), url))


HTML_SUFFIX = ".html"
INDEX_PAGE = "index.html"


def redirect_target(root: str, page: str, url: str) -> str | None:
    """The URL Pages would 308 to, or None when it serves the link directly."""
    target = resolve(root, page, url)
    if not within_root(root, target):
        return None

    if url.endswith(HTML_SUFFIX) and os.path.isfile(target):
        if os.path.basename(url) == INDEX_PAGE:
            return url[: -len(INDEX_PAGE)] or "/"
        return url[: -len(HTML_SUFFIX)]
    if os.path.isfile(target):
        return None
    if os.path.isfile(os.path.join(target, INDEX_PAGE)) and not url.endswith("/"):
        return url + "/"
    return None


def iter_pages(root: str):
    for dirpath, _dirnames, filenames in os.walk(root):
        for name in filenames:
            if name.endswith(HTML_SUFFIX):
                yield os.path.join(dirpath, name)


def check(root: str) -> dict[str, tuple[str, set[str]]]:
    """Return {link: (URL it redirects to, pages containing it)}."""
    found: dict[str, tuple[str, set[str]]] = {}
    for page in iter_pages(root):
        source = os.path.relpath(page, root)
        collector = LinkCollector()
        collector.feed(read_within(root, page))
        for raw in collector.links:
            url = normalise(raw)
            if url is None:
                continue
            final = redirect_target(root, page, url)
            if final is not None:
                found.setdefault(raw, (final, set()))[1].add(source)
    return found


def main() -> int:
    # Resolve once so every derived path is compared against a real, absolute
    # root rather than whatever shape the argument arrived in.
    root = os.path.realpath(sys.argv[1] if len(sys.argv) > 1 else "dist")
    if not os.path.isdir(root):
        print(f"error: output directory {root!r} does not exist — run 'make docs-build'")
        return 2

    redirects = check(root)
    total = sum(1 for _ in iter_pages(root))

    if not redirects:
        print(f"✅ {total} pages in {root}/: no internal link hits a redirect")
        return 0

    print(
        f"❌ {len(redirects)} internal link(s) that 308 on Cloudflare Pages — "
        "link the final URL instead:\n"
    )
    for raw in sorted(redirects):
        final, pages = redirects[raw]
        sources = sorted(pages)
        shown = ", ".join(sources[:5])
        more = f" (+{len(sources) - 5} more)" if len(sources) > 5 else ""
        print(f"  {raw}  →  {final}\n      linked from: {shown}{more}")
    print()
    return 1


if __name__ == "__main__":
    sys.exit(main())
