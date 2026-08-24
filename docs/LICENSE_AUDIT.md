---
title: "License Audit Report"
slug: "docs/license-audit"
description: "License audit of every MDDB dependency across Go modules and npm packages, with the distribution obligations each licence carries."
status: publish
---

# License Audit Report

**Project:** MDDB (Markdown Database)  
**Date:** 2026-03-10  
**Tool:** [Trivy](https://trivy.dev/) — filesystem license scanner  
**Scan command:** `trivy fs --scanners license .`  
**Amended:** 2026-08-24 — `github.com/google/renameio` removed from
`services/mddbd` (WIN-003); see the footnote on `github.com/coder/hnsw`. This is
a targeted correction, not a re-scan: every other row is still as of the scan date.

---

## Executive Summary

Scanned **4 targets** (Go modules + npm packages) with a total of **275 dependencies**.

| Metric | Value |
|--------|-------|
| Total dependencies scanned | 275 |
| CRITICAL issues | 0 |
| HIGH issues | 0 |
| MEDIUM issues | 2 |
| LOW issues | 274 |
| Permissive (notice) | 272 (98.6%) |
| Reciprocal | 2 (0.7%) |
| Public domain (unencumbered) | 2 (0.7%) |

**Overall risk: LOW** — No HIGH or CRITICAL license issues detected.

---

## License Distribution

| License | Count | Category | Commercial Use |
|---------|-------|----------|----------------|
| MIT | 225 | notice | Yes |
| BSD-3-Clause | 23 | notice | Yes |
| Apache-2.0 | 14 | notice | Yes |
| ISC | 6 | notice | Yes |
| BSD-2-Clause | 4 | notice | Yes |
| CC0-1.0 | 2 | unencumbered | Yes |
| MPL-2.0 | 2 | reciprocal | Yes (with conditions) |

---

## Scan Targets

### services/mddb-cli/go.mod

**3 dependencies**

| Package | License | Severity | Category |
|---------|---------|----------|----------|
| github.com/inconshreveable/mousetrap | Apache-2.0 | LOW | notice |
| github.com/spf13/cobra | Apache-2.0 | LOW | notice |
| github.com/spf13/pflag | BSD-3-Clause | LOW | notice |

### services/mddb-panel/package-lock.json

**216 dependencies**

| Package | License | Severity | Category |
|---------|---------|----------|----------|
| @0no-co/graphql.web | MIT | LOW | notice |
| @babel/runtime | MIT | LOW | notice |
| @types/debug | MIT | LOW | notice |
| @types/estree | MIT | LOW | notice |
| @types/estree-jsx | MIT | LOW | notice |
| @types/hast | MIT | LOW | notice |
| @types/http-proxy | MIT | LOW | notice |
| @types/mdast | MIT | LOW | notice |
| @types/ms | MIT | LOW | notice |
| @types/node | MIT | LOW | notice |
| @types/prismjs | MIT | LOW | notice |
| @types/react | MIT | LOW | notice |
| @types/unist | MIT | LOW | notice |
| @types/unist | MIT | LOW | notice |
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
| eventemitter3 | MIT | LOW | notice |
| express | MIT | LOW | notice |
| extend | MIT | LOW | notice |
| fault | MIT | LOW | notice |
| fill-range | MIT | LOW | notice |
| finalhandler | MIT | LOW | notice |
| follow-redirects | MIT | LOW | notice |
| forwarded | MIT | LOW | notice |
| fresh | MIT | LOW | notice |
| function-bind | MIT | LOW | notice |
| get-intrinsic | MIT | LOW | notice |
| get-proto | MIT | LOW | notice |
| gopd | MIT | LOW | notice |
| graphql | MIT | LOW | notice |
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
| http-proxy | MIT | LOW | notice |
| http-proxy-middleware | MIT | LOW | notice |
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
| is-plain-object | MIT | LOW | notice |
| is-promise | MIT | LOW | notice |
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
| requires-port | MIT | LOW | notice |
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
| undici-types | MIT | LOW | notice |
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

### services/mddbd/go.mod

**35 dependencies**

| Package | License | Severity | Category |
|---------|---------|----------|----------|
| github.com/99designs/gqlgen | MIT | LOW | notice |
| github.com/agnivade/levenshtein | MIT | LOW | notice |
| github.com/bits-and-blooms/bitset | BSD-3-Clause | LOW | notice |
| github.com/bits-and-blooms/bloom/v3 | BSD-2-Clause | LOW | notice |
| github.com/chewxy/math32 | BSD-2-Clause | LOW | notice |
| github.com/coder/hnsw [^hnsw-fork] | CC0-1.0 | LOW | unencumbered |
| github.com/go-viper/mapstructure/v2 | MIT | LOW | notice |
| github.com/goccy/go-json | MIT | LOW | notice |
| github.com/golang-jwt/jwt/v5 | MIT | LOW | notice |
| github.com/golang/snappy | BSD-3-Clause | LOW | notice |
| github.com/google/uuid | BSD-3-Clause | LOW | notice |
| github.com/gorilla/websocket | BSD-2-Clause | LOW | notice |
| github.com/hashicorp/golang-lru/v2 | MPL-2.0 | MEDIUM | reciprocal |
| github.com/klauspost/compress | Apache-2.0 | LOW | notice |
| github.com/klauspost/compress | BSD-3-Clause | LOW | notice |
| github.com/klauspost/compress | MIT | LOW | notice |
| github.com/quic-go/qpack | MIT | LOW | notice |
| github.com/quic-go/quic-go | MIT | LOW | notice |
| github.com/robfig/cron/v3 | MIT | LOW | notice |
| github.com/sosodev/duration | MIT | LOW | notice |
| github.com/vektah/gqlparser/v2 | MIT | LOW | notice |
| github.com/viterin/partial | MIT | LOW | notice |
| github.com/viterin/vek | MIT | LOW | notice |
| go.etcd.io/bbolt | MIT | LOW | notice |
| golang.org/x/crypto | BSD-3-Clause | LOW | notice |
| golang.org/x/exp | BSD-3-Clause | LOW | notice |
| golang.org/x/net | BSD-3-Clause | LOW | notice |
| golang.org/x/sync | BSD-3-Clause | LOW | notice |
| golang.org/x/sys | BSD-3-Clause | LOW | notice |
| golang.org/x/text | BSD-3-Clause | LOW | notice |
| google.golang.org/genproto/googleapis/rpc | Apache-2.0 | LOW | notice |
| google.golang.org/grpc | Apache-2.0 | LOW | notice |
| google.golang.org/protobuf | BSD-3-Clause | LOW | notice |
| gopkg.in/yaml.v3 | Apache-2.0 | LOW | notice |
| gopkg.in/yaml.v3 | MIT | LOW | notice |


[^hnsw-fork]: Resolved through a `replace` in `services/mddbd/go.mod` onto
`github.com/tradik/hnsw`, a fork holding one commit: the one sent upstream as
[coder/hnsw#24](https://github.com/coder/hnsw/pull/24). The licence is
unchanged — CC0-1.0 is a public-domain dedication, so the fork carries no
additional obligation. That commit is also what removed
`github.com/google/renameio` (Apache-2.0) from this module: it was reachable
only from the one hnsw function the patch rewrites, and it has no Windows
build. The `replace` is temporary and tracked for removal once upstream merges.

### test/go.mod

**21 dependencies**

| Package | License | Severity | Category |
|---------|---------|----------|----------|
| filippo.io/edwards25519 | BSD-3-Clause | LOW | notice |
| github.com/go-sql-driver/mysql | MPL-2.0 | MEDIUM | reciprocal |
| github.com/golang/snappy | BSD-3-Clause | LOW | notice |
| github.com/klauspost/compress | Apache-2.0 | LOW | notice |
| github.com/klauspost/compress | BSD-3-Clause | LOW | notice |
| github.com/klauspost/compress | MIT | LOW | notice |
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

---

## Items Requiring Attention

### Reciprocal Licenses (MPL-2.0)

The following packages use **MPL-2.0** (Mozilla Public License 2.0), which is a "file-level" copyleft license:

| Package | Target |
|---------|--------|
| github.com/hashicorp/golang-lru/v2 | services/mddbd/go.mod |
| github.com/go-sql-driver/mysql | test/go.mod |

**Impact:** MPL-2.0 requires that modifications to MPL-licensed *source files* must be released under MPL-2.0. However, it does **not** require the rest of the project to be open-sourced (unlike GPL). Using these libraries unmodified in a larger project is fully permitted for commercial use.

**Action required:** None, as long as the MPL-licensed files themselves are not modified. If modifications are needed, the changed files must be made available under MPL-2.0.

### Medium Severity

| Package | License | Target |
|---------|---------|--------|
| github.com/hashicorp/golang-lru/v2 | MPL-2.0 | services/mddbd/go.mod |
| github.com/go-sql-driver/mysql | MPL-2.0 | test/go.mod |

**Note:** MEDIUM severity in Trivy license scanning typically indicates licenses that may have additional requirements (e.g., attribution in binary distributions). Review these for compliance with your distribution method.

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

1. **No immediate action required** — all licenses are compatible with commercial use
2. **Maintain a LICENSE/NOTICE file** listing all third-party dependencies and their licenses (standard practice for Apache-2.0 and BSD-licensed dependencies)
3. **Re-run this audit** before each major release to catch any newly introduced dependencies with restrictive licenses
4. **Monitor MPL-2.0 dependencies** — if you fork or modify their source files, ensure compliance

---

*Report generated on 2026-03-10 using Trivy filesystem license scanner.*
