---
title: "Indexing the FreeBSD kernel source in MDDB"
slug: "blog/reading-the-freebsd-kernel-with-mddb"
status: publish
type: post
date: 2026-10-24
tags: [search, mcp, agents]
excerpt: "MDDB 2.18 reads C, C++ and assembly: which file defines a function, what calls it, who includes a header. Measured on the FreeBSD sys/ tree, plus what every other language gets."
description: "MDDB 2.18 extracts C, C++ and assembly symbols for code_graph: defines, calls and #include edges, checked against grep on FreeBSD's sys/ tree."
---

The first questions anyone asks of a kernel tree are rarely about prose.
Which file defines `do_execve`? What calls `vm_page_alloc`? Which files
include `<sys/mbuf.h>`?

Those questions came in as
[issue #319](https://github.com/tradik/mddb/issues/319) from
[@spagu](https://github.com/spagu), who is building
[kernel.tradik.com](https://kernel.tradik.com): the FreeBSD `sys/` tree
loaded into MDDB, one document per file, so that Claude can query it over
MCP. MDDB 2.18 answers them. This post covers what changed, the numbers from
the real tree, and the limits worth knowing before you rely on it.

## What a `.c` file was before

MDDB has known about source code since 2.12, but only for the languages of a
web theme: CSS, JavaScript and HTML. `.c`, `.h` and `.S` were not on the list,
so a kernel file was indexed as prose unless the client sent
`kind: ["code"]` itself.

For C, prose indexing is the wrong choice. The prose tokenizer stems words,
drops stop words, and splits on every character that is not a letter or a
digit. `vm_page_alloc` went into the index as three words, `vm`, `page` and
`alloc`, and the identifier itself could not be found. Even with `kind: code`
set by hand, a C file got code tokenizing but no symbols, so
[`code_graph`](https://mddb.tradik.com/docs/api/) had no edges for it.

## What MDDB reads from C now

Take the example from the issue, `sys/kern/kern_exec.c`:

```c
#include <sys/param.h>
#include <sys/proc.h>

static int do_execve(struct thread *td, struct image_args *args, ...);

int
sys_execve(struct thread *td, struct execve_args *uap)
{
	...
	error = exec_copyin_args(&args, uap->fname, uap->argv, uap->envv);
	if (error == 0)
		error = kern_execve(td, &args, NULL, oldvmspace);
```

Stored with no metadata at all (the extension is enough), the document gets:

```text
language: c
defines:  do_execve, sys_execve
uses:     exec_copyin_args, kern_execve     (when defined in another file)
imports:  sys/sys/param.h, sys/sys/proc.h
```

The rules behind those three lists:

- **defines**: function definitions, `static` prototypes (the definition is in
  this file), `struct`, `union` and `enum` tags, `typedef` names, `#define`
  macros and file-scope variables. A prototype without `static` declares
  something defined elsewhere, so it does not count. Otherwise every header
  would claim every function it declares.
- **uses**: names called inside function bodies that the file does not
  define. Keywords are skipped, and so are calls through a struct member
  (`sc->ops->start(sc)`), because those are not global symbols.
- **imports**: `#include` targets.

Assembly gets a smaller version of the same: `ENTRY(copyin)` and `.globl`
become defines, `call`, `bl` and `jal` targets become uses. So when a C file
calls `copyin`, the edge leads into `amd64/amd64/support.S`.

## Include paths

`#include "opt_ktrace.h"` is relative to the including file, and MDDB resolves
it that way. `#include <sys/proc.h>` is different: the compiler finds it
through `-I`, and in a FreeBSD build the include root is `sys/`. A collection
now has the same setting:

```bash
curl -X PUT "$MDDB/v1/collection-config" -H 'Content-Type: application/json' \
  -d '{"collection":"kernel","code":{"includePaths":["sys"]}}'
```

With `["sys"]`, `<sys/proc.h>` in any file is stored as `sys/sys/proc.h`,
which matches that header's document key. Set it before loading the tree.
Documents written earlier keep their old imports until they are written
again.

## Asking the questions

Once the tree is in, the questions from the top of this post are graph
queries:

```bash
# What breaks if I change this file? Callers and includers.
curl -s "$MDDB/v1/code-graph?collection=kernel&key=sys/sys/mbuf.h&direction=in"

# What does kern_exec.c depend on?
curl -s "$MDDB/v1/code-graph?collection=kernel&key=sys/kern/kern_exec.c&direction=out"
```

Each edge has a kind and the symbol that justifies it. C files produce
`calls` edges (a use matched to a define in another file) and `imports` edges
(an include matched to a document key). The same queries are available to an
agent as the MCP tool `code_graph`. "Which file defines `do_execve`" is a plain
metadata filter:
`meta.defines=do_execve`.

## Numbers from the real tree

A scanner is only worth something if it agrees with the code, so we ran the
extractor over FreeBSD `main`'s `sys/` before shipping:

- **15,617** `.c`, `.h` and `.S` files in about **5 seconds**, single
  threaded, with no failures on malformed or unusual input.
- The extractor says **730** files include `<sys/mbuf.h>`. `grep` for
  `#include <sys/mbuf.h>` finds the same 730.
- The extractor finds **no callers** of `vm_page_alloc`. That looked like a
  bug, but `grep` agrees: on `main` the name now appears only in its
  definition, its prototype and comments. The extractor ignores comments,
  and a prototype is not a call.

That last point is the kind of answer a text search gets wrong. A search for
`vm_page_alloc` returns five files: the definition, its prototype in
`vm/vm_page.h`, and three comments. None of them is a caller.

The first run also caught a real mistake. Kernel macros invoked at file
scope, such as `static SYSCTL_NODE(...)` or `DB_SHOW_COMMAND(...) { }`, came
out as function definitions. A name written in capitals only is now treated
as a macro. A variable declared with a macro type, such as
`static SLIST_HEAD(, exec_args_kva) exec_args_kva_freelist;`, is recorded under
its own name.

## Limits

This is a scanner, not a compiler, and some answers it cannot give:

- **No preprocessing.** Both branches of an `#ifdef` are read, and nothing is
  expanded. A function whose name only appears as a macro argument is not
  recorded as a definition. For example, `DB_SHOW_COMMAND(vmpage,
  db_show_vmpage) { ... }` defines `db_show_vmpage`, and the extractor does
  not record it. Its calls are still recorded.
- **Indirect calls are not followed.** Calls through a function pointer table,
  such as `VOP_*` through a vnode's operations vector or a `DEVMETHOD` table,
  are not edges. A large part of how the kernel dispatches work is invisible
  to it.
- **The capitals rule is a convention, not a fact.** It holds in `sys/`. In a
  codebase that writes functions in capitals, those functions would be missed.
- **The lists are capped** at `MDDB_CODE_MAX_SYMBOLS` (512 by default). A
  generated register header with 90,000 `#define`s keeps its first 512.

None of this replaces cscope, ctags or clangd on your own machine. The point
is different: a model answering questions over MCP gets a first answer
grounded in the tree, with the file and symbol to read next.

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
  (`vm_page_alloc`, and also `page`). Keywords such as `if` and `for` stay in
  the index, and nothing is stemmed.
- **Embeddings**: content is split where brackets balance, so a chunk sent
  for embedding is a whole function rather than half of one.

That has worked since 2.12 for every language in the table not marked new.
Symbols are what
each language has to earn with its own extractor. If you need one for a
language we have not covered, open an issue with a real file and the questions
you want answered, the way #319 did.

## Upgrading

2.18 is backward compatible. To get symbols for C documents you already
store, write them again: symbols are extracted when a document is saved.
The full list of changes is in the
[changelog](https://github.com/tradik/mddb/blob/2.18.0/CHANGELOG.md), and the
extraction rules are in the [API reference](https://mddb.tradik.com/docs/api/).

Thanks to @spagu for a report that came with the example, the expected
output and an offer to test against the full tree. We used all three.

*FreeBSD is a registered trademark of The FreeBSD Foundation.*
