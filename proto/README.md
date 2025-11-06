# MDDB Protocol Buffers

This directory contains the shared Protocol Buffer definitions for MDDB. All services and clients use these definitions to ensure API compatibility.

## 📁 Structure

```
proto/
├── mddb.proto          # Main service definition
├── generate.sh         # Code generation script for all languages
└── README.md          # This file
```

## 🔧 Generating Code

### All Languages

Generate code for all supported languages:

```bash
# From project root
./proto/generate.sh
```

This generates:
- **Go** → `services/mddbd/proto/`
- **Python** → `clients/python/mddb_client/`
- **Node.js** → `clients/nodejs/proto/`
- **PHP** → `services/php-extension/proto/`

### Individual Languages

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
2. **Regenerate** code: `./proto/generate.sh`
3. **Update** implementations in services/clients
4. **Test** all affected components
5. **Document** changes in CHANGELOG.md

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
# Validate proto syntax
protoc --descriptor_set_out=/dev/null proto/mddb.proto

# Generate code for all languages
./proto/generate.sh

# Run tests
make test
```

## 📦 Dependencies

### Required

- **protoc** - Protocol Buffer Compiler
  ```bash
  # macOS
  brew install protobuf
  
  # Linux
  apt-get install protobuf-compiler
  ```

### Language-Specific

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

MIT License - see [LICENSE](../LICENSE) for details.
