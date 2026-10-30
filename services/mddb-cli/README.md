# mddb-cli - MDDB Command-Line Client

A command-line client for MDDB (Markdown Database), providing an interface similar to `mysql-client` for managing markdown documents.

> Built on the shared Go SDK [`clients/go/mddb`](../../clients/go/mddb/) (`github.com/tradik/mddb/clients/go/mddb/v2`) — the CLI no longer carries its own HTTP client. Reuse the same SDK in your own Go programs.

## Installation

### Build from Source

```bash
# From project root
make build-cli

# Install to system
make install-all
```

### Docker

The image is built from the repository root, because the CLI uses the Go
client from `clients/go/mddb`:

```bash
docker build -f services/mddb-cli/Dockerfile -t mddb-cli .
docker run --rm -e MDDB_SERVER=http://host.docker.internal:11023 mddb-cli stats
```

### Manual Installation

```bash
cd services/mddb-cli
go mod tidy
go build -o mddb-cli .
sudo cp mddb-cli /usr/local/bin/
sudo cp mddb-cli.1 /usr/local/share/man/man1/
```

## Quick Start

```bash
# Index this repository (or any directory) in one command
mddb-cli autoindex .

# Add a document
echo "# Hello World" | mddb-cli add blog hello en_US

# Get a document
mddb-cli get blog hello en_US

# Search documents
mddb-cli search blog

# Create backup
mddb-cli backup
```

## Usage

### Global Options

```bash
-s, --server URL     MDDB server URL (default: http://localhost:11023)
-j, --json           Output raw JSON
-v, --verbose        Verbose output
-h, --help           Show help
--version            Show version
```

### Commands

#### reference - Index other projects' code to learn from

```bash
mddb-cli reference add github.com/etcd-io/bbolt [--ref v1.4.0] [--name bbolt]
mddb-cli reference list
mddb-cli reference update [name]
mddb-cli reference remove <name>
mddb-cli reference search "how are freelist pages released"
```

Shallow-clones into the user cache directory (`MDDB_REFERENCES_DIR` overrides
it), indexes the clone into `ref-<owner>-<repo>` marked `code.role:
reference`, with the licence and source on every document. See
[docs/REFERENCE-CODING.md](../../docs/REFERENCE-CODING.md).

#### eval - Measure retrieval quality

```bash
mddb-cli eval <collection> [--queries file.json] [--config fts/bm25]... [--k 10] [--fail-below recall@10=0.8,ndcg=0.6]
```

Runs the collection's evaluation set, or the queries in `--queries`, under
each `--config` (`fts/<algorithm>`, `vector[/<index>]`, `hybrid`, optionally
with BM25 parameters: `fts/bm25:k1=2:b=0.75`). It prints
recall@k, nDCG@k, MRR, latency and tokens for each. With `--fail-below` it
exits non-zero when any configuration scores under a threshold, which makes
it a CI gate. See
[docs/SEARCH.md](../../docs/SEARCH.md#measuring-instead-of-guessing-v2188).

#### gain - What retrieval saved

```bash
mddb-cli gain [--window 30d]
```

Prints, for the whole server, the searches by type and algorithm, the
tokens returned, and the tokens saved by passages and chunks compared with
whole documents. Token counts are estimates. The definitions are in
[docs/TELEMETRY.md](../../docs/TELEMETRY.md#retrieval-gain-v2188).

#### alias, reindex-swap - Stable names for collections

```bash
mddb-cli alias set docs docs-v1        # create or move
mddb-cli alias list
mddb-cli alias delete docs             # the collection stays
mddb-cli reindex-swap docs --into docs-v2 [--timeout 30m]
```

`reindex-swap` copies every document of the collection `docs` points at into
`docs-v2`. Give `docs-v2` its new configuration first, such as an embedding
model or chunking; without one, the old configuration is copied. It waits
until `docs-v2` is indexed, then moves the alias. The old collection is
kept, so you can roll back with `alias set`.

#### autoindex - Index a directory or repository

```bash
mddb-cli autoindex [path] [-c collection] [--lang en] [--include glob]... [--exclude glob]...
                   [--max-file-size bytes] [--profile fast] [--images] [--no-frontmatter]
                   [--prune] [--dry-run]
```

A Markdown file's frontmatter becomes metadata (v2.18.11+): the file is sent as
it is and the server reads the frontmatter once, with the parser `/v1/upload`
uses, then stores the body without the block. `tags: a, b`, `tags: [a, b]` and
a `- a` block list all become lists; the file meta below wins for a key both
name. `--no-frontmatter` stores Markdown files untouched.

Walks the directory (git-aware), skips binary and oversized files, sends the
rest in batches with path, kind, language, size, modified and git metadata,
and stores a retrieval profile from the search advisor. A second run rewrites
only changed files. PDF, DOCX, ODT, RTF, XLSX, ODS, PPTX, ODP and EPUB
(recognised by content) and HTML, TeX, CSV, TSV, EML and MBOX (by extension)
are sent as files, which the server converts to Markdown (v2.18.9+); the
report lists each with its format and conversion confidence, e.g.
`~ scan.pdf: converted from pdf (confidence 0.00: 3 of 3 pages have no extractable text ...)`.
With `--images` (v2.18.9+) each Markdown file is sent with the JPEG, PNG, GIF
and WebP files its relative image links name (inside the indexed tree, at
most 2 MB each), and the server stores them, and the pictures of PDFs, as
image documents under the file's key, with alt text and captions searchable
([docs/SEARCH.md](../../docs/SEARCH.md#images--alt-text-captions-and-pictures-v2189)).
See [docs/BULK-IMPORT.md](../../docs/BULK-IMPORT.md).

#### add - Add or update a document

```bash
# From stdin
echo "# Content" | mddb-cli add COLLECTION KEY LANG

# From file
mddb-cli add blog post1 en_US -f post.md

# With metadata
mddb-cli add blog post1 en_US -f post.md \
  -m "category=tech|tutorial,author=John Doe"
```

**Options:**
- `-f, --file FILE` - Read content from file
- `-m, --meta META` - Metadata (format: key=val1|val2,key2=val)
- `--no-frontmatter` - Store the content as it is, frontmatter block included

A frontmatter block at the top is read as `/v1/upload` reads it (v2.18.11+):

```markdown
---
title: Getting Started
tags: [tutorial, beginner]
topics:
  - go
  - search
---
# Getting Started
```

is stored as the body `# Getting Started` with meta `title=[Getting Started]`,
`tags=[tutorial, beginner]` and `topics=[go, search]`; `tags: tutorial, beginner`
gives the same list. A `-m` key wins over a frontmatter key of the same name.
Content without frontmatter is sent unchanged.

#### get - Retrieve a document

```bash
# Basic retrieval
mddb-cli get blog post1 en_US

# With template variables
mddb-cli get blog post1 en_US -e "year=2024,author=John"

# Content only (for piping)
mddb-cli get blog post1 en_US -c > output.md
```

**Options:**
- `-e, --env ENV` - Template variables (format: key=val,key2=val2)
- `-c, --content-only` - Output only content

#### search - Search documents

```bash
# All documents
mddb-cli search blog

# With filter
mddb-cli search blog -f "category=tech|tutorial"

# Multiple filters (AND logic)
mddb-cli search blog -f "category=tech,status=published"

# With sorting
mddb-cli search blog -S addedAt -a

# With pagination
mddb-cli search blog -l 10 -o 20
```

**Options:**
- `-f, --filter FILTER` - Metadata filter
- `-S, --sort FIELD` - Sort field (addedAt, updatedAt, key)
- `-a, --asc` - Sort ascending
- `-l, --limit N` - Limit results (default: 50)
- `-o, --offset N` - Offset results

#### export - Export documents

```bash
# Export as NDJSON
mddb-cli export blog -o backup.ndjson

# Export as ZIP
mddb-cli export blog -F zip -o backup.zip

# Export with filter
mddb-cli export blog -f "status=published" -o published.ndjson
```

**Options:**
- `-F, --format FORMAT` - Format: ndjson, zip (default: ndjson)
- `-o, --output FILE` - Output file (default: stdout)
- `-f, --filter FILTER` - Metadata filter

#### backup - Create database backup

```bash
# Auto-generated filename
mddb-cli backup

# Custom filename
mddb-cli backup my-backup.db
```

#### backup schedule - Scheduled backups (v2.18.9+)

The server takes these itself, on a cron schedule, and keeps the newest
`--keep` on a directory inside `MDDB_BACKUP_DIR` or in an S3-compatible
bucket. They are cron automations with a backup action and run when the
server has `MDDB_CRONS=true` (see `docs/BACKUP.md#scheduled-backups`).

```bash
# Nightly at 03:00, seven kept in MDDB_BACKUP_DIR/nightly
mddb-cli backup schedule add --name nightly --cron "0 3 * * *" --path nightly --keep 7

# Offsite to S3 (or MinIO, R2...); the secret comes from MDDB_BACKUP_SECRET_KEY
export MDDB_BACKUP_SECRET_KEY=...
mddb-cli backup schedule add --name offsite --cron "0 4 * * *" --target s3 \
  --endpoint s3.eu-west-1.amazonaws.com --region eu-west-1 --tls \
  --bucket mddb-backups --prefix prod-eu/ --access-key AKIA... --keep 30

mddb-cli backup schedule list
mddb-cli backup schedule remove <id>     # the backups already taken stay
```

`add` flags: `--name`, `--cron` (both required; 5 fields, or 6 with seconds
first), `--target local|s3`, `--path`, `--bucket`, `--prefix`, `--keep`,
`--collections a,b` (per-collection layout only), `--endpoint`, `--region`,
`--access-key`, `--secret-key` (default `$MDDB_BACKUP_SECRET_KEY`), `--tls`,
`--disabled`. `remove` refuses an ID that is not a backup schedule.

#### sync - Synced sources (v2.18.11+)

A synced source keeps a collection in step with a sitemap, a list of URLs
or the objects under an S3 prefix, on a cron schedule. It is a cron
automation with a sync action and runs when the server has
`MDDB_CRONS=true` (see `docs/AUTOMATIONS.md#synced-sources`).

```bash
mddb-cli sync create --name docs --cron "0 * * * *" --source sitemap \
  --url https://docs.example.com/sitemap.xml --collection docs --prune

# S3 (or MinIO, R2...); the secret comes from MDDB_SYNC_SECRET_KEY
export MDDB_SYNC_SECRET_KEY=...
mddb-cli sync create --name kb --cron "*/30 * * * *" --source s3 --collection kb \
  --endpoint s3.example.com --tls --bucket kb --prefix articles/ --access-key AKIA...

mddb-cli sync list                      # every source, its last run and result
mddb-cli sync list --collection docs    # the sources feeding docs
mddb-cli sync run <id>                  # run now; prints the job id
mddb-cli sync delete <id>               # the documents it wrote stay
```

`create` flags: `--name`, `--cron`, `--source sitemap|urls|s3`,
`--collection` (all required), `--lang` (default `en`), `--url`, `--urls a,b`,
`--bucket`, `--prefix`, `--endpoint`, `--region`, `--access-key`,
`--secret-key` (default `$MDDB_SYNC_SECRET_KEY`), `--tls`, `--prune`,
`--max-documents`, `--max-bytes`, `--disabled`. `list` shows the last result
as `status +added ~updated =unchanged -deleted !failed: error`; the S3 secret
is never shown. `run` fails (409) while the previous run is still going.
`delete` refuses an ID that is not a synced source.

#### restore - Restore from backup

```bash
mddb-cli restore backup-1699296000.db
```

⚠️ **Warning:** This replaces the current database!

#### truncate - Clean up old revisions

```bash
# Keep last 5 revisions
mddb-cli truncate blog

# Keep last 10 revisions
mddb-cli truncate blog -k 10

# Remove all revisions
mddb-cli truncate blog -k 0
```

**Options:**
- `-k, --keep N` - Number of revisions to keep (default: 5)
- `-d, --drop-cache` - Drop cache (default: true)

#### stats - Show server statistics

```bash
# Display statistics
mddb-cli stats

# JSON output
mddb-cli stats -j
```

Shows:
- Database path and size
- Access mode
- Total documents, revisions, and indices
- Per-collection statistics

#### diagnose - A bundle to attach to an issue (v2.18.10+)

```bash
mddb-cli diagnose --inspect               # what it would contain; writes nothing
mddb-cli diagnose                         # mddb-diagnose-<host>-<time>.tar.gz
mddb-cli diagnose --profiles --integrity --out bundle.tar.gz
```

A tar.gz describing the server — build, configuration through an allowlist,
stats, metrics, query-shape counters, recent error classes — without document
content, metadata values, document IDs, query text, keys, actors, IP addresses
or paths. Collection names are hashed unless `--keep-names` is given.
`MANIFEST.json` lists every file, its size, its SHA-256 and the redaction
applied; the CLI checks each file against it. Needs an admin login or key.
`--profiles` adds `runtime.json` (goroutine count, heap and GC counters; stacks and heap profiles come from `/debug/pprof`), `--integrity` bbolt's
consistency check of every database file (slow on a large database). See
[TELEMETRY.md, "Diagnose bundle"](../../docs/TELEMETRY.md#diagnose-bundle).

## Examples

### Basic Workflow

```bash
# 1. Add a document
cat > post.md <<EOF
# My First Post
This is the content of my first post.
EOF

mddb-cli add blog first-post en_US -f post.md \
  -m "category=blog,author=John Doe,tags=golang|database"

# 2. Retrieve it
mddb-cli get blog first-post en_US

# 3. Search for it
mddb-cli search blog -f "category=blog"

# 4. Export all blog posts
mddb-cli export blog -o blog-backup.ndjson
```

### Working with Templates

```bash
# Add document with template variables
cat > welcome.md <<EOF
# Welcome to %%siteName%%

The year is %%year%% and we're glad you're here!
EOF

mddb-cli add pages welcome en_US -f welcome.md

# Retrieve with substitution
mddb-cli get pages welcome en_US \
  -e "siteName=My Awesome Site,year=2024"
```

### Bulk Operations

```bash
# Import multiple files
for file in posts/*.md; do
  key=$(basename "$file" .md)
  mddb-cli add blog "$key" en_US -f "$file" \
    -m "category=blog,status=published"
done

# Export and process with jq
mddb-cli export blog -j | \
  jq -r '.[] | select(.meta.category[] == "tutorial") | .key'

# Backup all collections
for collection in blog pages docs; do
  mddb-cli export "$collection" -o "backup-${collection}.ndjson"
done
```

### Maintenance Tasks

```bash
# Daily backup script
#!/bin/bash
DATE=$(date +%Y-%m-%d)
mddb-cli backup "backup-${DATE}.db"

# Clean up old revisions
mddb-cli truncate blog -k 5
mddb-cli truncate pages -k 10

# Export published content
mddb-cli export blog -f "status=published" -o "published-${DATE}.ndjson"
```

### Integration with Other Tools

```bash
# Convert to HTML with pandoc
mddb-cli get blog post1 en_US -c | pandoc -o post.html

# Count documents
mddb-cli search blog -j | jq '. | length'

# List all keys
mddb-cli search blog -j | jq -r '.[].key'

# Find documents by author
mddb-cli search blog -j | \
  jq -r '.[] | select(.meta.author[] == "John Doe") | .key'
```

## Metadata Format

Metadata uses a simple key=value format:

```
key1=value1|value2,key2=value3
```

This creates:
```json
{
  "key1": ["value1", "value2"],
  "key2": ["value3"]
}
```

Multiple values for the same key are separated by `|` (OR logic).
Multiple keys are separated by `,` (AND logic).

## Filtering

Filters use the same format as metadata:

```bash
mddb-cli search blog -f "category=tech|tutorial,status=published"
```

This matches documents where:
- (category = "tech" OR category = "tutorial") AND
- (status = "published")

## Environment Variables

```bash
export MDDB_SERVER=http://localhost:11023
mddb-cli get blog post1 en_US  # Uses MDDB_SERVER
```

## Output Formats

### Human-Readable (default)
Formatted output with labels and structure.

### JSON (`--json`)
Raw JSON responses from server.

### Content-Only (`-c` with get)
Only markdown content, suitable for piping.

## Updating

```bash
mddb-cli self-update --check    # report only
mddb-cli self-update            # download, verify against checksums.txt, install
```

A snap or container install is updated through its own channel; the command
says which. On Windows `self-update` (with `--check` too) stops with a non-zero
exit: releases carry Linux, macOS and FreeBSD archives only, so build the
Windows binary from source with `make build-windows`
([docs/INSTALLATION.md](../../docs/INSTALLATION.md#windows)).

## Man Page

View the full manual:

```bash
man mddb-cli
```

## Troubleshooting

### Connection Refused

```bash
# Check if server is running
curl http://localhost:11023/v1/search

# Specify different server
mddb-cli -s http://localhost:8080 search blog
```

### Permission Denied (install)

```bash
# Use sudo for installation
sudo make install-all
```

### Command Not Found

```bash
# Check installation
which mddb-cli

# Add to PATH if needed
export PATH=$PATH:/usr/local/bin
```

## Uninstallation

```bash
make uninstall-cli
```

Or manually:
```bash
sudo rm /usr/local/bin/mddb-cli
sudo rm /usr/local/share/man/man1/mddb-cli.1
```

## See Also

- [MDDB Documentation](../../docs/)
- [API Reference](../../docs/API.md)
- [Examples](../../docs/EXAMPLES.md)

## License

BSD 3-Clause License - see [LICENSE](../../LICENSE) for details.
