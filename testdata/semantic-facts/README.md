# Semantic code facts, format v1

`mddb-cli autoindex --semantic` asks a language server where each symbol is
defined, referenced and implemented, and sends the answers with the document
as four meta keys. mddbd checks them on every write and keeps them only while
they describe the content stored. `cases.json` holds the shared cases: the CLI
encodes `facts` and must produce `encoded`; the server receives `sent`
(defaults to `encoded`) and must keep exactly `stored`. The two sides have
separate implementations (mddbd does not depend on the Go client), so these
cases are the contract between them. `generate.py` is the reference encoder
that writes `cases.json`; change the format there first, then both sides.

## Keys

| Key | Values | One value |
|---|---|---|
| `symIndex` | exactly one | `v1 <contentHash> <factsDigest> <source>[ truncated]` |
| `symDefs` | one per definition in this file | `<name> <kind> <start>-<end>[ <container>]` |
| `symRefs` | one or more per referenced symbol | `<defPath>#<name> <line>,<line>,...` |
| `symImpls` | one per implementation in this file | `<ifacePath>#<name> <localName>` |

- `contentHash`: the first 12 hex digits of SHA-256 over the document's content
  exactly as stored. Any change to the content invalidates the facts.
- `factsDigest`: the first 12 hex digits of SHA-256 over, for `symDefs`,
  `symRefs` and `symImpls` in that order, each value in sorted order as
  `key + "\t" + value + "\n"`. Computed by the CLI on what it sent; the server
  stores it and does not check it. The CLI compares it on the next run to see
  whether facts changed without the content changing.
- `source`: the language server and version, e.g. `gopls/0.24.0`.
- `truncated`: added by the server when it dropped values to stay under its
  limit.
- `name`: the qualified name — `Server.LookupSymbol`, `Outer.Inner.method`,
  `App\Http\Kernel.handle`. The last segment after the final `.` is the bare
  name.
- `kind`: one of `function method class interface struct type enum constructor`.
- Lines count from 1. `start-end` is the definition's span; `start <= end`.
- `defPath` / `ifacePath`: `meta.path` of the document holding the definition.
- `symRefs` lines are ascending and unique, at most 64 per value; more lines
  continue in another value with the same `<defPath>#<name>` prefix.

## Characters

Names, kinds, containers and sources contain no whitespace, `|` or `#`.
Paths contain no whitespace or `|` (a path may contain `#`; the name is what
follows the last `#`). A value is at most 1024 bytes. The CLI leaves out facts
it cannot encode under these rules and counts them; the server drops a value
that breaks them. `|` is excluded because the metadata index key is
`meta|<collection>|<key>|<value>|<docID>` and document IDs contain `|`: a
lookup by value prefix cuts the value at the first `|`.

## Order

Every key's values are sorted bytewise (Go `sort.Strings`, Python `sorted`).

## What the server keeps

1. A document that is not code, or has no `symIndex`, keeps no `sym*` key.
2. `symIndex` that is not one well-formed `v1` value, or whose `contentHash`
   is not the hash of the stored content: every `sym*` key is removed.
3. Each other value that does not parse under the rules above is dropped.
4. If more than `maxFacts` values remain across `symDefs`, `symRefs` and
   `symImpls` (default 4096, `MDDB_CODE_MAX_FACTS`), the server keeps
   `symDefs` first, then `symImpls`, then `symRefs`, each in sorted order,
   until the limit, and appends ` truncated` to `symIndex`.
5. An empty key is removed. If all three are empty, `symIndex` stays: the file
   was indexed and has nothing to say.
