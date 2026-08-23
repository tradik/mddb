---
title: "Blog authoring guide"
slug: "blog/authoring-guide"
status: draft
---

# MDDB Blog

Release announcements and engineering notes, published through the same
pipeline as the docs: each post is a Markdown file with YAML frontmatter,
ingested into the `blog` collection and rendered by the SSG.

## Conventions

- **Filename:** `YYYY-MM-DD-short-title.md` (date first — keeps the folder
  chronologically sorted).
- **Frontmatter:** the docs fields plus the post-only ones the SSG understands
  from 1.8.47 on:

```yaml
---
title: "Post title"              # keep the rendered <title> under 60 chars —
                                 # the theme appends " — MDDB" (21 → 7 chars)
slug: "blog/short-title"
status: publish                  # draft keeps it out of publication
type: post                       # explicit; drives URL and template behaviour
date: 2026-08-23                 # ordering, and the date-based URL
tags: [rag, retrieval, mcp]      # generates listings at /tag/<slug>/
excerpt: "What a reader sees in the listing and the feed."
description: "SEO description; distinct from excerpt, 70–160 characters."
mermaid: true                    # only on posts that contain a diagram
mermaid_theme: neutral
mermaid_background: "#ffffff"
---
```

  `excerpt` and `description` are separate on purpose: the first is what a
  human reads in a listing, the second is what a search result shows. Setting
  only one leaves the other empty, and every documentation page shipped an
  empty `<meta name="description">` for exactly that reason until 2.12.

- **Slug prefix** `blog/` puts the post under `/blog/<name>/` on the site;
  the `/blog/` index page (`docs/BLOG.md`, `layout: blog`) lists posts
  newest-first automatically. Add a `date: YYYY-MM-DD` field for ordering.
  Use `status: draft` to keep a post out of publication.
- Diagrams: ` ```mermaid ` fences render on GitHub and on the site. Declare
  `mermaid: true` in the post's frontmatter so the runtime is injected only
  where a diagram exists. Do not turn on `minify_all` in `.ssg.yaml`: mermaid
  source is whitespace-significant and minification corrupts it.
- Tone: write for a human who is deciding whether to upgrade — lead with what
  changed for *them*, keep API details in the docs and link out.
- Never mention third-party database products by name — describe our features
  on their own merits.
- Every number, command and JSON body in a post should come from a running
  server, not from memory. Both 2.12 posts were written against a local
  instance, and doing so found a bug in the feature being described.

## Publishing

Posts are loaded like any other Markdown folder:

```bash
scripts/load-md-folder.sh --collection blog blog/
```

## Before publishing

```bash
make docs-metadata        # the empty metadata.json the SSG expects
make docs-build           # fails on a broken internal link or an orphan page
```

The build is the check: it fails on dead internal links, on an indexable page
nothing links to, and on an `<img>` with no `alt`. A post that builds is a post
that deploys — the docs workflow runs the same generator.
