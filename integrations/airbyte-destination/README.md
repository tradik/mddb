# Airbyte Destination — MDDB

[![Airbyte CDK](https://img.shields.io/badge/Airbyte%20CDK-python-615EFF?logo=airbyte&logoColor=white)](https://docs.airbyte.com/connector-development/cdk-python/)
[![MDDB](https://img.shields.io/badge/MDDB-2.9.5%2B-1f7a8c)](https://github.com/tradik/mddb)
[![Python](https://img.shields.io/badge/Python-3.13-3776AB?logo=python&logoColor=white)](https://www.python.org/)
[![Coverage](https://img.shields.io/badge/coverage-97%25-brightgreen)](#tests)
[![License](https://img.shields.io/badge/license-BSD--3--Clause-green)](#license)

Custom Airbyte destination that ships records to [MDDB](https://github.com/tradik/mddb). Each Airbyte stream is mapped to its own MDDB collection; every record becomes one document upserted via `POST /v1/add`.

Published as the Docker image `tradik/airbyte-destination-mddb`. Airbyte registers it as a "custom connector" by Docker image name + tag — no PyPI publish required.

## Contents

- [Configuration (spec)](#configuration-spec)
- [Record mapping](#record-mapping)
- [Sync modes](#sync-modes)
- [Local development](#local-development)
- [Registering in the Airbyte UI](#registering-in-the-airbyte-ui)
- [Tests](#tests)
- [Known audit findings](#known-audit-findings)
- [Release](#release)
- [Architecture](#architecture)
- [Changelog](CHANGELOG.md)

## Configuration (spec)

| Field | Default | Description |
| --- | --- | --- |
| `mddbUrl` | `https://mddb.tradik.com` | Base URL of the MDDB instance, without trailing `/`. |
| `apiKey` | _(empty)_ | Bearer token (typically `vk_…`). Empty = MDDB instance without auth. |
| `keyField` | `id` | Record field used as the MDDB document key. If missing/null, the destination falls back to a SHA-1 hash of the record. |
| `language` | `en_US` | Locale stored on every document. |
| `batchSize` | `100` | Number of records buffered before a flush. |
| `timeoutSeconds` | `30` | HTTP timeout per request. |
| `verifySsl` | `true` | Disable only for self-signed dev instances. |

## Record mapping

An Airbyte `users` stream with record:

```json
{ "id": "u-1", "name": "Alice", "email": "a@b.c", "active": true }
```

→ becomes an MDDB document:

```json
{
  "collection": "users",
  "key": "u-1",
  "lang": "en_US",
  "meta": {
    "id": ["u-1"],
    "name": ["Alice"],
    "email": ["a@b.c"],
    "active": ["true"]
  },
  "contentMd": "<!-- emittedAt=… -->\n```json\n{ \"id\": \"u-1\", … }\n```\n"
}
```

- `meta` flattens record values into `map<string,[]string>` (the native MDDB schema). Lists pass through unchanged; dicts are serialised as a JSON string.
- `contentMd` carries the full record inside a fenced JSON code block — fully indexed by MDDB's BM25/TF-IDF and vector search.

## Sync modes

| Mode | Supported | Notes |
| --- | --- | --- |
| `append` | ✅ | Every record is upserted by `key`. |
| `append_dedup` | ✅ | Same as above (MDDB upserts by key natively). |
| `overwrite` | ⚠️ | Not declared in the spec. If forced, the connector behaves like append and logs a WARNING. Orphans (keys that vanished from the source) are **not deleted** — MDDB has no batch-delete-by-collection. |

## Local development

```bash
# From the MDDB repo root:
make airbyte-build              # docker build
make airbyte-test               # pytest + coverage
make airbyte-spec               # docker run … spec | json.tool
make airbyte-check URL=https://mddb.tradik.com  # smoke test

# Or from this directory:
cd integrations/airbyte-destination
make build test spec
make check URL=https://mddb.tradik.com
```

## Registering in the Airbyte UI

The connector is "custom" — Airbyte runs it from a Docker image. With a local Airbyte OSS instance:

1. **Settings → Destinations → ⊕ New connector**.
2. Fill in:
   - **Connector display name:** `MDDB`
   - **Docker repository name:** `tradik/airbyte-destination-mddb`
   - **Docker image tag:** `0.1.1` (or `dev` if you built locally)
   - **Connector documentation URL:** `https://github.com/tradik/mddb/tree/main/integrations/airbyte-destination`
3. **Add**. Airbyte runs `spec` in a fresh container and renders the configuration form.
4. **Destinations → ⊕ New destination → MDDB** → enter `mddbUrl` + optional `apiKey` → **Set up destination**. Airbyte will execute `check` and should report `SUCCEEDED`.

The image must be reachable from the Airbyte host's Docker daemon. A locally built `tradik/airbyte-destination-mddb:0.1.1` is visible immediately. After a release it lives on both Docker Hub (`tradik/airbyte-destination-mddb`) and GHCR (`ghcr.io/tradik/airbyte-destination-mddb`) — multi-arch (`linux/amd64,linux/arm64`) with SLSA build-provenance attestation on the GHCR digest. See [Release](#release).

## Tests

```bash
make airbyte-test
```

40 unit tests, ~97% coverage:

- `unit_tests/test_client.py` — record→document mapping, key hashing, MDDB HTTP retries, mocked `ping`/`addDocument`/`addBatch`.
- `unit_tests/test_destination.py` — `spec`, `check` (success / auth failure / connection failure), `write` (flush on STATE, flush on batch size, skipping unconfigured streams, overwrite warning, propagation of `keyField`/`language`).

## Known audit findings

`pip-audit -r requirements.txt` reports 55 findings in two packages: `nltk`
3.9.4 (53) and `setuptools` 80.10.2 (2). Both are pinned by `airbyte-cdk`
itself — `nltk==3.9.4` and `setuptools<81` in every release up to 7.30.0 — so
neither can be raised here without breaking the CDK's declared constraints.

This connector does not reach them. Its only import from the CDK is
`airbyte_cdk.destinations.Destination`; nothing here imports `nltk`,
`setuptools` or `pkg_resources`, or the CDK's file-based and document-parsing
modules that use them. Upstream tracks the pin as
[airbytehq/airbyte-python-cdk#1146](https://github.com/airbytehq/airbyte-python-cdk/issues/1146);
the findings clear with the CDK release that lifts it.

## CI/CD

Workflow: [`.github/workflows/airbyte-destination.yml`](../../.github/workflows/airbyte-destination.yml). Scoped by `paths:` so it only runs when files under `integrations/airbyte-destination/**` change.

| Job | When | What |
| --- | --- | --- |
| `test` | every PR + push (matrix: Python 3.12, 3.13) | `pytest --cov --cov-fail-under=90`, uploads `coverage.xml`. |
| `smoke-spec` | every PR + push | Build image with Buildx + gha cache, run `spec` (assert `mddbUrl`/`apiKey` present), run `check` against `https://mddb.tradik.com`. |
| `build-and-push` | **only push to `main`** | Multi-arch `linux/amd64,linux/arm64` build, push to Docker Hub and GHCR, SLSA build-provenance attestation. Tag comes from `metadata.yaml`'s `dockerImageTag`. |

Secrets used: `DOCKER_HUB_TOKEN` (existing) + auto-provided `GITHUB_TOKEN`.

## Release

The image is published automatically by CI when a commit lands on `main`. The version comes from `metadata.yaml`'s `dockerImageTag`, so:

```bash
# 1. Bump version in the connector
$EDITOR integrations/airbyte-destination/CHANGELOG.md
$EDITOR integrations/airbyte-destination/metadata.yaml       # dockerImageTag: 0.2.0
$EDITOR integrations/airbyte-destination/Dockerfile          # LABEL io.airbyte.version=0.2.0

# 2. Open PR -> CI runs tests + smoke spec
# 3. Merge to main -> CI builds & pushes:
#    tradik/airbyte-destination-mddb:0.2.0 + :latest
#    ghcr.io/tradik/airbyte-destination-mddb:0.2.0 + :latest

# 4. Optional: cut a per-integration git tag for the changelog history
git tag -a airbyte-destination-v0.2.0 -m "destination-mddb 0.2.0"
git push origin airbyte-destination-v0.2.0
```

## Architecture

```
Airbyte source ─► Airbyte worker ─► (docker run) ─► destination-mddb container
                                                       │
                                                       │  per record:
                                                       │  buildDocument(record)
                                                       │  client.addDocument(doc)
                                                       ▼
                                              POST https://mddb.tradik.com/v1/add
                                              Authorization: Bearer vk_…
```

The connector reads `AirbyteMessage`s from stdin, emits responses on stdout, and is built on the `airbyte-cdk` `Destination` base class. It implements only the three operations Airbyte requires: `spec`, `check`, `write`.

## License

BSD-3-Clause — see [LICENSE](../../LICENSE) in the MDDB repo root.
