# Versioning

Not everything in this repository moves together. This page says what does, so
a release does not quietly leave a component behind — and so nobody bumps a
component that was never meant to follow the server.

## The release version

The server's version is the product's version. It follows
[Semantic Versioning](https://semver.org/), and it is written in **thirteen**
places by hand. `scripts/check-version.sh` collects every one of them and fails
if they disagree; `make check-version` runs it, and so does the release
workflow before anything is tagged.

| Where | What it is |
|---|---|
| `services/mddbd/main.go` | `const VERSION` — what `mddbd --version` prints |
| `CHANGELOG.md` | the dated section for this release |
| `docs/openapi.yaml` | `info.version` — what an API client sees |
| `services/{mddbd,mddb-cli,mddb-panel}/snapcraft.yaml` | three snap packages |
| `.env.example`, `docker-compose.yml` | the image tag `docker compose up` pulls |
| `.ssg.yaml` | the version the documentation site renders |
| `clients/nodejs/package.json` | the npm package |
| `clients/python/pyproject.toml` | the PyPI package |
| `services/mddb-panel/package.json` | the panel |
| `services/php-extension/composer.json` | the PHP extension |
| `services/python-extension/pyproject.toml` | the Python extension |
| `integrations/langchain-mddb/pyproject.toml` | the LangChain adapter, and the `mddb-client>=` floor it declares |

The clients and extensions are on this list because they speak the server's
protocol. A published `@tradik/mddb-client@2.11.4` built from a 2.12.0 tree is
worse than a stale number: it names a server it was not built against, and
anyone diagnosing a protocol mismatch starts from the wrong assumption.

The two `uv.lock` files are absent from the guard on purpose: they are derived,
they record their project's own version, and CI's `uv sync --locked` already
refuses a lock that disagrees with its `pyproject.toml`. `scripts/bump-version.sh`
regenerates them, because uv is the only thing that knows the lock format it
will accept.

The **git tag** is deliberately absent from the guard. A tag is created after
the guard passes; checking it there would fail for the whole window between
bumping the version and tagging it. `RELEASING.md` covers that step.

## Components with their own version

These do **not** track the release, and a release must not bump them:

| Component | Why separate |
|---|---|
| `integrations/chrome-extension` | Published to the Chrome Web Store on its own cadence; the store's review queue decides when a version ships. |
| `integrations/github-action` | Consumers pin `@v1`; the action's interface changes independently of the server's. |
| `integrations/grafana-datasource` | Published to Grafana's plugin catalogue, with its own compatibility matrix. |
| `services/mddb-chat` | A separate service that talks to MDDB over gRPC like any other client. |
| `services/mddb-chat-widget` | Embedded in other people's pages; its version is a cache-busting concern, not a product claim. |

All five currently read `0.1.0` and have never been bumped. That is a fact
about the past, not a statement that they are pre-release — each is deployed.
Whoever cuts the first independent release of one of them should pick a real
starting number and say so here.

## What a version change means

- **Patch** (2.12.**0** → 2.12.1) — a fix. No stored data, configuration or API
  changes shape.
- **Minor** (2.**12**.0 → 2.13.0) — new capability. Existing configuration keeps
  working; a database written by the older version opens unchanged. This is
  checked, not assumed: `test/upgrade-fixtures/` holds real databases from
  earlier releases and CI opens them on every run.
- **Major** (**2**.12.0 → 3.0.0) — something that was true stops being true. A
  configuration key changes meaning, an endpoint's response changes shape, or a
  stored format needs migrating.

A deprecation gets at least one minor release of warning before it is removed,
and the warning names the replacement.

## Bumping the version

```bash
# Edit the thirteen sources, then:
make check-version          # fails while any of them disagree
scripts/check-version.sh --print   # shows each source and what it says
```

`scripts/tests/test-version.sh` is the guard's own test suite — eight cases,
including the two shapes that have actually happened: a documentation file left
behind during a bump, and a package manifest nobody was watching.
