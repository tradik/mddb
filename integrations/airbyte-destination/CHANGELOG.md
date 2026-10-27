# Changelog — destination-mddb

## [Unreleased]

### Security
- The image installs from `requirements.lock`, every package pinned with its
  hashes, wheels only (`--only-binary :all: --require-hashes`). It installed
  `requirements.txt` ranges, so a build could pick up any new release, and an
  sdist's setup script could run during it. `make lock` regenerates the lock.

## [0.1.1] — 2026-05-17

### Changed
- Bumped base image to `python:3.13-slim` (was `3.11-slim`).
- Bumped `airbyte-cdk` constraint to `>=7.0.0,<8.0.0` (was `>=0.58.0,<7.0.0`); the connector already runs against the modern CDK via the `model_validate` / dataclass fallbacks in `destination.py::spec`.
- Bumped `requests` minimum to `>=2.34.2` (was `>=2.31.0`).
- CI matrix bumped to Python `3.12` and `3.13` (coverage artifact now uploaded from the `3.13` job).

## [0.1.0] — 2026-05-11

### Added
- First version of the custom destination for MDDB (https://github.com/tradik/mddb).
- `spec.json`: `mddbUrl` (default `https://mddb.tradik.com`), `apiKey` (bearer, secret), `keyField` (default `id`), `language` (default `en_US`), `batchSize` (default 100), `timeoutSeconds`, `verifySsl`.
- Sync modes: `append`, `append_dedup` — both upsert by key (matches MDDB `/v1/add` semantics).
- `check`: probes via `POST /v1/search` with `limit=1`. Accepts 2xx/404/405, raises `PermissionError` on 401/403 and `RuntimeError` on 5xx.
- `write`: maps Airbyte records to MDDB documents — `collection = streamName`, `key = record[keyField]` with SHA-1 fallback, `meta = flatten(record)` to `map<string,[]string>`, `contentMd = record as JSON inside a fenced code block`.
- Buffering with configurable `batchSize`, flush on every `AirbyteMessage(STATE)` and at end of stream.
- HTTP retry (urllib3 `Retry`): 3× backoff on 429/5xx.
- Dockerfile: `python:3.11-slim`, non-root user `airbyte:1000`, entrypoint `python /airbyte/integration_code/main.py`.
- Icon `mddb.svg` (SVG wrapper around the 68×68 PNG from `versions/web/icons/mddb.png`).
- 40 unit tests, 97% coverage (`pytest --cov`).
- README with badges (Airbyte CDK / MDDB / Python / Coverage / License), full spec documentation, and Airbyte UI registration instructions.
- `metadata.yaml` with `releaseStage: alpha`, `supportLevel: community`, stable `definitionId` UUID.

### CI/CD
- GitHub Actions workflow [.github/workflows/airbyte-destination.yml](../../.github/workflows/airbyte-destination.yml) scoped by `paths:` to this integration. On every PR and push touching `integrations/airbyte-destination/**`:
  - **`test`** — runs `pytest unit_tests/ --cov=destination_mddb` on Python 3.11 and 3.12 with `--cov-fail-under=90`. Uploads `coverage.xml` artifact (3.11).
  - **`smoke-spec`** — builds the image with Buildx (gha cache), runs `spec` (asserts `mddbUrl` + `apiKey` present), runs `check` against `https://mddb.tradik.com` (warning, not hard-fail, so PRs don't break when public MDDB is down).
- **`build-and-push`** runs only on `push` to `main` (gated by `if: github.event_name == 'push' && github.ref == 'refs/heads/main'`). Builds multi-arch `linux/amd64,linux/arm64`, reads version from `metadata.yaml` `dockerImageTag`, pushes to both Docker Hub (`tradik/airbyte-destination-mddb:<tag>` + `:latest`) and GHCR (`ghcr.io/<owner>/airbyte-destination-mddb:<tag>` + `:latest`). Generates SLSA build-provenance attestation on the GHCR digest.

### Notes
- `overwrite` mode is not declared in the spec, but if forced (e.g. by the source side) the connector logs a WARNING and behaves like append-upsert. Orphans (keys that disappeared from the source) are not deleted — MDDB has no batch-delete-by-collection.
- `contentMd` contains an `<!-- emittedAt=… -->` HTML comment with the Airbyte timestamp, which lets you treat it as a "version" marker without polluting `meta`.
