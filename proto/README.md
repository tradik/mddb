# MDDB Protocol Buffers

This directory contains the shared Protocol Buffer definitions for MDDB. All services and clients use these definitions to ensure API compatibility.

## 📁 Structure

```
proto/
├── mddb.proto              # Main service definition
├── generate.sh             # Wrapper — runs buf if available, falls back to legacy
├── generate-legacy.sh      # Legacy protoc-based generator (fallback only)
└── README.md               # This file

buf.yaml                    # Buf module config + lint rules (repo root)
buf.gen.yaml                # Buf generation config with PINNED plugin versions
```

## 🔧 Generating Code

Code generation is driven by [`buf`](https://buf.build), which pins all plugin versions in [`buf.gen.yaml`](../buf.gen.yaml) for reproducible builds across environments.

### Primary: `buf generate`

```bash
# From project root — requires buf CLI (https://buf.build/docs/installation)
buf generate

# Or via the wrapper which also runs `buf lint` first
./proto/generate.sh
```

This generates:
- **Go** → `services/mddbd/proto/` (`protoc-gen-go v1.36.11`, `grpc-go v1.6.1`)
- **Python** → `clients/python/mddb_client/` (`protobuf v31.1`, `grpc-python v1.71.0`)
- **Node.js** → `clients/nodejs/proto/` (`protobuf-js v3.21.4`, `grpc-node v1.13.0`)
- **PHP** → `services/php-extension/proto/` (`protobuf v31.1`, `grpc-php v1.72.0`)

### Lint & Breaking Change Detection

```bash
# Lint proto files against the configured STANDARD rule set
buf lint

# Detect breaking changes vs main branch (what CI runs on PRs)
buf breaking --against '.git#branch=main'
```

CI enforces all three on every PR: `buf lint`, `buf breaking`, and `git diff --exit-code` after `buf generate` to catch stale committed code.

### Fallback: Legacy `protoc`

If `buf` cannot be installed in your environment, the legacy `protoc`-based flow is preserved:

```bash
./proto/generate-legacy.sh
```

**Caveats:** plugin versions are not pinned (`go install ... @latest` at runtime), no lint checks, and the script has a known quirk where `-I proto` strips the path prefix. Prefer `buf` unless you have a specific reason not to.

### Individual Languages (manual `protoc`)

Only for reference / debugging the legacy flow:

#### Go

```bash
protoc --go_out=services/mddbd --go_opt=paths=source_relative \
    --go-grpc_out=services/mddbd --go-grpc_opt=paths=source_relative \
    -I proto proto/mddb.proto
```

#### Python

```bash
python3 -m grpc_tools.protoc \
    -I proto \
    --python_out=clients/python/mddb_client \
    --grpc_python_out=clients/python/mddb_client \
    proto/mddb.proto
```

#### Node.js

```bash
# Copy proto for runtime loading
cp proto/mddb.proto clients/nodejs/proto/

# Or generate static code
grpc_tools_node_protoc \
    --js_out=import_style=commonjs,binary:clients/nodejs/proto \
    --grpc_out=grpc_js:clients/nodejs/proto \
    -I proto proto/mddb.proto
```

#### PHP

```bash
protoc --php_out=services/php-extension/proto \
    --grpc_out=services/php-extension/proto \
    --plugin=protoc-gen-grpc=`which grpc_php_plugin` \
    -I proto proto/mddb.proto
```

## 📝 Modifying the Protocol

### Workflow

1. **Edit** `proto/mddb.proto`
2. **Lint** the changes: `buf lint`
3. **Check for breaking changes:** `buf breaking --against '.git#branch=main'`
4. **Regenerate** code: `buf generate` (or `./proto/generate.sh`)
5. **Update** implementations in services/clients
6. **Test** all affected components: `cd services/mddbd && go test ./...`
7. **Document** changes in CHANGELOG.md
8. **Commit** both the `.proto` file and the regenerated code — CI fails if they drift.

### Versioning Rules

Follow Protocol Buffers compatibility rules:

✅ **Safe Changes:**
- Adding new fields (with new field numbers)
- Adding new RPC methods
- Adding new message types
- Making required fields optional

❌ **Breaking Changes:**
- Changing field numbers
- Changing field types
- Removing fields
- Renaming fields or messages

### Example: Adding a New Field

```protobuf
message Document {
  string id = 1;
  string key = 2;
  string lang = 3;
  map<string, MetaValues> meta = 4;
  string content_md = 5;
  int64 added_at = 6;
  int64 updated_at = 7;
  string author = 8;  // ✅ New field - safe to add
}
```

## 🎯 Best Practices

### Field Numbers

- **1-15**: Most frequently used fields (1 byte encoding)
- **16-2047**: Less frequent fields (2 bytes encoding)
- **19000-19999**: Reserved by Protocol Buffers
- **Never reuse** field numbers of deleted fields

### Naming Conventions

- **Messages**: PascalCase (`AddRequest`, `Document`)
- **Fields**: snake_case (`content_md`, `added_at`)
- **RPCs**: PascalCase (`Add`, `GetStats`)
- **Enums**: UPPER_SNAKE_CASE

### Comments

Always document:
- Purpose of each message
- Meaning of each field
- Constraints and validation rules
- Examples where helpful

```protobuf
// Document represents a markdown document with metadata.
// Documents are versioned - each update creates a new revision.
message Document {
  // Unique identifier (format: "collection|key|lang")
  string id = 1;
  
  // Document key (e.g., "homepage", "about-us")
  string key = 2;
  
  // Language code (e.g., "en_US", "pl_PL")
  string lang = 3;
  
  // Metadata key-value pairs (multi-value support)
  map<string, MetaValues> meta = 4;
  
  // Markdown content
  string content_md = 5;
  
  // Unix timestamp of first creation
  int64 added_at = 6;
  
  // Unix timestamp of last update
  int64 updated_at = 7;
}
```

## 🔍 Validation

Before committing changes:

```bash
# Lint proto style (matches what CI enforces)
buf lint

# Ensure no breaking changes vs main
buf breaking --against '.git#branch=main'

# Generate code for all languages (with pinned plugin versions)
buf generate

# Verify generated code matches committed files (CI runs this)
git diff --exit-code services/mddbd/proto/ clients/python/mddb_client/ \
    clients/nodejs/proto/ services/php-extension/proto/

# Run tests
cd services/mddbd && go test ./...
```

## 📦 Dependencies

### Primary: `buf` (recommended)

```bash
# macOS
brew install bufbuild/buf/buf

# Linux (see https://buf.build/docs/installation for more options)
curl -sSL "https://github.com/bufbuild/buf/releases/latest/download/buf-$(uname -s)-$(uname -m)" -o /usr/local/bin/buf
chmod +x /usr/local/bin/buf
```

All language-specific plugins are executed remotely on the Buf Schema Registry with pinned versions — **no local plugin installation required**.

### Fallback: local `protoc` + language plugins

Only needed if using `./proto/generate-legacy.sh`:

- **protoc** - Protocol Buffer Compiler
  ```bash
  brew install protobuf        # macOS
  apt-get install protobuf-compiler  # Linux
  ```

#### Go
```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```

#### Python
```bash
pip3 install grpcio-tools
```

#### Node.js
```bash
npm install -g grpc-tools
```

#### PHP
```bash
pecl install grpc
```

## 🔗 Resources

- [Protocol Buffers Guide](https://protobuf.dev/)
- [gRPC Documentation](https://grpc.io/docs/)
- [Proto3 Language Guide](https://protobuf.dev/programming-guides/proto3/)
- [Style Guide](https://protobuf.dev/programming-guides/style/)

## 📄 License

BSD 3-Clause License - see [LICENSE](LICENSE) in this directory. The definitions stay permissive so anyone can generate a client; the server they describe is under the Business Source License 1.1 ([LICENSING.md](../LICENSING.md)).
