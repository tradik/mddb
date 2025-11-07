# MDDB Examples

This directory contains example markdown files and usage scenarios for MDDB.

## Files

### sample-with-frontmatter.md

Example markdown file with YAML frontmatter metadata. Demonstrates:
- Frontmatter format (between `---` markers)
- Common metadata fields (title, author, category, tags, etc.)
- Markdown content structure

## Using the Bulk Import Script

### Import this example folder

```bash
# Preview what would be imported
./scripts/load-md-folder.sh ./examples blog --dry-run

# Import all examples
./scripts/load-md-folder.sh ./examples blog

# Import with custom metadata
./scripts/load-md-folder.sh ./examples blog \
  -m "source=examples" \
  -m "status=published"

# Import with verbose output
./scripts/load-md-folder.sh ./examples blog -v
```

### Using Makefile

```bash
# Import examples
make import-folder FOLDER=./examples COLLECTION=blog

# Preview import
make import-folder-dry FOLDER=./examples COLLECTION=blog

# Import with custom language
make import-folder FOLDER=./examples COLLECTION=blog LANG=pl_PL
```

## Frontmatter Format

Frontmatter is YAML-style metadata at the beginning of markdown files:

```markdown
---
key: value
another_key: another value
tags: value1, value2
---

# Your content here
```

### Supported Fields

Common frontmatter fields:
- `title` - Document title
- `author` - Author name
- `category` - Document category
- `tags` - Comma-separated tags
- `date` - Publication date
- `version` - Document version
- `status` - Publication status (draft, published, etc.)
- `difficulty` - Difficulty level (beginner, intermediate, advanced)

You can use any custom fields you need!

## Testing the Import

After importing, verify the documents:

```bash
# List all documents in collection
mddb-cli search blog

# Get specific document
mddb-cli get blog sample-with-frontmatter en_US

# Search by metadata
mddb-cli search blog -f "category=tutorial"
mddb-cli search blog -f "author=John Doe"
mddb-cli search blog -f "difficulty=beginner"
```

## Creating Your Own Examples

1. Create a markdown file with frontmatter:
   ```markdown
   ---
   title: My Document
   author: Your Name
   category: example
   ---
   
   # Content here
   ```

2. Save it in this directory

3. Import using the script:
   ```bash
   ./scripts/load-md-folder.sh ./examples blog
   ```

## See Also

- [Bulk Import Documentation](../docs/BULK-IMPORT.md)
- [API Documentation](../docs/API.md)
- [Usage Examples](../docs/EXAMPLES.md)
