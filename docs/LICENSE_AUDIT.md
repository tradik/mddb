---
title: "License Audit Report"
slug: "docs/license-audit"
description: "License audit of every MDDB dependency across Go modules and npm packages, with the distribution obligations each licence carries."
status: publish
---

# License Audit Report

**Project:** MDDB (Markdown Database)  
**Date:** 2026-11-23  
**Tool:** [Trivy](https://trivy.dev/) 0.76.2 — filesystem license scanner  
**Scan command:** `trivy fs --scanners license --skip-dirs dist --skip-dirs audit --skip-dirs '**/node_modules' .`  
**Replaces:** the scan of 2026-03-10 and its three amendments. This is a full re-scan of the tree at MDDB 2.19.13.

---

## MDDB's own licence

This report is about the licences MDDB *consumes*. The licence MDDB is
*distributed under* changed in 2.19.0: the server, CLI, panel and chat service
are under the Business Source License 1.1 (`LICENSE`), the client libraries,
integrations and protocol definitions stay BSD-3-Clause under their own
`LICENSE` files. The map is in [LICENSING.md](https://github.com/tradik/mddb/blob/main/LICENSING.md).
None of the dependencies below constrains that choice: there is no GPL or
AGPL code in the tree, and the two MPL-2.0 libraries carry a file-level
obligation that stops at their own files.

## Executive Summary

Trivy read **16 lockfiles and module files** and reported licences for **8** of them: **332 packages**, 342 licence entries (a package under two licences counts twice).

| Metric | Value |
|--------|-------|
| Packages with a licence reported | 332 |
| Licence entries | 342 |
| CRITICAL issues | 0 |
| HIGH issues | 0 |
| MEDIUM issues | 2 |
| LOW issues | 339 |
| Not classified by the scanner | 1 |
| Permissive (notice) | 337 (98.5%) |
| Reciprocal | 2 (0.6%) |
| Public domain (unencumbered) | 2 (0.6%) |

**Overall risk: LOW** — no HIGH or CRITICAL licence issue, and no GPL or AGPL licence, in what the scan covers.

### What the scan does not cover

Trivy found these files and reported no licence for any package in them. Their dependencies are **not audited** by this report:

| File | Ecosystem |
|------|-----------|
| clients/go/mddb/go.mod | Go |
| clients/python/uv.lock | Python |
| integrations/langchain-mddb/uv.lock | Python |
| services/mddb-chat/Cargo.lock | Rust |
| test/es-compat/uv.lock | Python |
| test/qdrant-compat/uv.lock | Python |
| tools/bench/go.mod | Go |
| tools/bench/soak/go.mod | Go |

The scan ran on a checkout without installed Python and Rust packages, and Trivy reported nothing for `uv.lock` and `Cargo.lock` in that state. The three Go modules in the list returned no licence entries either.

---

## License Distribution

| License | Count | Category | Commercial Use |
|---------|-------|----------|----------------|
| MIT | 251 | notice | Yes |
| BSD-3-Clause | 42 | notice | Yes |
| Apache-2.0 | 27 | notice | Yes |
| ISC | 12 | notice | Yes |
| BSD-2-Clause | 5 | notice | Yes |
| MPL-2.0 | 2 | reciprocal | Yes (with conditions) |
| 0BSD | 1 | unencumbered | Yes |
| BlueOak-1.0.0 | 1 | unknown | Not classified by the scanner |
| CC0-1.0 | 1 | unencumbered | Yes |

---

## Scan Targets

### services/mddb-cli/go.mod

**3 packages**

| Package | License | Severity | Category |
|---------|---------|----------|----------|
| github.com/inconshreveable/mousetrap | Apache-2.0 | LOW | notice |
| github.com/spf13/cobra | Apache-2.0 | LOW | notice |
| github.com/spf13/pflag | BSD-3-Clause | LOW | notice |

### services/mddbd/go.mod

**45 packages**

| Package | License | Severity | Category |
|---------|---------|----------|----------|
| github.com/99designs/gqlgen | MIT | LOW | notice |
| github.com/agnivade/levenshtein | MIT | LOW | notice |
| github.com/bits-and-blooms/bitset | BSD-3-Clause | LOW | notice |
| github.com/bits-and-blooms/bloom/v3 | BSD-2-Clause | LOW | notice |
| github.com/blevesearch/snowballstem | BSD-3-Clause | LOW | notice |
| github.com/cespare/xxhash/v2 | MIT | LOW | notice |
| github.com/coder/websocket | ISC | LOW | notice |
| github.com/dustin/go-humanize | MIT | LOW | notice |
| github.com/go-viper/mapstructure/v2 | MIT | LOW | notice |
| github.com/goccy/go-json | MIT | LOW | notice |
| github.com/golang-jwt/jwt/v5 | MIT | LOW | notice |
| github.com/golang/snappy | BSD-3-Clause | LOW | notice |
| github.com/google/uuid | BSD-3-Clause | LOW | notice |
| github.com/hashicorp/golang-lru/v2 | MPL-2.0 | MEDIUM | reciprocal |
| github.com/klauspost/compress | Apache-2.0 / BSD-3-Clause / MIT | LOW | notice |
| github.com/klauspost/cpuid/v2 | MIT | LOW | notice |
| github.com/klauspost/crc32 | BSD-3-Clause | LOW | notice |
| github.com/kr/pretty | MIT | LOW | notice |
| github.com/minio/crc64nvme | Apache-2.0 | LOW | notice |
| github.com/minio/md5-simd | Apache-2.0 | LOW | notice |
| github.com/minio/minio-go/v7 | Apache-2.0 | LOW | notice |
| github.com/philhofer/fwd | MIT | LOW | notice |
| github.com/pires/go-proxyproto | Apache-2.0 | LOW | notice |
| github.com/quic-go/qpack | MIT | LOW | notice |
| github.com/quic-go/quic-go | MIT | LOW | notice |
| github.com/robfig/cron/v3 | MIT | LOW | notice |
| github.com/rs/xid | MIT | LOW | notice |
| github.com/sosodev/duration | MIT | LOW | notice |
| github.com/tidwall/geoindex | MIT | LOW | notice |
| github.com/tidwall/rtree | MIT | LOW | notice |
| github.com/tinylib/msgp | MIT | LOW | notice |
| github.com/vektah/gqlparser/v2 | MIT | LOW | notice |
| github.com/zeebo/xxh3 | BSD-2-Clause | LOW | notice |
| go.yaml.in/yaml/v3 | Apache-2.0 / MIT | LOW | notice |
| golang.org/x/crypto | BSD-3-Clause | LOW | notice |
| golang.org/x/net | BSD-3-Clause | LOW | notice |
| golang.org/x/sync | BSD-3-Clause | LOW | notice |
| golang.org/x/sys | BSD-3-Clause | LOW | notice |
| golang.org/x/text | BSD-3-Clause | LOW | notice |
| google.golang.org/genproto/googleapis/rpc | Apache-2.0 | LOW | notice |
| google.golang.org/grpc | Apache-2.0 | LOW | notice |
| google.golang.org/protobuf | BSD-3-Clause | LOW | notice |
| gopkg.in/check.v1 | BSD-2-Clause | LOW | notice |
| gopkg.in/ini.v1 | Apache-2.0 | LOW | notice |
| gopkg.in/yaml.v3 | Apache-2.0 / MIT | LOW | notice |

### services/mddbd/third_party/bbolt/go.mod

**14 packages**

| Package | License | Severity | Category |
|---------|---------|----------|----------|
| github.com/aclements/go-moremath | BSD-3-Clause | LOW | notice |
| github.com/davecgh/go-spew | ISC | LOW | notice |
| github.com/inconshreveable/mousetrap | Apache-2.0 | LOW | notice |
| github.com/pmezard/go-difflib | BSD-3-Clause | LOW | notice |
| github.com/spf13/cobra | Apache-2.0 | LOW | notice |
| github.com/spf13/pflag | BSD-3-Clause | LOW | notice |
| github.com/stretchr/testify | MIT | LOW | notice |
| go.etcd.io/gofail | Apache-2.0 | LOW | notice |
| golang.org/x/mod | BSD-3-Clause | LOW | notice |
| golang.org/x/perf | BSD-3-Clause | LOW | notice |
| golang.org/x/sync | BSD-3-Clause | LOW | notice |
| golang.org/x/sys | BSD-3-Clause | LOW | notice |
| golang.org/x/tools | BSD-3-Clause | LOW | notice |
| gopkg.in/yaml.v3 | Apache-2.0 / MIT | LOW | notice |

### test/go.mod

**19 packages**

| Package | License | Severity | Category |
|---------|---------|----------|----------|
| filippo.io/edwards25519 | BSD-3-Clause | LOW | notice |
| github.com/go-sql-driver/mysql | MPL-2.0 | MEDIUM | reciprocal |
| github.com/golang/snappy | BSD-3-Clause | LOW | notice |
| github.com/klauspost/compress | Apache-2.0 / BSD-3-Clause / MIT | LOW | notice |
| github.com/lib/pq | MIT | LOW | notice |
| github.com/montanaflynn/stats | MIT | LOW | notice |
| github.com/xdg-go/pbkdf2 | Apache-2.0 | LOW | notice |
| github.com/xdg-go/scram | Apache-2.0 | LOW | notice |
| github.com/xdg-go/stringprep | Apache-2.0 | LOW | notice |
| github.com/youmark/pkcs8 | MIT | LOW | notice |
| go.mongodb.org/mongo-driver | Apache-2.0 | LOW | notice |
| golang.org/x/crypto | BSD-3-Clause | LOW | notice |
| golang.org/x/net | BSD-3-Clause | LOW | notice |
| golang.org/x/sync | BSD-3-Clause | LOW | notice |
| golang.org/x/sys | BSD-3-Clause | LOW | notice |
| golang.org/x/text | BSD-3-Clause | LOW | notice |
| google.golang.org/genproto/googleapis/rpc | Apache-2.0 | LOW | notice |
| google.golang.org/grpc | Apache-2.0 | LOW | notice |
| google.golang.org/protobuf | BSD-3-Clause | LOW | notice |

### clients/nodejs/package-lock.json

**33 packages**

| Package | License | Severity | Category |
|---------|---------|----------|----------|
| @grpc/grpc-js | Apache-2.0 | LOW | notice |
| @grpc/proto-loader | Apache-2.0 / Apache-2.0 | LOW | notice |
| @js-sdsl/ordered-map | MIT | LOW | notice |
| @protobufjs/aspromise | BSD-3-Clause | LOW | notice |
| @protobufjs/base64 | BSD-3-Clause | LOW | notice |
| @protobufjs/codegen | BSD-3-Clause | LOW | notice |
| @protobufjs/eventemitter | BSD-3-Clause | LOW | notice |
| @protobufjs/fetch | BSD-3-Clause | LOW | notice |
| @protobufjs/float | BSD-3-Clause | LOW | notice |
| @protobufjs/path | BSD-3-Clause | LOW | notice |
| @protobufjs/pool | BSD-3-Clause | LOW | notice |
| @protobufjs/utf8 | BSD-3-Clause | LOW | notice |
| @types/node | MIT | LOW | notice |
| ansi-regex | MIT | LOW | notice |
| ansi-styles | MIT | LOW | notice |
| cliui | ISC | LOW | notice |
| color-convert | MIT | LOW | notice |
| color-name | MIT | LOW | notice |
| emoji-regex | MIT | LOW | notice |
| escalade | MIT | LOW | notice |
| get-caller-file | ISC | LOW | notice |
| is-fullwidth-code-point | MIT | LOW | notice |
| lodash.camelcase | MIT | LOW | notice |
| long | Apache-2.0 | LOW | notice |
| protobufjs | BSD-3-Clause | LOW | notice |
| require-directory | MIT | LOW | notice |
| string-width | MIT | LOW | notice |
| strip-ansi | MIT | LOW | notice |
| undici-types | MIT | LOW | notice |
| wrap-ansi | MIT | LOW | notice |
| y18n | ISC | LOW | notice |
| yargs | MIT | LOW | notice |
| yargs-parser | ISC | LOW | notice |

### integrations/github-action/package-lock.json

**10 packages**

| Package | License | Severity | Category |
|---------|---------|----------|----------|
| @actions/core | MIT | LOW | notice |
| @actions/exec | MIT | LOW | notice |
| @actions/glob | MIT | LOW | notice |
| @actions/http-client | MIT | LOW | notice |
| @actions/io | MIT | LOW | notice |
| balanced-match | MIT | LOW | notice |
| brace-expansion | MIT | LOW | notice |
| minimatch | BlueOak-1.0.0 | UNKNOWN | unknown |
| tunnel | MIT | LOW | notice |
| undici | MIT / MIT | LOW | notice |

### integrations/grafana-datasource/package-lock.json

**1 packages**

| Package | License | Severity | Category |
|---------|---------|----------|----------|
| tslib | 0BSD | LOW | unencumbered |

### services/mddb-panel/package-lock.json

**207 packages**

| Package | License | Severity | Category |
|---------|---------|----------|----------|
| @0no-co/graphql.web | MIT | LOW | notice |
| @babel/runtime | MIT | LOW | notice |
| @types/debug | MIT | LOW | notice |
| @types/estree | MIT | LOW | notice |
| @types/estree-jsx | MIT | LOW | notice |
| @types/hast | MIT | LOW | notice |
| @types/mdast | MIT | LOW | notice |
| @types/ms | MIT | LOW | notice |
| @types/prismjs | MIT | LOW | notice |
| @types/react | MIT | LOW | notice |
| @types/unist | MIT / MIT | LOW | notice |
| @ungap/structured-clone | ISC | LOW | notice |
| @urql/core | MIT | LOW | notice |
| accepts | MIT | LOW | notice |
| bail | MIT | LOW | notice |
| body-parser | MIT | LOW | notice |
| braces | MIT | LOW | notice |
| bytes | MIT | LOW | notice |
| call-bind-apply-helpers | MIT | LOW | notice |
| call-bound | MIT | LOW | notice |
| ccount | MIT | LOW | notice |
| character-entities | MIT | LOW | notice |
| character-entities-html4 | MIT | LOW | notice |
| character-entities-legacy | MIT | LOW | notice |
| character-reference-invalid | MIT | LOW | notice |
| comma-separated-tokens | MIT | LOW | notice |
| content-disposition | MIT | LOW | notice |
| content-type | MIT | LOW | notice |
| cookie | MIT | LOW | notice |
| cookie-signature | MIT | LOW | notice |
| csstype | MIT | LOW | notice |
| date-fns | MIT | LOW | notice |
| debug | MIT | LOW | notice |
| decode-named-character-reference | MIT | LOW | notice |
| depd | MIT | LOW | notice |
| dequal | MIT | LOW | notice |
| devlop | MIT | LOW | notice |
| dunder-proto | MIT | LOW | notice |
| ee-first | MIT | LOW | notice |
| encodeurl | MIT | LOW | notice |
| entities | BSD-2-Clause | LOW | notice |
| es-define-property | MIT | LOW | notice |
| es-errors | MIT | LOW | notice |
| es-object-atoms | MIT | LOW | notice |
| escape-html | MIT | LOW | notice |
| escape-string-regexp | MIT | LOW | notice |
| estree-util-is-identifier-name | MIT | LOW | notice |
| etag | MIT | LOW | notice |
| express | MIT | LOW | notice |
| extend | MIT | LOW | notice |
| fault | MIT | LOW | notice |
| fill-range | MIT | LOW | notice |
| finalhandler | MIT | LOW | notice |
| forwarded | MIT | LOW | notice |
| fresh | MIT | LOW | notice |
| function-bind | MIT | LOW | notice |
| get-intrinsic | MIT | LOW | notice |
| get-proto | MIT | LOW | notice |
| gopd | MIT | LOW | notice |
| has-symbols | MIT | LOW | notice |
| hasown | MIT | LOW | notice |
| hast-util-from-parse5 | MIT | LOW | notice |
| hast-util-parse-selector | MIT | LOW | notice |
| hast-util-raw | MIT | LOW | notice |
| hast-util-sanitize | MIT | LOW | notice |
| hast-util-to-jsx-runtime | MIT | LOW | notice |
| hast-util-to-parse5 | MIT | LOW | notice |
| hast-util-whitespace | MIT | LOW | notice |
| hastscript | MIT | LOW | notice |
| highlight.js | BSD-3-Clause | LOW | notice |
| highlightjs-vue | CC0-1.0 | LOW | unencumbered |
| html-url-attributes | MIT | LOW | notice |
| html-void-elements | MIT | LOW | notice |
| http-errors | MIT | LOW | notice |
| http-proxy-middleware | MIT | LOW | notice |
| httpxy | MIT | LOW | notice |
| iconv-lite | MIT | LOW | notice |
| inherits | ISC | LOW | notice |
| inline-style-parser | MIT | LOW | notice |
| ipaddr.js | MIT | LOW | notice |
| is-alphabetical | MIT | LOW | notice |
| is-alphanumerical | MIT | LOW | notice |
| is-decimal | MIT | LOW | notice |
| is-extglob | MIT | LOW | notice |
| is-glob | MIT | LOW | notice |
| is-hexadecimal | MIT | LOW | notice |
| is-number | MIT | LOW | notice |
| is-plain-obj | MIT | LOW | notice |
| is-promise | MIT | LOW | notice |
| leaflet | BSD-2-Clause | LOW | notice |
| longest-streak | MIT | LOW | notice |
| lowlight | MIT | LOW | notice |
| lucide-react | ISC | LOW | notice |
| markdown-table | MIT | LOW | notice |
| math-intrinsics | MIT | LOW | notice |
| mdast-util-find-and-replace | MIT | LOW | notice |
| mdast-util-from-markdown | MIT | LOW | notice |
| mdast-util-gfm | MIT | LOW | notice |
| mdast-util-gfm-autolink-literal | MIT | LOW | notice |
| mdast-util-gfm-footnote | MIT | LOW | notice |
| mdast-util-gfm-strikethrough | MIT | LOW | notice |
| mdast-util-gfm-table | MIT | LOW | notice |
| mdast-util-gfm-task-list-item | MIT | LOW | notice |
| mdast-util-mdx-expression | MIT | LOW | notice |
| mdast-util-mdx-jsx | MIT | LOW | notice |
| mdast-util-mdxjs-esm | MIT | LOW | notice |
| mdast-util-phrasing | MIT | LOW | notice |
| mdast-util-to-hast | MIT | LOW | notice |
| mdast-util-to-markdown | MIT | LOW | notice |
| mdast-util-to-string | MIT | LOW | notice |
| media-typer | MIT | LOW | notice |
| merge-descriptors | MIT | LOW | notice |
| micromark | MIT | LOW | notice |
| micromark-core-commonmark | MIT | LOW | notice |
| micromark-extension-gfm | MIT | LOW | notice |
| micromark-extension-gfm-autolink-literal | MIT | LOW | notice |
| micromark-extension-gfm-footnote | MIT | LOW | notice |
| micromark-extension-gfm-strikethrough | MIT | LOW | notice |
| micromark-extension-gfm-table | MIT | LOW | notice |
| micromark-extension-gfm-tagfilter | MIT | LOW | notice |
| micromark-extension-gfm-task-list-item | MIT | LOW | notice |
| micromark-factory-destination | MIT | LOW | notice |
| micromark-factory-label | MIT | LOW | notice |
| micromark-factory-space | MIT | LOW | notice |
| micromark-factory-title | MIT | LOW | notice |
| micromark-factory-whitespace | MIT | LOW | notice |
| micromark-util-character | MIT | LOW | notice |
| micromark-util-chunked | MIT | LOW | notice |
| micromark-util-classify-character | MIT | LOW | notice |
| micromark-util-combine-extensions | MIT | LOW | notice |
| micromark-util-decode-numeric-character-reference | MIT | LOW | notice |
| micromark-util-decode-string | MIT | LOW | notice |
| micromark-util-encode | MIT | LOW | notice |
| micromark-util-html-tag-name | MIT | LOW | notice |
| micromark-util-normalize-identifier | MIT | LOW | notice |
| micromark-util-resolve-all | MIT | LOW | notice |
| micromark-util-sanitize-uri | MIT | LOW | notice |
| micromark-util-subtokenize | MIT | LOW | notice |
| micromark-util-symbol | MIT | LOW | notice |
| micromark-util-types | MIT | LOW | notice |
| micromatch | MIT | LOW | notice |
| mime-db | MIT | LOW | notice |
| mime-types | MIT | LOW | notice |
| ms | MIT | LOW | notice |
| negotiator | MIT | LOW | notice |
| object-inspect | MIT | LOW | notice |
| on-finished | MIT | LOW | notice |
| once | ISC | LOW | notice |
| parse-entities | MIT | LOW | notice |
| parse5 | MIT | LOW | notice |
| parseurl | MIT | LOW | notice |
| path-to-regexp | MIT | LOW | notice |
| picomatch | MIT | LOW | notice |
| prismjs | MIT | LOW | notice |
| property-information | MIT | LOW | notice |
| proxy-addr | MIT | LOW | notice |
| qs | BSD-3-Clause | LOW | notice |
| range-parser | MIT | LOW | notice |
| raw-body | MIT | LOW | notice |
| react | MIT | LOW | notice |
| react-dom | MIT | LOW | notice |
| react-markdown | MIT | LOW | notice |
| react-syntax-highlighter | MIT | LOW | notice |
| refractor | MIT | LOW | notice |
| rehype-raw | MIT | LOW | notice |
| rehype-sanitize | MIT | LOW | notice |
| remark-gfm | MIT | LOW | notice |
| remark-parse | MIT | LOW | notice |
| remark-rehype | MIT | LOW | notice |
| remark-stringify | MIT | LOW | notice |
| router | MIT | LOW | notice |
| safer-buffer | MIT | LOW | notice |
| scheduler | MIT | LOW | notice |
| send | MIT | LOW | notice |
| serve-static | MIT | LOW | notice |
| setprototypeof | ISC | LOW | notice |
| side-channel | MIT | LOW | notice |
| side-channel-list | MIT | LOW | notice |
| side-channel-map | MIT | LOW | notice |
| side-channel-weakmap | MIT | LOW | notice |
| space-separated-tokens | MIT | LOW | notice |
| statuses | MIT | LOW | notice |
| stringify-entities | MIT | LOW | notice |
| style-to-js | MIT | LOW | notice |
| style-to-object | MIT | LOW | notice |
| to-regex-range | MIT | LOW | notice |
| toidentifier | MIT | LOW | notice |
| trim-lines | MIT | LOW | notice |
| trough | MIT | LOW | notice |
| type-is | MIT | LOW | notice |
| unified | MIT | LOW | notice |
| unist-util-is | MIT | LOW | notice |
| unist-util-position | MIT | LOW | notice |
| unist-util-stringify-position | MIT | LOW | notice |
| unist-util-visit | MIT | LOW | notice |
| unist-util-visit-parents | MIT | LOW | notice |
| unpipe | MIT | LOW | notice |
| urql | MIT | LOW | notice |
| vary | MIT | LOW | notice |
| vfile | MIT | LOW | notice |
| vfile-location | MIT | LOW | notice |
| vfile-message | MIT | LOW | notice |
| web-namespaces | MIT | LOW | notice |
| wonka | MIT | LOW | notice |
| wrappy | ISC | LOW | notice |
| zustand | MIT | LOW | notice |
| zwitch | MIT | LOW | notice |

---

## Items Requiring Attention

### Reciprocal Licenses (MPL-2.0)

These packages are under **MPL-2.0** (Mozilla Public License 2.0), a file-level copyleft licence. Trivy rates them MEDIUM:

| Package | Target |
|---------|--------|
| github.com/hashicorp/golang-lru/v2 | services/mddbd/go.mod |
| github.com/go-sql-driver/mysql | test/go.mod |

**Impact:** MPL-2.0 requires that modifications to MPL-licensed *source files* are released under MPL-2.0. It does **not** extend to the rest of the project. `test/go.mod` is the integration-test module and is not part of any released binary.

**Action required:** none while the MPL-licensed files are used unmodified. A changed file must be made available under MPL-2.0.

### Licences the scanner does not classify

| Package | License | Target |
|---------|---------|--------|
| minimatch | BlueOak-1.0.0 | integrations/github-action/package-lock.json |

Trivy has no category for the Blue Oak Model License 1.0.0 and reports it as UNKNOWN. Its text grants use, modification and distribution and asks for a copy of the licence or a link to it; it has no copyleft clause.

---

## License Categories Explained

| Category | Description | Examples |
|----------|-------------|----------|
| **notice** (permissive) | Free to use, modify, and distribute. Requires attribution/copyright notice. | MIT, BSD, Apache-2.0, ISC |
| **unencumbered** | Public domain or equivalent. No restrictions. | CC0-1.0, Unlicense |
| **reciprocal** | Modifications to licensed files must be shared. Does not "infect" the rest of the codebase. | MPL-2.0 |
| **restricted** (copyleft) | Entire derivative work must use the same license. | GPL-2.0, GPL-3.0 |

---

## Recommendations

1. **No action required** for the packages covered: every licence allows commercial use.
2. **Audit the files listed under "What the scan does not cover"** with a tool that reads those ecosystems before relying on this report for the Rust and Python parts.
3. **Keep the third-party notices** that Apache-2.0 and BSD licences ask for with each distribution.
4. **Re-run the scan** when a dependency is added: the command at the top of this page reproduces it.
5. **Do not modify the MPL-2.0 files** without publishing the changed files under MPL-2.0.

---

*Report generated on 2026-11-23 with Trivy 0.76.2.*
