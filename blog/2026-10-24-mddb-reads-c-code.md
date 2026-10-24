---
title: "MDDB reads C, C++ and assembly"
slug: "blog/mddb-reads-c-code"
status: publish
type: post
date: 2026-10-24
tags: [search, mcp, agents]
excerpt: "MDDB 2.18 reads C, C++ and assembly: which file defines a function, what calls it, who includes a header. What it finds, how it was checked, and what every other language gets."
description: "MDDB 2.18 extracts C, C++ and assembly symbols for code_graph: function definitions, calls and #include edges, checked against grep."
---

The first questions anyone asks of a C codebase are rarely about prose.
Which file defines `parse_request`? What calls `buf_alloc`? Which files
include `<net/conn.h>`?

MDDB 2.18 answers them. Store a C project one document per file, and MDDB
records what each file defines, calls and includes. An agent working over
MCP can then follow those links instead of guessing from a text search. This
post covers what changed, how it was checked, and the limits worth knowing
before you rely on it.

## What a `.c` file was before

MDDB has known about source code since 2.12, but only for the languages of a
web theme: CSS, JavaScript and HTML. `.c`, `.h` and `.S` were not on the list,
so a C file was indexed as prose unless the client sent `kind: ["code"]`
itself.

For C, prose indexing is the wrong choice. The prose tokenizer stems words,
drops stop words, and splits on every character that is not a letter or a
digit. `buf_alloc` went into the index as two words, `buf` and `alloc`, and
the identifier itself could not be found. Even with `kind: code` set by hand,
a C file got code tokenizing but no symbols, so
[`code_graph`](https://mddb.tradik.com/docs/api/) had no edges for it.

## What MDDB reads from C now

Take `src/server/request.c`:

```c
#include <net/conn.h>
#include <util/buf.h>
#include "request_internal.h"

static int parse_header(struct conn *c, struct buf *b);

int
parse_request(struct conn *c)
{
	struct buf *b = buf_alloc(c->rx_len);

	if (parse_header(c, b) != 0)
		return (conn_close(c, ERR_BAD_REQUEST));
	return (dispatch(c, b));
}
```

Stored with no metadata at all (the extension is enough), the document gets:

```text
language: c
defines:  parse_header, parse_request
uses:     buf_alloc, conn_close, dispatch
imports:  src/net/conn.h, src/util/buf.h, src/server/request_internal.h
```

The rules behind those three lists:

- **defines**: function definitions, `static` prototypes (the definition is in
  this file), `struct`, `union` and `enum` tags, `typedef` names, `#define`
  macros and file-scope variables. A prototype without `static` declares
  something defined elsewhere, so it does not count. Otherwise every header
  would claim every function it declares.
- **uses**: names called inside function bodies that the file does not
  define. Keywords are skipped, and so are calls through a struct member
  (`c->ops->send(c)`), because those are not global symbols.
- **imports**: `#include` targets.

Assembly gets a smaller version of the same: `ENTRY(memcpy_fast)` and `.globl`
become defines, `call`, `bl` and `jal` targets become uses. So when a C file
calls a routine written in assembly, the edge leads into the `.S` file.

## Include paths

`#include "request_internal.h"` is relative to the including file, and MDDB
resolves it that way. `#include <net/conn.h>` is different: the compiler finds
it through `-I`. A collection now has the same setting:

```bash
curl -X PUT "$MDDB/v1/collection-config" -H 'Content-Type: application/json' \
  -d '{"collection":"myproject","code":{"includePaths":["src"]}}'
```

With `["src"]`, `<net/conn.h>` in any file is stored as `src/net/conn.h`,
which matches that header's document key. Set it before loading the code.
Documents written earlier keep their old imports until they are written
again.

## Asking the questions

Once the code is in, the questions from the top of this post are graph
queries:

```bash
# What breaks if I change this header? Callers and includers.
curl -s "$MDDB/v1/code-graph?collection=myproject&key=src/net/conn.h&direction=in"

# What does request.c depend on?
curl -s "$MDDB/v1/code-graph?collection=myproject&key=src/server/request.c&direction=out"
```

Each edge has a kind and the symbol that justifies it. C files produce
`calls` edges (a use matched to a define in another file) and `imports` edges
(an include matched to a document key). The same queries are available to an
agent as the MCP tool `code_graph`. "Which file defines `parse_request`" is a
plain metadata filter: `meta.defines=parse_request`.

## How it was checked

A scanner is only worth something if it agrees with the code, so before
shipping we ran the extractor over a large open-source C codebase:

- **15,617** `.c`, `.h` and `.S` files in about **5 seconds**, single
  threaded, with no failures on malformed or unusual input.
- For a widely included header, the extractor and `grep` agreed on the
  number of files that include it: **730** each.
- For one well-known function, the extractor found **no callers**. That looked
  like a bug, but `grep` agreed: the name appeared only in its definition, its
  prototype and comments. The extractor ignores comments, and a prototype is
  not a call.

That last point is the kind of answer a text search gets wrong. A search for
the name returned five files, and none of them was a caller.

The first run also caught a real mistake. Macros invoked at file scope,
such as `static REGISTER_HANDLER(...)` or `DEFINE_COMMAND(...) { }`, came out
as function definitions. A name written in capitals only is now treated as a
macro. A variable declared with a macro type, such as
`static LIST_HEAD(, conn) idle_conns;`, is recorded under its own name.

## Limits

This is a scanner, not a compiler, and some answers it cannot give:

- **No preprocessing.** Both branches of an `#ifdef` are read, and nothing is
  expanded. A function whose name only appears as a macro argument is not
  recorded as a definition. For example, `DEFINE_COMMAND(status, cmd_status)
  { ... }` defines `cmd_status`, and the extractor does not record it. Its
  calls are still recorded.
- **Indirect calls are not followed.** Calls through a function pointer, or
  through a table of them (an operations vector, a list of callbacks), are not
  edges. In code that dispatches that way, a lot of the call structure is
  invisible to it.
- **The capitals rule is a convention, not a fact.** In a codebase that writes
  functions in capitals, those functions would be missed.
- **The lists are capped** at `MDDB_CODE_MAX_SYMBOLS` (512 by default). A
  generated register header with 90,000 `#define`s keeps its first 512.

None of this replaces cscope, ctags or clangd on your own machine. The point
is different: a model answering questions over MCP gets a first answer
grounded in the code, with the file and symbol to read next.

## Every other language

C is the newest language, not the only one. Here is what MDDB does with each
file type, based on the extension, when the client does not say otherwise:

| Files | Code search and chunking | Symbols and `code_graph` |
|---|---|---|
| CSS, SCSS, Sass, LESS | yes | selectors, custom properties, `@import`, `url()` |
| JavaScript, TypeScript | yes | functions, classes, `import` / `require` |
| HTML | yes | ids, classes used, `src` / `href`, `on*` handlers |
| C, C++ (new) | yes | as above |
| Assembly `.S .s .asm` (new) | yes | `ENTRY` / `.globl`, calls, includes |
| m4 (new) | yes | not yet |
| Go, Python, Ruby, PHP, Rust, Java, shell, SQL | yes | not yet |
| JSON, YAML, TOML, XML, SVG | yes | not yet |

"Code search and chunking" means:

- **Search**: a whole identifier and its parts are both searchable
  (`parse_request`, and also `request`). Keywords such as `if` and `for` stay
  in the index, and nothing is stemmed.
- **Embeddings**: content is split where brackets balance, so a chunk sent
  for embedding is a whole function rather than half of one.

That has worked since 2.12 for every language in the table not marked new.
Symbols are what each language has to earn with its own extractor. If you
need one for a language we have not covered, open an issue with a real file
and the questions you want answered.

## Upgrading

2.18 is backward compatible. To get symbols for C documents you already
store, write them again: symbols are extracted when a document is saved.
The full list of changes is in the
[changelog](https://github.com/tradik/mddb/blob/main/CHANGELOG.md), and the
extraction rules are in the [API reference](https://mddb.tradik.com/docs/api/).
