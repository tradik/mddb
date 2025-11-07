# Bulk Import Guide

## Overview

The `load-md-folder.sh` script allows you to bulk import markdown files from a folder into MDDB. It's perfect for migrating existing documentation, importing blog posts, or loading large collections of markdown content.

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

## See Also

- [CLI Documentation](CLI.md)
- [API Documentation](API.md)
- [Examples](EXAMPLES.md)
- [Deployment Guide](DEPLOYMENT.md)
