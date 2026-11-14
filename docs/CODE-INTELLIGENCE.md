---
title: "Code Intelligence"
slug: "docs/code-intelligence"
description: "How MDDB answers where a symbol is defined, who calls it and what implements it: heuristic symbols for 13 programming languages, and precise facts from gopls, basedpyright, phpactor and clangd with mddb-cli autoindex --semantic."
status: publish
---

# Code Intelligence

MDDB can tell an agent where a function is defined, which lines of which files
use it, what it calls, which tests touch it and — since 2.19.3 — which types
implement an interface. It answers on two levels of precision, through the same
`code_symbol` lookup.

| | Heuristic (always on) | Semantic (`autoindex --semantic`, 2.19.3+) |
|---|---|---|
| Languages | 13: Go, Python, PHP, JS/TS, Rust, Java, C/C++, Ruby, CSS, Terraform, YAML, HTML, assembly | Go, Python, PHP, C, C++ (2.19.4) |
| Where it comes from | mddbd reads each file alone with scanners | a language server reads the whole checkout, with its dependencies |
| Definitions | name and span | qualified name (`Conn.Close`), kind, span, enclosing type |
| References | name matches, plus full-text hits for `x.f()` calls | the exact lines that refer to this definition |
| Same name, several definitions | one merged answer | `ambiguous: true` and a list of candidates |
| Callers, callees, tests | from the spans around each match | from the facts, per definition |
| Implementations | — | interfaces, abstract classes, protocols, C++ subclasses and overrides |
| Needs | nothing | a language server on the machine running `mddb-cli` |
| `precision` in the answer | omitted, or `heuristic` | `semantic`, or `mixed` when only part of the collection has facts |

The heuristic level guesses where text cannot decide. In mddbd's own Go code,
54% of call sites go through a value or a package (`s.Close()`, `pkg.Run()`),
and 470 names are defined in more than one file (`Close` 19 times, `Stats`
30). The semantic level knows which `Close` each call means.

## Quick start

```bash
# 1. A language server for each language you index (see the table below).
go install golang.org/x/tools/gopls@latest

# 2. Index the checkout with facts. Run it where the code builds: the language
#    server needs the project's toolchain and dependencies.
mddb-cli autoindex . --semantic

# 3. Ask.
curl -s "$MDDB/v1/symbol?collection=myrepo&name=Close"
curl -s "$MDDB/v1/symbol?collection=myrepo&name=Store&include=implementations"
```

Agents use the MCP tool `code_symbol` with the same parameters, and the panel's
Code Symbols page shows the same answer. Run `mddb-cli autoindex . --semantic`
again after changes: it sends only the documents whose content or facts
changed.

## Language servers

`mddb-cli` uses the first server it finds on `PATH`, one language at a time.
`mddb-cli diagnose` lists the ones it found, with their versions.

| Language | Server (preferred first) | Install | Licence | Override |
|---|---|---|---|---|
| Go | gopls | `go install golang.org/x/tools/gopls@latest` | BSD-3-Clause | `MDDB_LSP_GO` |
| Python | basedpyright, else pyrefly | `uv tool install basedpyright` (or `pip install basedpyright`; Node comes with it) / `uv tool install pyrefly` | MIT | `MDDB_LSP_PYTHON` |
| PHP | phpactor, else PHPantom | the `phpactor.phar` from the phpactor releases (PHP 8.2+), or a PHPantom release binary | MIT | `MDDB_LSP_PHP` |
| C, C++ | clangd (23 or later) | the static `clangd-<os>-<version>.zip` from the clangd releases, or the `clangd` package of the distribution or LLVM; needs the build's `compile_commands.json` | Apache-2.0 WITH LLVM-exception | `MDDB_LSP_C`, `MDDB_LSP_CPP` |

Versions tested on 2026-11-14: gopls 0.24.0, basedpyright 1.40.2, pyrefly
1.4.1, phpactor 2026.10.07.0, PHPantom 0.11.1. basedpyright comes before pyrefly
because pyrefly 1.4.1 returns no implementations of classes. An `MDDB_LSP_*`
variable replaces the built-in command (split on spaces, no shell), e.g.
`MDDB_LSP_PHP="php /opt/phpactor.phar language-server"`.

### C and C++ (clangd, 2.19.4)

clangd 23.1.0 was tested on 2026-11-14. It runs as
`clangd --background-index --log=info --compile-commands-dir=<dir>`; the last
flag is added whenever the program is clangd, also when `MDDB_LSP_C` names it
elsewhere (keep `--log=info` in an override: the check below reads it).

- **compile_commands.json is required.** clangd learns each file's include
  paths and defines from the build's compilation database. `autoindex` looks
  for it in the root, then `<root>/build`, or takes `--compile-commands
  <file-or-dir>`. Generate one with CMake
  (`-DCMAKE_EXPORT_COMPILE_COMMANDS=ON`), `bear -- make`, or Meson/Ninja
  (which write it). Without one, `auto` warns and the C and C++ files keep the
  heuristics; `--semantic=c` stops before anything is written.
- **Guessed flags are refused.** If clangd still compiles an indexed file
  without the database (it logs `Failed to find compilation database for …`
  or `with command clangd fallback`), the run returns no facts for C and C++:
  answers from guessed flags are partial and nothing in the protocol says so.
- **One server for both.** A C++ project's `.h` headers count as C, so when
  both languages are indexed they run in one clangd (`c+cpp` in the report)
  and references between headers and `.cpp` files are kept.
- **Names.** `geo::Square::area` is stored as `geo.Square.area`; namespaces
  prefix names but are not containers; anonymous namespaces add nothing;
  template arguments are dropped (`List<T>::push` is `List.push`). A function
  is defined where its body is: header prototypes and in-class member
  declarations are not definitions (pure virtual methods are). `typedef` and
  `using` aliases are `type`.
- **Implementations** are asked for C++ classes and methods only (subclasses
  and overrides); C has neither, so nothing is asked.
- clangd waits for its background index to finish before references are
  asked, however long that takes within `--semantic-timeout`; raise it for a
  large tree. The index is kept in `.cache/clangd/` next to the database,
  which makes the next run faster; add it to `.gitignore`.
- Cross-compilers: add `--query-driver=<glob>` through `MDDB_LSP_C`.

## Flags

```bash
mddb-cli autoindex . --semantic                # auto: every language it can do
mddb-cli autoindex . --semantic=go,python      # these languages, or stop
mddb-cli autoindex . --semantic --dry-run      # run the servers, report, send nothing
mddb-cli autoindex . --semantic=c,cpp --compile-commands build/   # C and C++
```

- **auto** indexes each language that is in the tree, that the server accepts
  facts for (`semantic` in `GET /v1/code-languages`) and that has a language
  server installed. A missing or failing server is a warning; those files keep
  the heuristic level.
- **A list** is a requirement: a missing server, a failing server or an mddbd
  older than 2.19.3 stops the run before anything is written.
- `--semantic-timeout` (default `20m`), `--semantic-concurrency` (`4`),
  `--semantic-memory-mb` (gopls only), `--compile-commands` (clangd). Full reference:
  [Bulk Import](BULK-IMPORT.md#semantic-facts-from-a-language-server---semantic-v2193).

## Reading an answer

```json
{
  "symbol": "Close",
  "ambiguous": true,
  "candidates": [
    {"path": "internal/net/conn.go", "name": "Conn.Close", "kind": "method"},
    {"path": "store/file.go", "name": "File.Close", "kind": "method"}
  ],
  "precision": "semantic",
  "semanticSource": "gopls/0.24.0"
}
```

- **`ambiguous`** — several definitions match a bare name. Ask again with the
  candidate's `name` (`File.Close`) and `path`; the answer then lists only that
  method's references, callers and callees.
- **`target`** on each reference says which definition it belongs to.
- **`precision`**: `semantic` means every definition and use came from facts;
  `mixed` means some files were indexed without them (for example a language
  without a server); `heuristic` means none. It is omitted from a heuristic
  answer to a request that did not ask for `path` or `implementations`, so
  older clients see exactly the answers they saw before.
- **`implementations`** is empty without facts — that is the answer "not
  known", not "none".
- **Known limit:** without `path`, the candidates for a bare name come from
  the heuristic `defines` list, so interface methods and names in very large
  files (past its 512 names) are missing from them — `Close` in mddbd lists 21
  of 23. With `path` every definition in that file is found from the facts.
- **References through interfaces** count: gopls reports a call through
  `io.Closer` as a reference to every `Close` that satisfies it, and so does
  the answer.

Full field list: [API](API.md#precise-answers-from-semantic-facts-v2193),
[MCP](MCP.md), [GraphQL](GRAPHQL.md), [gRPC](GRPC.md). In
`retrievalMode: graph`, `graphKinds` accepts `implementations`
([Search](SEARCH.md)).

## How much better

Measured on 50 random symbols per language (fixed seed), against what the
language server itself reports; method and commands in
[Benchmark](BENCHMARK.md#symbol-lookup-heuristics-vs-language-server-facts-v2193).

| Language | References correct (heuristic → semantic) | References found | Implementations right |
|---|---|---|---|
| Go (1349 files) | 38.5% → **100%** | 88.8% → 93.5% (the rest is the 200-per-relation limit) | 40 → **50** of 50 |
| Go, methods with a shared name | 4.4% → **100%** | 58.8% → 100% | |
| Python (14 files) | 57.8% → **100%** | 70.5% → 100% | |
| PHP (39 files) | 84.2% → **100%** | 91.7% → 100% | |

The heuristic answer for `Close` in mddbd returned 681 lines for 51 true ones;
the semantic answer lists 21 candidates and, for `WAL.Close`, exactly the 55
lines gopls reports. Answers are about as large in tokens and no slower
(around 200 ms per lookup on the Go collection, either way). Search results
are unchanged.

## Cost

Measured on mddbd's own code (1343 Go files, 300 000 lines), gopls 0.24.0:

| | |
|---|---|
| First run | about 2.5 minutes; 18 272 definitions, 89 551 reference lines, 192 implementations |
| Second run, nothing changed | the language server runs again, 0 documents sent |
| Language server memory | 3.3–5.5 GB peak (gopls) |
| Facts in meta | median 1.2 KB, p99 9.2 KB per document; +55–65 MB on disk for the collection |
| Without `--semantic` | unchanged from 2.19.2 |

Languages run one after another, so the peak is the largest single server, not
the sum. Facts are recomputed for the whole tree on every `--semantic` run.

## When facts go stale

Facts describe the content they were computed from. Each document's `symIndex`
holds a hash of that content, and mddbd drops the facts the moment the content
changes through any API: the answer falls back to `heuristic` (or `mixed`)
until the next `autoindex --semantic`. Facts are never shown in search results;
reading a single document returns them.

## Troubleshooting

| Message or symptom | Cause | Fix |
|---|---|---|
| `language server did not load the workspace: N of M references requests failed` | the server could not build the project — most often a wrong toolchain (`go.work requires go >= 1.27`) or missing dependencies (`vendor/`, a virtualenv, `composer install`) | fix `PATH`/`GOTOOLCHAIN` or install the dependencies, then run again; no facts are sent from a half-loaded project |
| `language server not found for python: none of [...] is on PATH; ... python files keep the heuristics` | none of the servers is on `PATH` | install one, or set `MDDB_LSP_PYTHON` |
| `--semantic=php` stops with `semantic facts for php: ...` | a listed language is required | install the server, or use `--semantic` (auto) |
| `no compilation database (compile_commands.json) in <root> or <root>/build; ...` | clangd has no build flags for the tree | generate `compile_commands.json`, or name it with `--compile-commands` |
| `language server compiled files without the project's build settings: a.c, ...` | clangd fell back to guessed flags for those files (not in the database, or an override without `--log=info` hid the check — then this message never appears) | regenerate the database from a build that compiles those files, or pass the right `--compile-commands`; no C/C++ facts are sent until then |
| `the server keeps no semantic facts (it predates 2.19.3)` | mddbd is older than the CLI | upgrade mddbd |
| `precision: mixed` | some files have no facts | index those languages with a server, or accept the mix |
| `symIndex` ends in ` truncated` | the file had more than `MDDB_CODE_MAX_FACTS` (4096) facts | references were cut first; raise the limit on mddbd if needed |

## What MDDB does not do

MDDB does not run language servers inside mddbd, and it offers no live
language-server tools (hover, rename, diagnostics). Those belong in an editor
or an agent working on the checkout, which has the files and the toolchain.
MDDB keeps the facts it was given, so any client — a remote agent over MCP, the
chat, CI, the panel — can ask about the code without a checkout.

`autoindex --semantic` is not offered over MCP: starting language servers from
a tool call is a separate security decision.

## Adding a language

The language-server table is one place in mddb-cli
(`services/mddb-cli/internal/lspindex/servers.go`). A new language is a new
entry — the command, the language id, how its symbol names nest (clangd's
hooks are in `servers_clangd.go`) — plus a test
against the fake server, and `semantic: true` for it in mddbd's
`/v1/code-languages`. The fact format is in
[testdata/semantic-facts/README.md](https://github.com/tradik/mddb/blob/main/testdata/semantic-facts/README.md).
