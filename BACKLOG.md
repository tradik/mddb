# Backlog

One line is one session: a ready prompt, its size (S/M/L) and the model to run
it on. No status here: state lives in issues and CHANGELOG.md. A session with no
other instruction takes the first line, does only that line, commits, and
deletes the line.

| Prompt | Size | Model |
|---|---|---|
| SEO: add a `VideoObject` JSON-LD block for the homepage video (name, description, thumbnailUrl, uploadDate from the YouTube page, embedUrl, contentUrl); make `check_schema: strict` pass | S | Sonnet |
| SEO: give the 50 generated API reference pages (`docs/api/reference/*`) meta descriptions of 70–160 characters, via the OpenAPI tag descriptions in `docs/openapi.yaml` | M | Sonnet |
| Perf: cut render-blocking web fonts (three Google Fonts families on every page): self-host Geist and Geist Mono as WOFF2 with `font-display: swap`, drop Instrument Serif if unused, and recompress `images/hero.webp` (345 KB) | M | Sonnet |
