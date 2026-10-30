---
title: "Schema Validation"
slug: "docs/schema-validation"
description: "Enforce structure on document metadata per collection in MDDB: opt-in field types, required keys and constraints, disabled by default."
status: publish
---

# Schema Validation

> **Note**: The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD", "SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be interpreted as described in [RFC 2119](https://www.ietf.org/rfc/rfc2119.txt).

## Table of Contents

- [Overview](#overview)
- [Quick Start](#quick-start)
- [Supported Rules](#supported-rules)
  - [required](#required)
  - [properties](#properties)
  - [enum](#enum)
  - [pattern](#pattern)
  - [minItems / maxItems](#minitems--maxitems)
- [HTTP API](#http-api)
  - [POST /v1/schema/set](#post-v1schemaset)
  - [POST /v1/schema/get](#post-v1schemaget)
  - [POST /v1/schema/delete](#post-v1schemadelete)
  - [POST /v1/schema/list](#post-v1schemalist)
  - [POST /v1/validate](#post-v1validate)
- [gRPC API](#grpc-api)
- [CLI](#cli)
- [MCP Tools](#mcp-tools)
- [Disabling Validation](#disabling-validation)
- [Error Messages](#error-messages)

## Overview

Schema Validation lets you enforce structure on document metadata within a collection. It is **opt-in per collection** and **disabled by default** -- a collection without a schema accepts any metadata.

Schemas use a **JSON Schema subset** focused on metadata validation. When a schema is set for a collection, every call to `/v1/add` (or the gRPC `Add` RPC) will validate the document's `meta` field against the schema before persisting. If validation fails, the request is rejected with a descriptive error.

Key points:

- Schemas are scoped to a single collection.
- Only the `meta` field is validated; `contentMd`, `key`, and `lang` are unaffected.
- Deleting a schema immediately disables validation for that collection.
- Schemas are stored in the database and survive restarts.

## Quick Start

### Step 1 -- Set a schema

```bash
curl -X POST http://localhost:11023/v1/schema/set \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "schema": "{\"required\":[\"category\",\"author\"],\"properties\":{\"category\":{\"type\":\"string\",\"enum\":[\"blog\",\"tutorial\",\"news\"]},\"author\":{\"type\":\"string\"},\"tags\":{\"type\":\"string\",\"minItems\":1,\"maxItems\":5}}}"
  }'
```

### Step 2 -- Add a valid document

```bash
curl -X POST http://localhost:11023/v1/add \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "key": "hello",
    "lang": "en_US",
    "meta": {
      "category": ["blog"],
      "author": ["John Doe"],
      "tags": ["golang", "database"]
    },
    "contentMd": "# Hello World"
  }'
```

Response: the document is stored successfully.

### Step 3 -- See a validation error

```bash
curl -X POST http://localhost:11023/v1/add \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "key": "bad-post",
    "lang": "en_US",
    "meta": {
      "tags": ["golang"]
    },
    "contentMd": "# Missing required fields"
  }'
```

Response:

```json
{
  "error": "schema validation failed: missing required field \"category\"; missing required field \"author\""
}
```

## Supported Rules

### required

An array of metadata key names that MUST be present in every document.

```json
{
  "required": ["category", "author"]
}
```

If a document is added without the `category` key in its `meta`, validation fails.

### properties

Defines per-key rules. Each key maps to an object that MAY contain `type`, `enum`, `pattern`, `minItems`, and `maxItems`.

```json
{
  "properties": {
    "status": { "type": "string" },
    "priority": { "type": "integer" },
    "score": { "type": "number" },
    "featured": { "type": "boolean" }
  }
}
```

Supported types:

| Type | Description | Example valid values |
|------|-------------|---------------------|
| `string` | Any string value | `["hello"]`, `["foo", "bar"]` |
| `number` | Numeric value (integer or float) | `["3.14"]`, `["42"]` |
| `integer` | Integer value only | `["42"]`, `["-1"]` |
| `boolean` | Boolean value | `["true"]`, `["false"]` |

Since metadata values in MDDB are always stored as `[]string`, type validation checks that each value in the array can be parsed as the declared type.

### enum

Restricts a key's values to a fixed set of allowed strings.

```json
{
  "properties": {
    "status": {
      "type": "string",
      "enum": ["draft", "published", "archived"]
    }
  }
}
```

A document with `"status": ["pending"]` would fail validation because `"pending"` is not in the allowed set.

### pattern

A regular expression that every value for the key MUST match.

```json
{
  "properties": {
    "slug": {
      "type": "string",
      "pattern": "^[a-z0-9-]+$"
    }
  }
}
```

A document with `"slug": ["Hello World"]` would fail because the value does not match the pattern.

### minItems / maxItems

Controls the number of values allowed per metadata key.

```json
{
  "properties": {
    "tags": {
      "type": "string",
      "minItems": 1,
      "maxItems": 5
    },
    "category": {
      "type": "string",
      "minItems": 1,
      "maxItems": 1
    }
  }
}
```

- `minItems`: Minimum number of values (e.g., at least 1 tag).
- `maxItems`: Maximum number of values (e.g., exactly 1 category).

A document with `"tags": []` would fail the `minItems: 1` check. A document with `"category": ["blog", "news"]` would fail the `maxItems: 1` check.

## HTTP API

### POST /v1/schema/set

Set or update the validation schema for a collection. `schema` is the schema document as a **JSON string**, not a nested object: an object is a 400. `schema/get` and `schema/list` return it the same way.

**Request Body**:
```json
{
  "collection": "blog",
  "schema": "{\"required\":[\"category\",\"author\"],\"properties\":{\"category\":{\"type\":\"string\",\"enum\":[\"blog\",\"tutorial\",\"news\"]},\"author\":{\"type\":\"string\"},\"tags\":{\"type\":\"string\",\"minItems\":1,\"maxItems\":5}}}"
}
```

**Response**:
```json
{
  "status": "ok",
  "collection": "blog"
}
```

**cURL Example**:
```bash
curl -X POST http://localhost:11023/v1/schema/set \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "schema": "{\"required\":[\"category\"],\"properties\":{\"category\":{\"type\":\"string\",\"enum\":[\"blog\",\"tutorial\"]}}}"
  }'
```

---

### POST /v1/schema/get

Retrieve the current schema for a collection.

**Request Body**:
```json
{
  "collection": "blog"
}
```

**Response** (schema exists):
```json
{
  "collection": "blog",
  "schema": "{\"required\":[\"category\",\"author\"],\"properties\":{\"category\":{\"type\":\"string\",\"enum\":[\"blog\",\"tutorial\",\"news\"]},\"author\":{\"type\":\"string\"},\"tags\":{\"type\":\"string\",\"minItems\":1,\"maxItems\":5}}}",
  "enabled": true
}
```

**Response** (no schema):
```json
{
  "collection": "blog",
  "schema": "",
  "enabled": false
}
```

**cURL Example**:
```bash
curl -X POST http://localhost:11023/v1/schema/get \
  -H 'Content-Type: application/json' \
  -d '{"collection": "blog"}'
```

---

### POST /v1/schema/delete

Delete the schema for a collection, disabling validation.

**Request Body**:
```json
{
  "collection": "blog"
}
```

**Response**:
```json
{
  "status": "ok",
  "collection": "blog"
}
```

**cURL Example**:
```bash
curl -X POST http://localhost:11023/v1/schema/delete \
  -H 'Content-Type: application/json' \
  -d '{"collection": "blog"}'
```

---

### POST /v1/schema/list

List all collections that have a schema defined.

**Request Body**: Empty or `{}`.

**Response**:
```json
{
  "schemas": [
    {
      "collection": "blog",
      "schema": "{\"required\":[\"category\",\"author\"],\"properties\":{\"category\":{\"type\":\"string\",\"enum\":[\"blog\",\"tutorial\",\"news\"]},\"author\":{\"type\":\"string\"}}}"
    },
    {
      "collection": "products",
      "schema": "{\"required\":[\"price\",\"sku\"],\"properties\":{\"price\":{\"type\":\"number\"},\"sku\":{\"type\":\"string\",\"pattern\":\"^SKU-[0-9]+$\"}}}"
    }
  ]
}
```

**cURL Example**:
```bash
curl -X POST http://localhost:11023/v1/schema/list \
  -H 'Content-Type: application/json' \
  -d '{}'
```

---

### POST /v1/validate

Validate a document's metadata against the collection schema without persisting anything. Useful for dry-run checks before adding documents.

**Request Body**:
```json
{
  "collection": "blog",
  "meta": {
    "category": ["blog"],
    "author": ["Jane Doe"],
    "tags": ["golang", "tutorial"]
  }
}
```

**Response** (valid):
```json
{
  "valid": true,
  "errors": [],
  "warnings": []
}
```

**Response** (invalid):
```json
{
  "valid": false,
  "errors": [
    "schema validation failed: field \"tags\" has 6 values, maximum 5 allowed",
    "field \"status\" value \"pending\" not in allowed values [draft published archived]"
  ],
  "warnings": []
}
```

**Response** (no schema set for collection):
```json
{
  "valid": true,
  "errors": [],
  "warnings": []
}
```

**cURL Example**:
```bash
curl -X POST http://localhost:11023/v1/validate \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "meta": {
      "category": ["blog"],
      "author": ["Jane Doe"]
    }
  }'
```

---

## gRPC API

The following RPCs are available on the `mddb.MDDB` service (port `11024`):

| RPC | Request | Response | Description |
|-----|---------|----------|-------------|
| `SetSchema` | `SetSchemaRequest` | `SetSchemaResponse` | Set or update a collection schema |
| `GetSchema` | `GetSchemaRequest` | `GetSchemaResponse` | Retrieve a collection schema |
| `DeleteSchema` | `DeleteSchemaRequest` | `DeleteSchemaResponse` | Delete a collection schema |
| `ListSchemas` | `ListSchemasRequest` | `ListSchemasResponse` | List all collection schemas |
| `ValidateDocument` | `ValidateDocumentRequest` | `ValidateDocumentResponse` | Validate metadata against schema |

### grpcurl Examples

```bash
# Set schema
grpcurl -plaintext -d '{
  "collection": "blog",
  "schema": "{\"required\":[\"category\"],\"properties\":{\"category\":{\"type\":\"string\",\"enum\":[\"blog\",\"tutorial\"]}}}"
}' localhost:11024 mddb.MDDB/SetSchema

# Get schema
grpcurl -plaintext -d '{"collection": "blog"}' \
  localhost:11024 mddb.MDDB/GetSchema

# Delete schema
grpcurl -plaintext -d '{"collection": "blog"}' \
  localhost:11024 mddb.MDDB/DeleteSchema

# List all schemas
grpcurl -plaintext -d '{}' \
  localhost:11024 mddb.MDDB/ListSchemas

# Validate document
grpcurl -plaintext -d '{
  "collection": "blog",
  "meta": {
    "category": {"values": ["blog"]},
    "author": {"values": ["John Doe"]}
  }
}' localhost:11024 mddb.MDDB/ValidateDocument
```

---

## CLI

The `mddb-cli` tool provides schema management commands:

### Set a schema

```bash
mddb-cli schema set blog '{
  "required": ["category", "author"],
  "properties": {
    "category": {"type": "string", "enum": ["blog", "tutorial", "news"]},
    "author": {"type": "string"}
  }
}'
```

### Get a schema

```bash
mddb-cli schema get blog
```

### Delete a schema

```bash
mddb-cli schema delete blog
```

### List all schemas

```bash
mddb-cli schema list
```

### Validate metadata

```bash
mddb-cli validate blog '{"category": ["blog"], "author": ["John Doe"]}'
```

---

## MCP Tools

The following MCP tools are available for schema validation:

| Tool | Description |
|------|-------------|
| `set_schema` | Set or update the validation schema for a collection |
| `get_schema` | Retrieve the current schema for a collection |
| `delete_schema` | Delete the schema for a collection |
| `list_schemas` | List all collections with schemas |
| `validate_document` | Validate metadata against a collection schema |

### Example MCP Usage

When connected to an LLM via MCP, you can use natural language:

- "Set a schema for the blog collection that requires category and author fields"
- "Show me the schema for the products collection"
- "Validate this metadata against the blog schema: category=tutorial, author=Jane"
- "List all collections that have schemas"
- "Remove the schema from the blog collection"

---

## Disabling Validation

To disable validation for a collection, delete its schema:

```bash
# HTTP
curl -X POST http://localhost:11023/v1/schema/delete \
  -H 'Content-Type: application/json' \
  -d '{"collection": "blog"}'

# CLI
mddb-cli schema delete blog

# gRPC
grpcurl -plaintext -d '{"collection": "blog"}' \
  localhost:11024 mddb.MDDB/DeleteSchema
```

Once the schema is deleted, all documents will be accepted regardless of their metadata content. Existing documents are NOT re-validated or removed.

---

## Error Messages

When validation fails on `/v1/add`, the response includes a descriptive error:

### Missing required key

```json
{
  "error": "schema validation failed: missing required field \"category\""
}
```

### Invalid type

```json
{
  "error": "schema validation failed: field \"priority\" value \"not-a-number\" is not a valid integer"
}
```

### Enum violation

```json
{
  "error": "schema validation failed: field \"status\" value \"pending\" not in allowed values [draft published archived]"
}
```

### Pattern mismatch

```json
{
  "error": "schema validation failed: field \"slug\" value \"Hello World\" does not match pattern \"^[a-z0-9-]+$\""
}
```

### minItems / maxItems violation

```json
{
  "error": "schema validation failed: field \"tags\" has 0 values, minimum 1 required"
}
```

```json
{
  "error": "schema validation failed: field \"category\" has 3 values, maximum 1 allowed"
}
```

### Multiple errors

When multiple rules are violated, all errors are combined:

```json
{
  "error": "schema validation failed: missing required field \"author\"; field \"tags\" has 6 values, maximum 5 allowed; field \"status\" value \"invalid\" not in allowed values [draft published archived]"
}
```

---

## See Also

- [API Documentation](API.md) - Full HTTP/JSON API reference
- [gRPC Guide](GRPC.md) - gRPC API reference
- [Examples](EXAMPLES.md) - More code examples
