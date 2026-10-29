---
title: "Reference Coding"
slug: "docs/reference-coding"
description: "Index other projects' source code as reference collections and ask how they solved a problem before implementing it: mddb-cli reference, cross-collection hybrid search, and the reference_* MCP tools."
status: publish
---

# Reference Coding

Before implementing something, look at how a project that already did it did
it. MDDB keeps that code — someone else's, kept to learn from, never to edit —
in **reference collections**, and answers questions across all of them with
the path, lines and definition of each answer.

```
task → find a project that solved it → mddb-cli reference add → ask → implement
```

## Adding a reference

```bash
mddb-cli reference add github.com/etcd-io/bbolt
```

```
✓ ref-etcd-io-bbolt: 160 files (0.8 MB) — 160 added, 0 updated, 0 unchanged, 0 failed, 0 pruned, 1 skipped in 223ms
```

What happens, all on your machine:

1. A shallow clone (`git clone --depth 1`) into
   `$XDG_CACHE_HOME/mddb/references/<name>` (`MDDB_REFERENCES_DIR` overrides
   the location). `--ref v1.2` picks a branch or tag.
2. The collection, `ref-<owner>-<repo>` unless `--name` says otherwise, gets
   `code.role: "reference"` and a description with the repository, the commit
   and the licence: `Reference: https://github.com/etcd-io/bbolt @ a4ef19710eb7 (licence: MIT)`.
3. The clone is indexed as `mddb-cli autoindex` does it, and every document
   also carries `meta.license` and `meta.sourceRepository`.

Only `https://`, `ssh://` and `user@host:path` addresses are accepted, plus a
bare `host/owner/repo` as https. `file://`, local paths, git transport helpers
and anything starting with `-` are refused. **The server never clones and
never takes a repository address** — fetching is the client's job, which keeps
SSRF and path traversal off the server.

| Command | Does |
|---|---|
| `mddb-cli reference add <repo> [--ref R] [--name N]` | Clone and index |
| `mddb-cli reference list` | Reference collections with repository, commit and licence |
| `mddb-cli reference update [name]` | Fetch the latest commit (all references without a name) and re-index, pruning deleted files |
| `mddb-cli reference remove <name>` | Delete the collection and the clone |
| `mddb-cli reference search "<question>"` | Hybrid search over every reference, as passages |

## Asking

```bash
mddb-cli reference search "how are freelist pages released after a transaction" --top-k 2
```

```
ref-etcd-io-bbolt internal/freelist/freelist_test.go:80-100 TestFreelist_free_freelist_alloctx
    		t.Fatalf("exp=%v; got=%v", exp, f.pendingPageIds()[101].ids)
    	}
    	f.ReleasePendingPages()
ref-etcd-io-bbolt db.go:755-775
    //
    // Transactions should not be dependent on one another. Opening a read
```

Under the hood that is one request any client can send:

```bash
curl -X POST http://localhost:11023/v1/cross-search -H "Content-Type: application/json" -d '{
  "query": "how are freelist pages released after a transaction",
  "targetCollections": ["role:reference"],
  "searchType": "hybrid",
  "format": "passages"
}'
```

- `targetCollections: ["role:reference"]` stands for every collection with
  that code role; names and selectors can be mixed.
- `searchType` is `vector` (the default, as before), `fts` or `hybrid`. Full
  text and hybrid run the single-collection search in each target and fuse the
  lists by reciprocal rank — scores from different collections are not
  comparable, ranks are. Over one collection, `hybrid` returns what
  `hybrid_search` returns.
- `format: "passages"` returns path, lines, enclosing definition and text
  ([Passages](SEARCH.md#passages--what-an-agent-gets-v2188)).

## MCP tools

All read-only, in the `code` toolset, and all default to every reference
collection (`collections` narrows them):

| Tool | Answers |
|---|---|
| `reference_search` | How did they do X? — hybrid search, passages |
| `reference_symbol` | Where do they define and use this symbol? |
| `reference_examples` | Show me code that calls it — the calling definitions, with their text |
| `reference_dependencies` | What does this file import (or what imports it)? |

## Licences

Every reference document carries `meta.license`, detected from the
repository's `LICENSE` or `COPYING` file (an SPDX identifier when the text is
recognised, `see LICENSE` when it is not, `unknown` when there is no file).
**Read it before copying code.** A reference is there to learn how something
was solved; reusing its code is subject to its licence, and an agent should
say which licence applies whenever it reuses more than an idea.
