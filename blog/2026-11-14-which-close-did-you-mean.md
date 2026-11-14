---
title: "Which Close Did You Mean?"
slug: "blog/which-close-did-you-mean"
status: publish
type: post
date: 2026-11-14
tags: [search, agents, release, benchmarking]
excerpt: "MDDB 2.19.3 lets a language server index your code. code_symbol then knows which of 21 Close methods a call means, lists the types that implement an interface, and says how sure it is."
description: "MDDB 2.19.3: mddb-cli autoindex --semantic runs gopls, basedpyright or phpactor, so code_symbol answers with exact references and interface implementations."
---

Ask MDDB's own server code where `Close` is used, and the honest answer is
"which one?". Twenty-one types in mddbd have a method called `Close`: the
write-ahead log, three storage backends, two caches, an HTTP/3 server, a
syslog exporter and more. A call reads `w.Close()` or `resp.Body.Close()`. From
the text alone there is no telling which `Close` that is.

Until now `code_symbol` answered from text. It read each file on its own,
found the names a file defines and the names it uses, and matched them. That is
quick, needs nothing installed, and works for 13 programming languages. It also merges every
`Close` into one answer. 2.19.3 adds a second level: a language server reads the
whole checkout, the way your editor does, and MDDB keeps what it learns.

## Two commands

```bash
go install golang.org/x/tools/gopls@latest     # or basedpyright, or phpactor
mddb-cli autoindex . --semantic
```

The second line is the `autoindex` you know, with one flag. Before sending
anything it starts the language server for each language in the tree — gopls
for Go, basedpyright for Python, phpactor for PHP — and asks it where every
function, method, type and interface is defined, which lines refer to it, and
which types implement which interfaces. The answers travel with the documents
as metadata. Then you ask as before:

```bash
curl -s "$MDDB/v1/symbol?collection=mddbd&name=Close"
```

On mddbd's own code (1,350 Go files), the answer no longer pretends there is
one `Close`:

```json
{
  "symbol": "Close",
  "ambiguous": true,
  "candidates": [
    {"path": "adaptive_index.go", "name": "AdaptiveIndexManager.Close", "kind": "method"},
    {"path": "audit_handlers_test.go", "name": "fakeAuditExporter.Close", "kind": "method"},
    {"path": "http3.go", "name": "HTTP3Server.Close", "kind": "method"}
  ],
  "precision": "semantic",
  "semanticSource": "gopls/0.24.0"
}
```

That is three of the 21 candidates. Pick one by adding its file, and you get
that method alone:

```bash
curl -s "$MDDB/v1/symbol?collection=mddbd&name=Close&path=wal.go&include=references"
```

Without the language server, that request returns 200 references and stops at
the limit: every `Close` call anywhere in the code. With it, it returns
exactly 55. Each reference is marked `"target": "wal.go#WAL.Close"`, and they
are the same lines `gopls references` gives for that method.

## Interfaces finally have implementations

The question text matching could never answer is "what implements this?". A
struct does not mention the interface it satisfies; only the compiler knows.

```bash
curl -s "$MDDB/v1/symbol?collection=mddbd&name=backupTarget&include=implementations"
```

```text
backup_target.go       65–68    localBackupTarget  struct
backup_target_s3.go    27–31    s3BackupTarget     struct
backup_target_test.go  122–127  fakeTarget         struct
```

Before 2.19.3 that list was empty. The same works for Python's abstract
classes and protocols, and for PHP interfaces.

## How much better, measured

We drew 50 random symbols per language with a fixed seed, asked `code_symbol`
for each, and compared the result with what the language server itself
reports:

| | Name matching | Language server |
|---|---|---|
| Go: references that are right | 38.5% | **100%** |
| Go: methods with a shared name | 4.4% | **100%** |
| Go: true references found | 88.8% | 93.5% |
| Go: implementations right | 40 of 50 | **50 of 50** |
| Python and PHP: references that are right | 58% and 84% | **100%** |

The Go recall below 100% comes from the 200-per-relation limit: one struct
alone has 230 references. The Python and PHP corpora are small (14 and 39
files), so treat their numbers as a sanity check. The full method and the
commands to repeat it are in
[the benchmark notes](/docs/benchmark/#symbol-lookup-heuristics-vs-language-server-facts-v2193).

## What it costs

- **Time.** Without the flag, indexing mddbd took 2.4 seconds. With it, 123
  seconds, two minutes of which is gopls type-checking 1,350 files. A second
  run with nothing changed still runs gopls, but sends nothing.
- **Memory on the machine running the CLI.** gopls peaked at 4–5 GB here.
  Languages run one after another, so the peak is the largest server, not the
  sum. mddbd itself starts no language server and needs no extra memory.
- **Space.** The facts add about 1.2 KB per source file (median), and they
  never show up in search results.

## How sure is the answer?

Every answer carries `precision`. `semantic` means everything came from the
language server. `mixed` means part of the collection was indexed without one,
for example a language with no server installed. `heuristic` means name
matching, as before. Facts describe the content they were computed from: edit
a file through the API and its facts are dropped until the next
`autoindex --semantic`. A collection indexed without the flag answers exactly
as it did in 2.19.2.

One thing is still missing. A bare name like `Close` finds 21 of the 23
definitions, not all of them: the two interface methods only show up when you
name their file. That is next on the list.

## Getting it

- Upgrade mddbd and mddb-cli to 2.19.3.
- Install a language server for each language you index; `mddb-cli diagnose`
  shows which ones it finds.
- Run `mddb-cli autoindex . --semantic` where the code builds, because the
  server needs the project's toolchain and dependencies.
- Agents call the `code_symbol` MCP tool with the same parameters. The panel's
  Code Symbols page shows the precision, the candidates and the
  implementations.

The whole picture, including troubleshooting, is in the
[Code Intelligence guide](/docs/code-intelligence/).
