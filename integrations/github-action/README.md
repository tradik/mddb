# MDDB Sync — GitHub Action

[![Action](https://img.shields.io/badge/GitHub%20Action-mddb--sync-2088FF?logo=githubactions&logoColor=white)](https://github.com/tradik/mddb/tree/main/integrations/github-action)
[![Node](https://img.shields.io/badge/Node-24-3C873A?logo=node.js&logoColor=white)](https://nodejs.org/)
[![TypeScript](https://img.shields.io/badge/TypeScript-6.0-3178C6?logo=typescript&logoColor=white)](https://www.typescriptlang.org/)
[![MDDB](https://img.shields.io/badge/MDDB-2.9.5%2B-1f7a8c)](https://github.com/tradik/mddb)
[![Coverage](https://img.shields.io/badge/coverage-98%25-brightgreen)](#tests)
[![License](https://img.shields.io/badge/license-BSD--3--Clause-green)](#license)

Native JavaScript GitHub Action that ingests repository files into an [MDDB](https://github.com/tradik/mddb) collection via `POST /v1/add`. Drop it into any workflow to keep an MDDB collection in sync with your docs, READMEs, OpenAPI specs, or any other text/markdown/JSON artefacts that live in git.

Designed to be picked up by `uses: tradik/mddb/integrations/github-action@v1` — no Docker image, no marketplace publish required.

## Contents

- [Quick start](#quick-start)
- [Inputs](#inputs)
- [Outputs](#outputs)
- [Record mapping](#record-mapping)
- [Local development](#local-development)
- [Tests](#tests)
- [CI/CD](#cicd)
- [Release](#release)
- [Architecture](#architecture)
- [Changelog](CHANGELOG.md)

## Quick start

```yaml
# .github/workflows/sync-docs.yml
name: Sync docs to MDDB

on:
  push:
    branches: [main]
    paths: ['docs/**', 'README.md']

jobs:
  sync:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v6

      - name: Sync to MDDB
        uses: tradik/mddb/integrations/github-action@v1
        with:
          mddb-url: https://mddb.tradik.com
          api-key: ${{ secrets.MDDB_API_KEY }}
          collection: project-docs
          path: |
            docs/**/*.md
            README.md
          ignore: docs/draft/**
          key-strategy: path
          key-prefix: ${{ github.repository }}/
```

## Inputs

| Name | Default | Description |
| --- | --- | --- |
| `mddb-url` | `https://mddb.tradik.com` | Base URL of the MDDB instance, no trailing `/`. |
| `api-key` | _(empty)_ | Bearer token (typically `vk_…`). Empty = MDDB instance without auth. |
| `collection` | **required** | Target MDDB collection. Auto-created on first `/v1/add`. |
| `path` | `**/*.md` | Newline-separated glob patterns. `!`-prefixed patterns are negated. |
| `ignore` | _(empty)_ | Newline-separated globs to exclude. |
| `working-directory` | `.` | Directory the globs resolve against. |
| `language` | `en_US` | `lang` recorded on every document. |
| `key-strategy` | `path` | `path` (slugified relative path), `hash` (sha1 of content), `filename` (basename). |
| `key-prefix` | _(empty)_ | String prepended to every key. Useful when multiple repos share one collection. Validated: only `A-Z a-z 0-9 . _ / -`, max 100 chars (else the action fails fast). |
| `concurrency` | `8` | Parallel `/v1/add` requests in flight (1–64). |
| `timeout-seconds` | `30` | HTTP timeout per request (1–600). |
| `verify-ssl` | `true` | Disable only for self-signed dev MDDB instances. |
| `dry-run` | `false` | Walk + build documents, but skip the network. |
| `fail-on-error` | `true` | Fail the job on per-document upload errors. Set `false` to keep the summary as a warning. |

## Outputs

| Name | Description |
| --- | --- |
| `documents-scanned` | Files matched by the glob (after `ignore`). |
| `documents-added` | Successful `/v1/add` upserts. |
| `documents-failed` | Files that failed to upload. |

## Record mapping

A file at `docs/guide/intro.md` containing `# Hello` produces:

```json
{
  "collection": "project-docs",
  "key": "tradik/mddb/docs/guide/intro.md",
  "lang": "en_US",
  "meta": {
    "source": ["github-action"],
    "path": ["docs/guide/intro.md"],
    "extension": [".md"],
    "size": ["7"],
    "repository": ["tradik/mddb"],
    "ref": ["<commit sha>"]
  },
  "contentMd": "# Hello"
}
```

- Markdown (`.md`, `.markdown`, `.mdx`) and plain-text (`.txt`, `.rst`, `.adoc`) are stored verbatim in `contentMd`.
- JSON, YAML, TOML, HTML, XML, CSS and common code files are wrapped in a fenced code block with the matching language for FTS/vector indexing.
- `repository` and `ref` come from `GITHUB_REPOSITORY` and `GITHUB_SHA` when present (always set in GitHub-hosted workflows).

## Local development

```bash
# From the MDDB repo root:
make gha-install         # npm install
make gha-test            # jest
make gha-coverage        # jest --coverage (>=90% enforced)
make gha-build           # bundle dist/ with @vercel/ncc
make gha-check           # format + lint + test + build

# Or from this directory:
cd integrations/github-action
make install build test
```

The bundled `dist/` **must be committed** — GitHub Actions consume this directory directly, so any change to `src/` needs a matching `make build` and commit.

## Tests

```bash
make gha-coverage
```

57 unit tests, ~98% line / 95% branch coverage:

- `__tests__/inputs.test.ts` — input parsing, defaults, validation.
- `__tests__/document.test.ts` — slugify, key strategies, content fencing, meta.
- `__tests__/walker.test.ts` — globbing, ignores, dedupe, negated patterns, absolute paths.
- `__tests__/client.test.ts` — `/v1/add` POST, auth header, retries on 5xx/network errors, `verify-ssl=false` agent, ping classification.
- `__tests__/main.test.ts` — end-to-end orchestration, failure accounting, dry-run, fail-on-error.

## CI/CD

Workflow: [`.github/workflows/github-action.yml`](../../.github/workflows/github-action.yml). Scoped by `paths:` so it only runs when files under `integrations/github-action/**` change.

| Job | When | What |
| --- | --- | --- |
| `test` | every PR + push (matrix: Node 22, 24) | `npm ci`, `npm run all` (format check + lint + tests w/ coverage + build), uploads `coverage/`. |
| `verify-dist` | every PR + push | Rebuild and assert `dist/` matches the committed bundle (catches stale releases). |
| `smoke` | every PR + push | Run the bundled action with `dry-run: true` against the integration's own README to confirm it boots. |
| `release` | tag push `gha-v*` | Publish a GitHub Release pinning `v<major>` / `v<major>.<minor>` floating tags via [`mhausenblas/mkdocs-deploy-gh-pages`-style tag bumping](#release). |

## Release

Action consumers reference floating tags (`@v1`, `@v1.2`), so on every release we:

```bash
# 1. Bump version
$EDITOR integrations/github-action/package.json     # "version": "0.2.0"
$EDITOR integrations/github-action/CHANGELOG.md

# 2. Rebuild dist/ + commit
cd integrations/github-action
make build
git add dist/ package.json CHANGELOG.md
git commit -m "github-action: release v0.2.0"

# 3. Tag and push
git tag -a gha-v0.2.0 -m "github-action v0.2.0"
git tag -f gha-v0 gha-v0.2.0
git push origin gha-v0.2.0 gha-v0 --force-with-lease
```

`gha-v0` is the floating major tag consumers pin to (`uses: tradik/mddb/integrations/github-action@gha-v0`). The release workflow can be wired up to do step 3 automatically — see CI section.

## Architecture

```
GitHub Actions runner
        │
        ▼
node24 dist/index.js
        │
        │  walk(working-directory, patterns, ignore)
        │  for each file (concurrency-limited):
        │      buildDocument(file)
        │      client.addDocument(doc)
        ▼
POST https://mddb.tradik.com/v1/add
Authorization: Bearer vk_…
```

The action is a single bundled Node 24 script (`dist/index.js`, ~1 MB) produced by `@vercel/ncc` from `src/main.ts`. It uses Node's built-in `fetch` (no `axios`/`got` dependency) and `@actions/glob` for cross-platform pattern matching.

## License

BSD-3-Clause — see [LICENSE](../LICENSE) for the integrations. The MDDB server itself is under the Business Source License 1.1 ([LICENSING.md](../../LICENSING.md)).
