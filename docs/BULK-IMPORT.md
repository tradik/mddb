---
title: "Bulk Import Guide"
slug: "docs/bulk-import"
description: "Bulk-load folders of Markdown into MDDB: front-matter metadata extraction, recursive scanning, custom fields, dry runs and progress reporting."
status: publish
---

# Bulk Import Guide

## autoindex: one command (v2.18.8+)

`mddb-cli autoindex [path]` is the way to load a directory or a repository:

```bash
mddb-cli autoindex .                       # this repository, collection named after it
mddb-cli autoindex ~/notes -c notes        # a folder, an explicit collection
mddb-cli autoindex . --dry-run             # the plan: files, skipped files and why
mddb-cli autoindex . --prune               # also delete documents whose file is gone
mddb-cli autoindex . --exclude 'testdata/**' --max-file-size 2000000 --profile fast
mddb-cli autoindex docs --images          # also store the images Markdown files link to
```

| Step | What happens |
|---|---|
| Files | In a git repository, what git tracks or would track (`.gitignore` honoured); elsewhere everything except `node_modules`, `target`, `dist`, `build`, `vendor`, `.venv`, `__pycache__` and editor folders. `--include` / `--exclude` take globs (`dir/**` for a subtree). |
| Converted (v2.18.9+) | Files the server converts are sent as files (`format` + `contentBase64` on `/v1/ingest`) and stored as Markdown, through the converters `/v1/upload` uses. Recognised by content where the format has a signature — `%PDF` (PDF), `{\rtf` (RTF), a zip holding `word/document.xml` (DOCX), `xl/workbook.xml` (XLSX) or `ppt/presentation.xml` (PPTX), or an OpenDocument/EPUB `mimetype` (ODT, ODS, ODP, EPUB) — so a PDF named `.txt` is still a PDF; HTML (`.html`, `.htm`, `.xhtml`), TeX (`.tex`, `.latex`), CSV/TSV and mail (`.eml`, `.mbox`) by extension, so their markup does not reach the index. An email's convertible attachments and a mailbox's messages become documents of their own below the file's key, with `meta.parent`. The report lists each converted document with its confidence: `~ scan.pdf: converted from pdf (confidence 0.00: 3 of 3 pages have no extractable text ...)`; `--json` has them under `converted`. |
| Images (v2.18.9+) | With `--images`, each Markdown file is sent with the JPEG, PNG, GIF and WebP files its relative image links name (inside the indexed tree, at most 2 MB each; `images` on `/v1/ingest`), and `extractImages` is set: the server stores them, and the pictures of PDFs, as image documents below the file's key with `meta.parent`, `meta.altText` and `meta.caption`, which full-text search indexes. See [SEARCH.md](SEARCH.md#images--alt-text-captions-and-pictures-v2189). |
| Skipped | Other binary files (a NUL byte in the first 8 KiB, or a zip that is no document) and files over `--max-file-size` (default 1 MiB). `--dry-run` lists each with the reason. |
| Documents | Key = the file's path; `lang` from `--lang` (default `en`). Meta: `path`, `extension`, `kind` (`code`, `doc` for Markdown and converted files, `text`), `sourceFormat` for converted files, `language` (from the server's table, `GET /v1/code-languages`), `size`, `modified` (RFC 3339), and `gitCommit`, `gitBranch` in a repository. The server extracts `defines` / `uses` / `imports`. |
| Sending | Batches of 200 documents or 4 MiB (converted files counted at their base64 size) to `/v1/ingest`, with `skipDuplicates`: an unchanged file is counted `unchanged` and not rewritten, so a second run takes about a second. A converted file is compared by its Markdown, which the converters produce the same way every time. |
| Quality (v2.18.9+) | Every converted document carries `meta.conversionConfidence` (0–1) and `meta.conversionWarnings`; a scan without a text layer scores 0. List them with `"filter": {"conversionConfidence": {"lt": 0.5}}`; the search advisor run at the end warns when there are any. See [API.md](API.md#post-v1upload) for the heuristic. |
| C and C++ | Directories named `include` holding headers become the collection's `code.includePaths`, unless it already has some, so `#include <...>` resolves in the code graph. |
| After | The search advisor stores a retrieval profile for the collection (for code: hybrid search, graph mode, `format: passages`). |

Measured on this repository (2.18.8): 1455 files, 14 MB, indexed in 41 s on a
first run and checked in 0.5 s on a second with nothing changed.

The CLI walks the directory; the server never takes a filesystem path, so
there is no endpoint that could be pointed at the server's own disk. For
the same reason there is no MCP tool for it: an agent runs `mddb-cli
autoindex` like any other command.

## Overview (load-md-folder.sh, legacy)

`autoindex` replaces this script for new work. The `load-md-folder.sh` script imports markdown files from a folder into MDDB. It's perfect for migrating existing documentation, importing blog posts, or loading large collections of markdown content.

## Features

- **Automatic Key Generation** - Creates unique keys from filenames
- **Frontmatter Support** - Extracts YAML-style metadata from file headers
- **Recursive Scanning** - Process entire directory trees
- **Progress Tracking** - Real-time progress with statistics
- **Dry Run Mode** - Preview imports without making changes
- **Error Handling** - Graceful failure handling with detailed reporting
- **Metadata Enrichment** - Add custom metadata to all imported files
- **Multi-language Support** - Specify language code for all documents

## Installation

The script is located in the `scripts/` directory and requires:
- Bash shell
- `mddb-cli` command available in PATH
- Running MDDB server

```bash
# Make script executable (if not already)
chmod +x scripts/load-md-folder.sh
```

## Basic Usage

### Simple Import

Import all `.md` files from a folder:

```bash
./scripts/load-md-folder.sh ./docs blog
```

This will:
1. Scan `./docs` for `.md` files
2. Import them into the `blog` collection
3. Use default language `en_US`
4. Generate keys from filenames

### Recursive Import

Process all subfolders:

```bash
./scripts/load-md-folder.sh ./content articles --recursive
```

Or use the short form:

```bash
./scripts/load-md-folder.sh ./content articles -r
```

### Custom Language

Specify a different language code:

```bash
./scripts/load-md-folder.sh ./docs-pl blog --lang pl_PL
```

Short form:

```bash
./scripts/load-md-folder.sh ./docs-pl blog -l pl_PL
```

## Advanced Usage

### Adding Metadata

Add custom metadata to all imported files:

```bash
./scripts/load-md-folder.sh ./posts blog \
  --meta "author=John Doe" \
  --meta "status=published" \
  --meta "category=tutorial"
```

Short form:

```bash
./scripts/load-md-folder.sh ./posts blog \
  -m "author=John Doe" \
  -m "status=published"
```

### Dry Run

Preview what would be imported without making changes:

```bash
./scripts/load-md-folder.sh ./docs blog --dry-run
```

This shows:
- Which files would be imported
- Generated keys
- Extracted metadata
- Final metadata combination

### Verbose Output

See detailed information during import:

```bash
./scripts/load-md-folder.sh ./docs blog --verbose
```

Shows:
- Each file being processed
- Generated key for each file
- Metadata for each file
- Success/failure status

### Custom Server

Connect to a different MDDB server:

```bash
./scripts/load-md-folder.sh ./docs blog \
  --server http://production-server:11023
```

Or use environment variable:

```bash
MDDB_SERVER=http://production-server:11023 \
  ./scripts/load-md-folder.sh ./docs blog
```

### Batch Size

Control progress update frequency:

```bash
./scripts/load-md-folder.sh ./docs blog --batch-size 50
```

Default is 10 files per progress update.

## Sizing a batch

These limits apply when you send documents yourself, over `/v1/ingest`,
`/v1/add-batch` or `/v1/bulk-ingest-job` (#324).

**The body cap is for the whole request.** `MDDB_MAX_BODY_BYTES` (default
32 MB, [config.md](config.md)) limits the entire JSON body, so the whole
`documents` array counts against it, not each document. Keep a margin for
JSON escaping and metadata, for example 24 MB of content per request.

**Endpoints and what they accept:**

| Endpoint | Returns | `options.profile` (`fast`) | Notes |
|---|---|---|---|
| `POST /v1/ingest` | when written | yes | Synchronous. `options.skipEmbeddings` and the other ingest options apply. |
| `POST /v1/add-batch` | when written | no | Synchronous. Takes the documents only. |
| `POST /v1/bulk-ingest-job` | at once (202), with a job ID | no | Body shaped like `add-batch` plus `callbackUrl`. |

`profile: fast` turns off revisions and webhooks, skips duplicates and
indexes text only. It does not turn off embeddings: send
`skipEmbeddings: true` for that. For a large load you want to tune, use
`/v1/ingest`.

**Jobs run one at a time.** `/v1/bulk-ingest-job` hands each job to a single
FIFO worker, which writes it in chunks of 500 documents. Splitting a load into
many small jobs does not make them run in parallel; they queue. The queue
holds 64 jobs, and a job's documents are kept in memory until written. A
restart marks jobs in flight as failed and their documents are lost: resubmit
them, or use the synchronous `/v1/ingest` when you need to know each batch is
stored.

**Recommended batch for source code:** 500 documents or 24 MB, whichever
comes first. 500 matches the job chunk size. Source files are larger than
prose: on a C codebase the average file was about 28 KB, so 500 files is
about 14 MB.

**Throughput, measured.** 889 C files and 16,053 functions split from them
(16,942 documents, 45.6 MB) were loaded with `/v1/ingest` in 500-document
requests, with `profile: fast` and `skipEmbeddings: true`. The load took
**18 seconds**, about 940 documents and 2.5 MB of content per second, on
tmpfs. 2.17.2 (#317) removed the slow first batch into an empty collection
and about halved the time of a load into a growing one. Expect a disk with
fsync to be slower than tmpfs. The database file grew to 908 MB, about 20×
the content, because code is indexed whole, in parts and with positions. See
[DEPLOYMENT.md](DEPLOYMENT.md#large-databases-startup-time-and-memory) for
what that means at startup.

## Watching a job finish

An async import returns a job ID immediately. Rather than polling
`GET /v1/bulk/jobs/{id}` in a loop, subscribe to the job's event stream:

```bash
curl -N "http://localhost:11023/v1/events?job=bj_1a2b3c"
```

```
event: job.started
data: {"event":"job.started","timestamp":1711324801,"job":{"id":"bj_1a2b3c","status":"processing","total":5000,"processed":0,...}}

event: job.progress
data: {"event":"job.progress","timestamp":1711324805,"job":{"id":"bj_1a2b3c","status":"processing","total":5000,"processed":2500,...}}

event: job.completed
data: {"event":"job.completed","timestamp":1711324812,"job":{"id":"bj_1a2b3c","status":"completed","total":5000,"processed":5000,"added":5000,...}}
```

The `job` payload is the same record the status endpoint returns, so code that
already reads counters from `GET` needs no second shape. `job.progress` arrives
at most once per second per job; the start and terminal events always arrive.

A connection filtered to a job receives that job's events only — no `doc.*`
traffic from the import itself. See [SSE.md](SSE.md) for authentication, per-IP
limits and reconnection.

## Frontmatter Support

The script automatically extracts metadata from YAML-style frontmatter:

```markdown
---
title: Getting Started
author: John Doe
tags: tutorial, beginner
category: documentation
date: 2024-01-15
---

# Getting Started

Your content here...
```

This frontmatter will be converted to metadata:
- `title=Getting Started`
- `author=John Doe`
- `tags=tutorial, beginner`
- `category=documentation`
- `date=2024-01-15`

### Frontmatter Format

Supported format:
```yaml
---
key: value
another_key: another value
tags: value1, value2
---
```

Requirements:
- Must start with `---` on first line
- Must end with `---` on its own line
- Use `key: value` format
- Values can contain spaces (quotes optional)

## Key Generation

Keys are automatically generated from filenames:

| Filename | Generated Key |
|----------|---------------|
| `Getting Started.md` | `getting-started` |
| `API_Reference.md` | `api-reference` |
| `2024-01-15-blog-post.md` | `2024-01-15-blog-post` |
| `My Document (v2).md` | `my-document-v2` |

Rules:
- Convert to lowercase
- Replace spaces and special characters with hyphens
- Remove consecutive hyphens
- Trim leading/trailing hyphens

## Metadata Combination

Metadata is combined from multiple sources:

1. **Automatic metadata**:
   - `source=folder-import`
   - `filename=original-filename.md`

2. **Frontmatter metadata** (extracted from file)

3. **Custom metadata** (from `--meta` flags)

Example:
```bash
# File: tutorial.md with frontmatter:
---
author: Jane
category: tutorial
---

# Command:
./scripts/load-md-folder.sh ./docs blog -m "status=published"

# Final metadata:
source=folder-import,filename=tutorial.md,author=Jane,category=tutorial,status=published
```

## Examples

### Migrate Documentation

```bash
# Import entire docs folder with recursive scanning
./scripts/load-md-folder.sh ./docs documentation \
  --recursive \
  --meta "version=2.0" \
  --meta "status=published" \
  --verbose
```

### Import Blog Posts

```bash
# Import blog posts with author metadata
./scripts/load-md-folder.sh ./blog-posts blog \
  --lang en_US \
  --meta "author=John Doe" \
  --meta "type=blog-post"
```

### Multi-language Content

```bash
# Import English version
./scripts/load-md-folder.sh ./content/en articles -l en_US -r

# Import Polish version
./scripts/load-md-folder.sh ./content/pl articles -l pl_PL -r

# Import German version
./scripts/load-md-folder.sh ./content/de articles -l de_DE -r
```

### Preview Before Import

```bash
# First, do a dry run
./scripts/load-md-folder.sh ./docs blog --dry-run

# If everything looks good, run for real
./scripts/load-md-folder.sh ./docs blog
```

### Large Import with Progress

```bash
# Import large folder with progress updates every 100 files
./scripts/load-md-folder.sh ./large-docs blog \
  --recursive \
  --batch-size 100 \
  --verbose
```

## Output

### Progress Display

```
════════════════════════════════════════════════
  MDDB Folder Loader
════════════════════════════════════════════════

Checking server connectivity...
✓ Server is running

Configuration:
  Folder:     ./docs
  Collection: blog
  Language:   en_US
  Server:     http://localhost:11023
  Recursive:  true

Scanning for markdown files...
Found 150 markdown file(s)

════════════════════════════════════════════════
  Loading Files
════════════════════════════════════════════════

Progress: [##########################            ] 52% (78/150 files)
```

### Summary

```
════════════════════════════════════════════════
  Summary
════════════════════════════════════════════════

Results:
  Total files:    150
  Successful:     148
  Failed:         2
  Duration:       45s
  Throughput:     3.29 files/sec

✓ Import completed with some failures
```

## Error Handling

### Common Errors

**Server not running:**
```
✗ Cannot connect to MDDB server at http://localhost:11023
  Make sure the server is running
```

**Folder not found:**
```
Error: Folder does not exist: ./nonexistent
```

**No markdown files:**
```
No markdown files found in ./empty-folder
```

### Failed Imports

If some files fail to import:
- The script continues processing remaining files
- Failed files are counted in the summary
- Exit code is 1 (failure) if any imports failed
- Exit code is 0 (success) if all imports succeeded

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `MDDB_SERVER` | Server URL | `http://localhost:11023` |
| `MDDB_CLI` | CLI command path | `mddb-cli` |

Example:
```bash
export MDDB_SERVER=http://production:11023
export MDDB_CLI=/usr/local/bin/mddb-cli

./scripts/load-md-folder.sh ./docs blog
```

## Performance Tips

1. **Batch Size**: Increase for large imports to reduce output
   ```bash
   ./scripts/load-md-folder.sh ./docs blog -b 100
   ```

2. **Disable Verbose**: For faster imports
   ```bash
   ./scripts/load-md-folder.sh ./docs blog
   ```

3. **Use Extreme Mode**: Enable on server for better performance
   ```bash
   MDDB_EXTREME=true mddbd
   ```

4. **Local Server**: Import to local server, then backup/restore to production

## Troubleshooting

### Script not executable

```bash
chmod +x scripts/load-md-folder.sh
```

### CLI not found

```bash
# Install CLI
make build-cli
make install-all

# Or specify full path
MDDB_CLI=/path/to/mddb-cli ./scripts/load-md-folder.sh ./docs blog
```

### Server connection refused

```bash
# Check if server is running
mddb-cli stats

# Start server
make docker-up
# or
make run
```

### Frontmatter not parsed

Ensure frontmatter format:
- Starts with `---` on line 1
- Ends with `---` on its own line
- Uses `key: value` format

## Integration with CI/CD

### GitHub Actions

```yaml
name: Import Documentation

on:
  push:
    paths:
      - 'docs/**/*.md'

jobs:
  import:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v2
      
      - name: Install MDDB CLI
        run: |
          wget https://github.com/tradik/mddb/releases/latest/download/mddb-cli-latest-linux-amd64.tar.gz
          tar xzf mddb-cli-latest-linux-amd64.tar.gz
          sudo mv mddb-cli /usr/local/bin/
      
      - name: Import Documentation
        env:
          MDDB_SERVER: ${{ secrets.MDDB_SERVER }}
        run: |
          ./scripts/load-md-folder.sh ./docs documentation -r -m "version=${{ github.sha }}"
```

### GitLab CI

```yaml
import-docs:
  stage: deploy
  script:
    - chmod +x scripts/load-md-folder.sh
    - ./scripts/load-md-folder.sh ./docs documentation -r
  only:
    - main
```

## Best Practices

1. **Always dry run first** on production data
2. **Use meaningful collection names** that reflect content type
3. **Add version metadata** for tracking changes
4. **Use recursive mode** for organized folder structures
5. **Include frontmatter** in markdown files for rich metadata
6. **Test with small batches** before large imports
7. **Monitor server resources** during large imports
8. **Backup database** before major imports

## Async Bulk Ingest with Job Tracking

For imports of tens of thousands of documents where you don't want the HTTP
request to block, use the async job API instead of `/v1/add-batch`.

### Submit a job

```bash
curl -X POST http://localhost:11023/v1/bulk-ingest-job \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "articles",
    "documents": [ ... up to N documents ... ],
    "callbackUrl": "https://example.com/webhook"
  }'
```

Returns HTTP 202 with the job record:

```json
{
  "id": "bulk_1776539903516445000_849876",
  "collection": "articles",
  "status": "pending",
  "total": 12500,
  "submittedAt": 1713432000
}
```

### Poll for progress

```bash
curl http://localhost:11023/v1/bulk-ingest-job/bulk_1776539903516445000_849876
```

Response fields:

| Field | Description |
|-------|-------------|
| `status` | `pending`, `processing`, `completed`, `failed`, or `cancelled` |
| `total` | Documents submitted |
| `processed` | Documents processed so far |
| `added` / `updated` / `failed` | Per-document outcome counters |
| `errors` | Up to 50 error messages captured during processing |
| `startedAt` / `completedAt` | Unix timestamps |

### List all jobs

```bash
curl http://localhost:11023/v1/bulk-ingest-jobs?collection=articles
```

### Cancel a pending job

```bash
curl -X DELETE http://localhost:11023/v1/bulk-ingest-job/bulk_1776539903516445000_849876
```

In-flight jobs (`processing`) cannot be cancelled — they run to completion.

### How it works

- **Single worker, FIFO** — BoltDB writes are serialised, so the job worker
  runs one job at a time. Multiple submits queue up.
- **Chunked commits** — documents are processed in sub-batches of 500 so a
  long write transaction never blocks readers for more than a few seconds.
- **In-memory payload, persistent status** — the document list lives in the
  queue; if the server restarts mid-job the status record flips to `failed`.
- **Callback webhook** — set `callbackUrl` to receive a POST with the final
  job record when processing completes (`X-MDDB-Event: bulk_ingest.completed`).

### When to use async vs `/v1/add-batch`

| Use `/v1/add-batch` | Use `/v1/bulk-ingest-job` |
|---------------------|---------------------------|
| Up to ~1000 docs per request | Thousands of docs, long-running |
| You need the response inline | You can poll or accept a callback |
| CLI one-shot imports | Pipeline-driven or UI-driven imports |

## Keeping a site or bucket in sync (v2.18.10+)

An import is a copy of the moment. To keep a collection in step with a web
site or an S3-compatible bucket, give a cron automation rule a `sync`
action instead: on its schedule the server lists a sitemap, a list of URLs
or the objects under a prefix, fetches only what changed (ETag /
Last-Modified, the object's ETag, else a sha256 of the content), writes it
through the `/v1/upload` converters, and — with `prune: true` — deletes the
documents whose source is gone.

```bash
curl -X POST http://localhost:11023/v1/automation \
  -H "Content-Type: application/json" \
  -d '{"type": "cron", "name": "handbook", "schedule": "0 * * * *", "enabled": true,
       "sync": {"source": "s3", "bucket": "handbook", "prefix": "published/",
                "collection": "handbook", "lang": "en", "prune": true,
                "storageConfig": {"endpoint": "minio:9000", "accessKey": "...", "secretKey": "..."}}}'
```

Fields, bounds, keys and safety rules: [AUTOMATIONS.md](AUTOMATIONS.md#synced-sources).
Git repositories stay on the client side (`mddb-cli autoindex`): the server
never runs `git`.

## See Also

- [API Documentation](API.md)
- [Examples](EXAMPLES.md)
- [Installation & CLI](INSTALLATION.md)
- [Deployment Guide](DEPLOYMENT.md)
