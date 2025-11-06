# mddb-cli - MDDB Command-Line Client

A command-line client for MDDB (Markdown Database), providing an interface similar to `mysql-client` for managing markdown documents.

## Installation

### Build from Source

```bash
# From project root
make build-cli

# Install to system
make install-all
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

MIT License - see [LICENSE](../../LICENSE) for details.
