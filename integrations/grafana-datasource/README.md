# MDDB — Grafana Datasource Plugin

[![CI](https://github.com/tradik/mddb/actions/workflows/grafana-datasource.yml/badge.svg)](https://github.com/tradik/mddb/actions/workflows/grafana-datasource.yml)
[![Plugin](https://img.shields.io/badge/Grafana%20Plugin-tradik--mddb--datasource-F46800?logo=grafana&logoColor=white)](https://github.com/tradik/mddb/tree/main/integrations/grafana-datasource)
[![Grafana](https://img.shields.io/badge/Grafana-%E2%89%A5%2011.0-F46800?logo=grafana&logoColor=white)](https://grafana.com/)
[![Node](https://img.shields.io/badge/Node-24-3C873A?logo=node.js&logoColor=white)](https://nodejs.org/)
[![TypeScript](https://img.shields.io/badge/TypeScript-6.0-3178C6?logo=typescript&logoColor=white)](https://www.typescriptlang.org/)
[![MDDB](https://img.shields.io/badge/MDDB-2.9.16%2B-1f7a8c)](https://github.com/tradik/mddb)
[![Coverage](https://img.shields.io/badge/coverage-%E2%89%A5%2090%25-brightgreen)](#tests)
[![License](https://img.shields.io/badge/license-BSD--3--Clause-green)](#license)

Native Grafana datasource plugin that lets dashboards query an [MDDB](https://github.com/tradik/mddb) instance directly. Five built-in query types cover MDDB's analytics surface — temporal histograms, hot-doc leaderboards, metadata aggregates, full-text search, and instance stats — and route through Grafana's datasource proxy so CORS and auth are handled by the Grafana server, not the browser.

This is a *frontend* plugin (no backend Go binary), so installation is a single drop-in directory and the plugin works the same way in Grafana OSS, Enterprise, and Cloud (with the appropriate unsigned-plugin or private-plugin policy).

> **Auth model.** Grafana never ships `secureJsonData` (the API key) back to the frontend after save — only `secureJsonFields.apiKey: true` (a boolean) is exposed. The plugin therefore declares two proxy routes in `plugin.json`: `auth` (with `Authorization: Bearer {{ .SecureJsonData.apiKey }}`) and `noauth` (no header). The browser picks one based on whether the API key is configured, and Grafana's `pluginproxy` injects the bearer token server-side before forwarding to MDDB. No secret ever touches the browser.

## Contents

- [Why a dedicated datasource?](#why-a-dedicated-datasource)
- [Quick start](#quick-start)
- [Query types](#query-types)
- [Configuration](#configuration)
- [Local development](#local-development)
- [Tests](#tests)
- [Known audit findings](#known-audit-findings)
- [Packaging & release](#packaging--release)
- [Architecture](#architecture)
- [Changelog](CHANGELOG.md)

## Why a dedicated datasource?

MDDB already exposes a Prometheus-compatible `/metrics` endpoint, so server-level metrics (request rate, latency, database size) belong on the existing Prometheus datasource — see [docs/TELEMETRY.md](../../docs/TELEMETRY.md).

This plugin is for the *content-shaped* signals Prometheus can't capture: how often individual documents are read, which documents are trending, what the metadata facet distribution looks like over time, and what an FTS / vector query returns at this moment. Those answers live in MDDB itself — exposing them as a first-class Grafana datasource lets you mix them with Prometheus, Loki, Tempo, etc. on a single dashboard.

## Quick start

### 1. Install the plugin

Once built (`make build` produces `dist/`), copy it into Grafana's plugin directory or run from Docker:

```bash
# Docker (image is built from the bundled dist/)
make build
make docker
docker run --rm -p 3000:3000 \
  -e GF_AUTH_ANONYMOUS_ENABLED=true \
  -e GF_AUTH_ANONYMOUS_ORG_ROLE=Admin \
  tradik/mddb-grafana-datasource:0.1.0

# Or bind-mount into an existing Grafana
make build
docker run --rm -p 3000:3000 \
  -e GF_PLUGINS_ALLOW_LOADING_UNSIGNED_PLUGINS=tradik-mddb-datasource \
  -v $(pwd)/dist:/var/lib/grafana/plugins/tradik-mddb-datasource \
  grafana/grafana:13.0.1
```

For a permanent install on bare metal:

```bash
sudo mkdir -p /var/lib/grafana/plugins/tradik-mddb-datasource
sudo cp -r dist/* /var/lib/grafana/plugins/tradik-mddb-datasource/
# /etc/grafana/grafana.ini -> [plugins] allow_loading_unsigned_plugins = tradik-mddb-datasource
sudo systemctl restart grafana-server
```

### 2. Add the datasource

`Configuration → Data sources → Add data source → MDDB`. Fill in:

| Field | Example | Notes |
| --- | --- | --- |
| **MDDB URL** | `https://mddb.tradik.com` | Reachable from the Grafana server (proxy mode). |
| **Default collection** | `docs` | Used by queries that don't override it. |
| **Default language** | `en_US` | Only relevant if queries inherit it later. |
| **API key** | `vk_…` | Bearer token, stored encrypted in `secureJsonData`. |

Click **Save & test** — the plugin pings `/v1/stats` and reports success / auth failure / server error.

### 3. Build a panel

Drop a panel onto a dashboard, pick the new MDDB datasource, then pick a query type from the dropdown. Each type maps directly to an MDDB endpoint:

| Query type | MDDB endpoint | Best panel |
| --- | --- | --- |
| **Temporal histogram** | `POST /v1/temporal/histogram` | Time series |
| **Hot documents** | `POST /v1/temporal/hot` | Table / Bar chart |
| **Metadata aggregate** | `POST /v1/aggregate` | Pie / Bar / Time series (if buckets) |
| **Full-text search** | `POST /v1/fts` | Table |
| **Database stats** | `POST /v1/stats` | Stat / Table |

Dashboard time range is applied automatically as `from` / `to` (seconds since epoch).

## Query types

### Temporal histogram

Time-series of `create` / `update` / `access` events bucketed by `day` / `week` / `month`. Requires `MDDB_TEMPORAL=true` on the server — see [docs/TEMPORAL-TRACK.md](../../docs/TEMPORAL-TRACK.md).

Response shape (`{buckets: [{from, to, count}, …]}`) is converted to a 2-column DataFrame (`time`, `count`).

### Hot documents

Top-N most accessed documents in a collection. Returns `docId`, `accessCount`, `lastAccessAt`.

### Metadata aggregate

Calls `/v1/aggregate` for a chosen `facetKey`. If MDDB returns date-histogram `buckets`, the plugin emits a time-series DataFrame; otherwise it emits a `value` / `count` table.

### Full-text search

Runs `/v1/fts` and tabulates `key`, `lang`, `score`, joined `highlight`. Type the FTS query (supports all 7 modes — boolean, phrase, wildcard, proximity, range, fuzzy, simple) directly in the **Query** field. Supports template variables — `$var` is replaced before the request.

### Database stats

Pulls `/v1/stats` and emits one row per collection: `collection`, `documents`, `revisions`, `embeddings`. Independent of the time range. Useful for an at-a-glance "MDDB instance" dashboard tile.

## Configuration

| Field | jsonData key | Required | Description |
| --- | --- | --- | --- |
| MDDB URL | `url` | ✓ | Base URL, no trailing slash. Reached from Grafana server. |
| Default collection | `defaultCollection` | — | Used when a query omits `collection`. |
| Default language | `defaultLanguage` | — | Reserved for future per-language queries. Defaults to `en_US`. |
| API key | `secureJsonData.apiKey` | — | Bearer token. Encrypted at rest by Grafana. |

Template variables: `collection`, `query`, and `facetKey` are passed through `getTemplateSrv().replace()` before each request.

## Local development

```bash
# From the MDDB repo root:
make grafana-install      # npm install
make grafana-test         # jest
make grafana-coverage     # jest --coverage (≥90% enforced)
make grafana-build        # webpack production bundle into dist/
make grafana-check        # format + lint + tests + build

# Or from this directory:
cd integrations/grafana-datasource
make install build test
```

`make dev` runs webpack in watch mode against `dist/`. Bind-mount that into a local `grafana/grafana` container with `GF_PLUGINS_ALLOW_LOADING_UNSIGNED_PLUGINS=tradik-mddb-datasource` for a tight feedback loop.

## Tests

```bash
make grafana-coverage
```

Pure-logic Jest suites focus on the four modules that drive correctness:

- `__tests__/query.test.ts` — request builder, defaults, limit clamping, validation.
- `__tests__/transform.test.ts` — response → Grafana `DataFrame` for every query type.
- `__tests__/client.test.ts` — base URL handling, auth header, `MddbHttpError` mapping, `/testDatasource` probe classification.
- `__tests__/datasource.test.ts` — `DataSourceApi` lifecycle, template-variable interpolation, per-target dispatch, error propagation.

React components are intentionally excluded from the coverage gate — they're thin shells over `@grafana/ui` inputs and verified manually in Grafana.

## Known audit findings

`npm audit` reports four moderate findings here, all one issue: `react-router`
6.x, reached through `@grafana/ui` → `react-router-dom-v5-compat`
([GHSA-wrjc-x8rr-h8h6](https://github.com/advisories/GHSA-wrjc-x8rr-h8h6),
[GHSA-337j-9hxr-rhxg](https://github.com/advisories/GHSA-337j-9hxr-rhxg)).

None of it ships. `webpack.config.js` marks every `@grafana/*` package
external, and the built `dist/module.js` contains no react-router code at all:
at runtime the plugin uses the `@grafana/ui` of the Grafana it is loaded into,
so exposure depends on the operator's Grafana version, not on this plugin. The
SSR finding needs server-side rendering, which a datasource plugin never does.

**Do not run `npm audit fix --force`.** Its "fix" is to downgrade `@grafana/ui`
from 13.x to 11.2.10. Even the newest `@grafana/ui` (13.2.2 at the time of
writing) still depends on `react-router-dom-v5-compat ^6.26.1`, so there is no
forward fix to take yet; the finding clears when Grafana moves off it.

## Packaging & release

```bash
make build          # webpack production bundle to dist/
make package        # → tradik-mddb-datasource-0.1.0.zip
```

Distribution options:

1. **Grafana Cloud / Enterprise (signed)** — submit `dist/` to the [Grafana plugin signing service](https://grafana.com/docs/grafana/latest/developers/plugins/sign-a-plugin/). The plugin id `tradik-mddb-datasource` and `info.version` are already aligned with [`src/plugin.json`](src/plugin.json).
2. **Self-hosted / private** — drop `dist/` into `/var/lib/grafana/plugins/` (or the configured plugin path) and enable unsigned loading via `allow_loading_unsigned_plugins = tradik-mddb-datasource`.
3. **Docker** — `make docker` produces `tradik/mddb-grafana-datasource:<version>` with the plugin pre-baked into `grafana/grafana:13.0.1`.

Tag-driven releases follow the same convention as the GitHub Action: push `grafana-v<version>` to publish a GitHub Release with the zip attached.

## Architecture

```
Grafana panel
    │  React (QueryEditor / ConfigEditor)
    ▼
MddbDataSource  (DataSourceApi<MddbQuery, MddbDataSourceOptions>)
    │  buildRequest(target, range, defaults) → { path, body }
    │  client.post(path, body)
    │      ▼
    │  /api/datasources/proxy/uid/<uid>/<auth|noauth><path>
    │      ▼  ← Grafana pluginproxy substitutes {{ .JsonData.url }} and
    │            injects "Authorization: Bearer {{ .SecureJsonData.apiKey }}"
    │            (only on the "auth" route).
    │  ────▶ MDDB
    │  toDataFrame(target, payload)
    ▼
Grafana DataFrame  →  Time-series / Table / Stat panel
```

- `query.ts` builds the JSON body for each MDDB endpoint and validates inputs.
- `transform.ts` converts the response into a Grafana `DataFrame` (via `createDataFrame`) — time-series with `FieldType.time` for `temporal-histogram` and bucketed `aggregate`; tables otherwise.
- `client.ts` builds the `/api/datasources/proxy/uid/<uid>/<route>` URL and classifies the response for the `/testDatasource` probe; it never sees the API key itself.
- `datasource.ts` reads `secureJsonFields.apiKey` (boolean) to pick the `auth` vs `noauth` route, applies template variables only in `applyTemplateVariables()` (Grafana calls it exactly once per target — `query()` must not re-interpolate), and lives behind `module.ts`, which exposes the `DataSourcePlugin` Grafana picks up at load time.

## License

BSD-3-Clause — see [LICENSE](../../LICENSE) in the MDDB repo root.
