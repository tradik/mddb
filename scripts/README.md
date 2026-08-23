# MDDB Scripts

This directory contains utility scripts for MDDB operations.

## Available Scripts

### load-md-folder.sh

Bulk import markdown files from a folder into MDDB database.

**Features:**
- Automatic key generation from filenames
- YAML frontmatter metadata extraction
- Recursive folder scanning
- Progress tracking with statistics
- Dry run mode for preview
- Custom metadata support
- Multi-language support

**Usage:**
```bash
./scripts/load-md-folder.sh <folder_path> <collection> [options]
```

**Examples:**
```bash
# Basic import
./scripts/load-md-folder.sh ./docs blog

# Recursive import with custom language
./scripts/load-md-folder.sh ./content articles -r -l pl_PL

# Add custom metadata
./scripts/load-md-folder.sh ./posts blog -m "author=John" -m "status=published"

# Dry run (preview only)
./scripts/load-md-folder.sh ./docs blog -d

# Verbose output
./scripts/load-md-folder.sh ./docs blog -v
```

**Options:**
- `-l, --lang LANG` - Language code (default: en_US)
- `-r, --recursive` - Process subfolders recursively
- `-m, --meta KEY=VALUE` - Add metadata (can be used multiple times)
- `-s, --server URL` - MDDB server URL
- `-v, --verbose` - Verbose output
- `-d, --dry-run` - Preview without executing
- `-b, --batch-size N` - Progress update frequency
- `-h, --help` - Show help message

**Documentation:**
See [BULK-IMPORT.md](../docs/BULK-IMPORT.md) for detailed documentation.

### check-docs-links.py

Fails the documentation build on internal links that resolve **only through a
redirect**. Everything else is now validated by SSG itself — `check_links`,
`check_images`, `check_meta` and `check_orphans` in `.ssg.yaml` cover dead
links, images with no `alt`, indexable pages missing a title or description,
and orphan pages.

This script covers the one thing those cannot: they resolve a URL against the
output directory, which is not how Cloudflare Pages serves it. Pages rewrites
two shapes before answering, with a 308 each time:

| link in the HTML | what Pages does |
|---|---|
| `/docs/api/swagger/index.html` | 308 → `/docs/api/swagger/` (strips `.html`) |
| `/docs/config` | 308 → `/docs/config/` (appends the slash) |

Both work for a visitor, so nothing is broken — they cost a round trip and a
hop of crawl budget. To the output directory they look valid, so only a check
that models the deploy target sees them. One `.html` link in the shared footer
once put all 51 pages through a redirect.

**Usage:**
```bash
python3 scripts/check-docs-links.py [output_dir]   # default: dist
make docs-linkcheck                                # build, then check
```

Exit codes: `0` clean, `1` redirecting links found, `2` output directory
missing. External hosts are never fetched, so the check is offline and
deterministic. Requested upstream as spagu/ssg#87; it goes away if `check_links`
learns the deploy target.

## Using with Makefile

```bash
# Import folder
make import-folder FOLDER=./docs COLLECTION=blog

# Preview import
make import-folder-dry FOLDER=./docs COLLECTION=blog

# Recursive import
make import-folder-recursive FOLDER=./docs COLLECTION=blog

# With custom options
make import-folder FOLDER=./docs COLLECTION=blog LANG=pl_PL META="author=John"
```

## Requirements

- Bash shell
- `mddb-cli` command available in PATH
- Running MDDB server

## Environment Variables

- `MDDB_SERVER` - Server URL (default: http://localhost:11023)
- `MDDB_CLI` - CLI command path (default: mddb-cli)

## See Also

- [Bulk Import Documentation](../docs/BULK-IMPORT.md)
- [Examples](../docs/EXAMPLES.md)
- [API Documentation](../docs/API.md)
