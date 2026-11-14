---
title: "Code Intelligence"
slug: "docs/code-intelligence"
description: "How MDDB answers where a symbol is defined, who calls it and what implements it: heuristic symbols for 13 languages, and precise facts from gopls, basedpyright and phpactor with mddb-cli autoindex --semantic."
status: publish
---

# Code Intelligence

MDDB can tell an agent where a function is defined, which lines of which files
use it, what it calls, which tests touch it and — since 2.19.3 — which types
implement an interface. It answers on two levels of precision, through the same
`code_symbol` lookup.

| | Heuristic (always on) | Semantic (`autoindex --semantic`, 2.19.3+) |
|---|---|---|
| Languages | 13: Go, Python, PHP, JS/TS, Rust, Java, C/C++, Ruby, CSS, Terraform, YAML, HTML, assembly | Go, Python, PHP |
| Where it comes from | mddbd reads each file alone with scanners | a language server reads the whole checkout, with its dependencies |
| Definitions | name and span | qualified name (`Conn.Close`), kind, span, enclosing type |
| References | name matches, plus full-text hits for `x.f()` calls | the exact lines that refer to this definition |
| Same name, several definitions | one merged answer | `ambiguous: true` and a list of candidates |
| Callers, callees, tests | from the spans around each match | from the facts, per definition |
| Implementations | — | interfaces, abstract classes, protocols |
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

Versions tested on 2026-11-14: gopls 0.24.0, basedpyright 1.40.2, pyrefly
1.4.1, phpactor 2026.10.07.0, PHPantom 0.11.1. basedpyright comes before pyrefly
because pyrefly 1.4.1 returns no implementations of classes. An `MDDB_LSP_*`
variable replaces the built-in command (split on spaces, no shell), e.g.
`MDDB_LSP_PHP="php /opt/phpactor.phar language-server"`.

## Flags

```bash
mddb-cli autoindex . --semantic                # auto: every language it can do
mddb-cli autoindex . --semantic=go,python      # these languages, or stop
mddb-cli autoindex . --semantic --dry-run      # run the servers, report, send nothing
```

- **auto** indexes each language that is in the tree, that the server accepts
  facts for (`semantic` in `GET /v1/code-languages`) and that has a language
  server installed. A missing or failing server is a warning; those files keep
  the heuristic level.
- **A list** is a requirement: a missing server, a failing server or an mddbd
  older than 2.19.3 stops the run before anything is written.
- `--semantic-timeout` (default `20m`), `--semantic-concurrency` (`4`),
  `--semantic-memory-mb` (gopls only). Full reference:
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

Full field list: [API](API.md#precise-answers-from-semantic-facts-v2193),
[MCP](MCP.md), [GraphQL](GRAPHQL.md), [gRPC](GRPC.md). In
`retrievalMode: graph`, `graphKinds` accepts `implementations`
([Search](SEARCH.md)).

## Cost

Measured on mddbd's own code (1343 Go files, 300 000 lines), gopls 0.24.0:

| | |
|---|---|
| First run | about 2.5 minutes; 18 272 definitions, 89 551 reference lines, 192 implementations |
| Second run, nothing changed | the language server runs again, 0 documents sent |
| Language server memory | 3.5–5.5 GB peak (gopls) |
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
| `the server keeps no semantic facts (it predates 2.19.3)` | mddbd is older than the CLI | upgrade mddbd |
| `precision: mixed` | some files have no facts | index those languages with a server, or accept the mix |
| `symIndex` ends in ` truncated` | the file had more than `MDDB_CODE_MAX_FACTS` (2048) facts | references were cut first; raise the limit on mddbd if needed |

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
entry — the command, the language id, how its symbol names nest — plus a test
against the fake server, and `semantic: true` for it in mddbd's
`/v1/code-languages`. The fact format is in
[testdata/semantic-facts/README.md](https://github.com/tradik/mddb/blob/main/testdata/semantic-facts/README.md).
