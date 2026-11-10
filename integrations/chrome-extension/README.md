# MDDB Browser — Chrome Extension

[![Chrome](https://img.shields.io/badge/Chrome-Manifest%20V3-4285F4?logo=googlechrome&logoColor=white)](https://developer.chrome.com/docs/extensions/mv3/)
[![Node](https://img.shields.io/badge/Node-24-3C873A?logo=node.js&logoColor=white)](https://nodejs.org/)
[![TypeScript](https://img.shields.io/badge/TypeScript-6.0-3178C6?logo=typescript&logoColor=white)](https://www.typescriptlang.org/)
[![MDDB](https://img.shields.io/badge/MDDB-2.9.5%2B-1f7a8c)](https://github.com/tradik/mddb)
[![Coverage](https://img.shields.io/badge/coverage-99%25-brightgreen)](#tests)
[![License](https://img.shields.io/badge/license-BSD--3--Clause-green)](#license)

A small Chrome (Manifest V3) extension that turns the browser toolbar into a live status
panel for an [MDDB](https://github.com/tradik/mddb) server. Configure the server URL and
optional API key once, then see live document counts, the current collection breakdown,
and a one-click link to the MDDB admin panel.

The extension stores all configuration locally via `chrome.storage.local`, talks only to
the MDDB server you configure, and ships with no telemetry, analytics, or third-party
calls. See the bundled [privacy policy](public/privacy.html) and
[terms of use](public/terms.html) — canonical copies live at
[tradik.com/privacy](https://tradik.com/privacy) and
[tradik.com/terms](https://tradik.com/terms).

## Contents

- [What it does](#what-it-does)
- [Install](#install)
- [Configuration](#configuration)
- [How it works](#how-it-works)
- [Local development](#local-development)
- [Tests](#tests)
- [CI / CD](#ci--cd)
- [Release](#release)
- [Architecture](#architecture)
- [Privacy & permissions](#privacy--permissions)
- [Changelog](CHANGELOG.md)
- [License](#license)

## What it does

| | |
| --- | --- |
| **Popup** | Live document, revision, and collection counts; mode + uptime; the top 5 collections by document count; a button to open the MDDB admin panel; a refresh button. |
| **Badge** | Toolbar badge shows the total document count (`1.2k`, `15k`, `99k+`); colour-coded — teal when healthy, red on error, no badge when unconfigured. |
| **Options** | Server URL, optional API key, optional admin-panel URL, and a refresh interval (30 – 3600 s, or `0` to disable). "Test connection" pings `GET /v1/health` and surfaces auth vs. server vs. network errors clearly. |
| **Background** | A single MV3 service worker that re-queries `GET /v1/stats` on a `chrome.alarms` schedule, caches the result, and updates the badge. |

## Install

### Pre-built zip (latest release)

1. Download `mddb-browser-<version>.zip` from
   [github.com/tradik/mddb/releases](https://github.com/tradik/mddb/releases?q=chrome-ext-v).
2. Unzip into a folder.
3. Open `chrome://extensions`, enable **Developer mode**, click **Load unpacked**, and
   pick the unzipped folder.

### Build it yourself

```bash
cd integrations/chrome-extension
make install
make package        # → dist/mddb-browser-<version>.zip
# OR
make build          # unpacked output in ./build (faster for iteration)
```

Then **Load unpacked** the `build/` directory in `chrome://extensions`.

## Configuration

Open the extension's options page (right-click the toolbar icon → **Options**, or the
**Settings** link in the popup footer) and fill:

| Field | Required | Notes |
| --- | --- | --- |
| **MDDB server URL** | yes | Base URL, e.g. `https://mddb.tradik.com` or `http://localhost:11023`. Trailing slashes are normalised. |
| **API key** | no | Sent as `X-API-Key`. Leave blank for servers that allow anonymous reads. |
| **Admin panel URL** | no | Overrides the panel link. Defaults to `<server-origin>:3000`. |
| **Background refresh (seconds)** | yes | `0` disables, otherwise clamped to `[30, 3600]`. |

When you click **Save**, Chrome prompts to grant host permission for the server's origin
only — no broad host access is ever requested up-front.

## How it works

- **Popup** (`popup.html` / `popup.js`) is a static page that reads the cached
  `/v1/stats` result from `chrome.storage.local` and asks the background worker for a
  fresh refresh if no cache exists.
- **Background service worker** (`background.js`) registers a periodic
  `chrome.alarms` job, listens for storage changes (re-poll on save), and exposes a
  `mddb:refresh` message so the popup's manual refresh button works.
- **Options page** (`options.html` / `options.js`) edits a single `settings` object in
  `chrome.storage.local` and lazily asks for the runtime host permission for the configured
  origin.

The MDDB API client only ever issues `GET /v1/health` and `GET /v1/stats`, with an
`X-API-Key` header when configured. `credentials: 'omit'` is set on every request so
browser cookies are never forwarded.

## Local development

Requires Node ≥ 24 and npm.

```bash
make install
make test            # jest (jsdom)
make test-coverage   # enforces ≥90% lines / branches / functions / statements
make lint            # eslint
make format          # prettier --write
make build           # esbuild → ./build
make package         # → dist/mddb-browser-<version>.zip
make check           # the full CI pipeline
```

The `make build` output is a Chrome-loadable unpacked extension — point
`chrome://extensions` → **Load unpacked** → `build/` and reload after each rebuild.

## Tests

98 unit tests across the API client, URL/format helpers, storage layer, background
service worker, popup rendering, and options page. The Jest config enforces
**≥ 90 %** branches/functions/lines/statements; the bundle currently sits at
**99 % stmts / 94 % branches / 100 % functions / 99 % lines**.

The DOM-touching tests run under `jest-environment-jsdom` with a tiny `chrome.*` stub
defined in [`__tests__/setup.ts`](__tests__/setup.ts); the popup/options entrypoints
(`src/popup-entry.ts`, `src/options-entry.ts`) are excluded from coverage because they
are five-line bootstrap wrappers.

## CI / CD

The workflow [`.github/workflows/chrome-extension.yml`](../../.github/workflows/chrome-extension.yml)
runs on every push and PR touching this directory:

1. **format check** (`prettier --check`)
2. **lint** (ESLint)
3. **tests with coverage** on a Node 22 & 24 matrix, 90 % threshold enforced
4. **security audit** (`npm audit --omit=dev --audit-level=high`)
5. **build** (esbuild bundle into `build/`)
6. **package** (zip into `dist/`)
7. **smoke** (re-unzips the artefact and validates the manifest)

Coverage is uploaded as a workflow artefact. The Node 24 leg's `dist/*.zip` is also
uploaded so reviewers can install the candidate build straight from the workflow run.

## Release

Pushing a `chrome-ext-v<version>` tag (e.g. `chrome-ext-v0.1.1`) triggers the `release`
job which:

1. Verifies `package.json` and `manifest.json` versions match the tag.
2. Rebuilds the bundle and packages a clean zip.
3. Publishes a GitHub Release named `MDDB Browser Extension <version>` with the zip
   attached and the CHANGELOG entry linked.
4. Force-moves the floating `chrome-ext-v<major>` and `chrome-ext-v<major>.<minor>` tags so
   Chrome Web Store metadata can keep pointing to the latest in a minor series.

Releases are cut from `main`. Bump `version` in both `package.json` and
[`public/manifest.json`](public/manifest.json) (the build script keeps them in sync from
`package.json`) and add a CHANGELOG entry before tagging.

## Architecture

```
toolbar icon ──► popup.html ──► popup.js ──► chrome.storage.local (cached stats)
                                       │
                                       └──► chrome.runtime.sendMessage('mddb:refresh')
                                                          │
                                                          ▼
chrome.alarms ───► background.js (service worker) ──► GET /v1/stats ──► MDDB server
                                       │
                                       └──► chrome.action.setBadgeText(...)
options icon ──► options.html ──► options.js ──► chrome.storage.local (settings)
                                       │
                                       └──► chrome.permissions.request({ origins })
```

## Privacy & permissions

The extension declares the minimum-required permissions:

| Permission | Why |
| --- | --- |
| `storage` | Local-only settings + stats cache. |
| `alarms` | Schedule the background refresh of the badge counter. |
| `optional_host_permissions: http://*/*, https://*/*` | Requested at save time only for the origin of the server you configure; no broad access is ever granted up front. |

No analytics, no third-party calls, no remote code, no content scripts — see the bundled
[privacy policy](public/privacy.html) for the full statement, and the canonical Tradik
policy at [tradik.com/privacy](https://tradik.com/privacy).

## License

BSD 3-Clause — see [LICENSE](../LICENSE) for the integrations. The MDDB server itself is under the Business Source License 1.1 ([LICENSING.md](../../LICENSING.md)).
