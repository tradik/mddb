# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **`search_advisor` — how should I search this collection? (SRCH-010)** — the
  server offers eight vector algorithms, four ranking algorithms, three fusion
  strategies and four retrieval modes, and an agent connecting over MCP sees a
  list of names and nothing else. It defaults or guesses, and every algorithm
  added since made the menu longer without making the choice easier.

  The advisor measures the collection — how many documents, how long, how much
  new vocabulary each contributes, whether they are embedded, whether they are
  code — and recommends the search type, ranking algorithm, vector index,
  fusion weights and result shape, **with a plain-language reason for every
  choice**. Three collections on one server get three different answers: a
  theme gets `bm25` + `retrievalMode: graph`, 900-word manuals get `pmisparse`
  + `chunk` + topK 5, short status notes get `bm25` + `parent` + topK 20.

  `GET /v1/search-advisor?collection=X`, the `search_advisor` MCP tool
  (annotated read-only), and a returned `retrievalProfile` ready to store on
  the collection so every client inherits it. `apply=true` stores it, merging
  rather than replacing — a collection's response prompt and encryption flag
  have nothing to do with retrieval and must survive.

  It measures the corpus, not your queries, which is why it explains itself:
  disagreeing with a reason is a legitimate outcome.


- **`retrievalMode: "graph"` (SRCH-006)** — `parent`, `chunk` and `window`
  change the *shape* of a result; this changes which documents are *reached*.
  A query about a checkout bug matches `checkout.js`; the stylesheet whose
  selector it manipulates and the template that loads it match nothing, and an
  agent has to notice the gap and ask again. Graph mode follows the connection
  graph out from the documents that did match, scoring each neighbour as a
  decaying fraction of the result that reached it, and returns the edge that
  justifies each one — `fromKey`, `symbol`, `kind`, `depth`. Without that a
  caller sees a document that matched nothing and cannot tell whether the
  search is working. Neighbours are appended after the direct matches rather
  than merged into the ranking, because they are a different kind of answer.


- **COMPARISON.md rewritten around measured numbers (DOC-013, DOC-015)** — the
  page was 286 lines of "✅ Advantages" checklists with one performance table
  whose numbers had no methodology, no date and no way to reproduce them. Every
  figure now names the command that produces it again (`make bench-comparison`),
  and the page states the host, the corpus, the transport and the measurement
  date. `test/mddb-profile` is the harness: ingest throughput, search latency
  percentiles, on-disk size and server RSS.

  The most useful thing it measures is what full-text indexing costs. The same
  5 000-document corpus ingests at 562 docs/s into 325 MiB with the index on,
  and at **13 883 docs/s into 16 MiB with `skipFts`** — 25× the write
  throughput and 20× the storage. That is the honest price of instant search
  over every document, and it is a per-ingest choice.

  A **"vs RAG wrappers"** section is new (DOC-015): where the trade actually
  falls between an engine and a layer over somebody else's engine, framed as a
  coupling decision rather than a feature list. Every comparison keeps its
  "choose the other thing when…" section. [PMISparse](docs/PMISPARSE.md) and
  the [FTS benchmark](docs/BENCHMARK.md) are linked from it, which they were
  not.


- **A guard for relative Markdown links (DOC-013)** — `check-docs-links.py`
  validates the built site, so nothing checked README.md, CONTRIBUTING.md or
  the CHANGELOG, which are read on GitHub where a dead link is a plain 404.
  `scripts/check-repo-links.sh` walks every tracked Markdown file and fails on
  a relative link that resolves to nothing. Schemes, anchors and site-absolute
  paths are out of scope, each for a stated reason. Seven-case test suite;
  runs in CI and `make ci`.


- **Two release write-ups on the blog** — *What Breaks If I Change This?* on
  the code connection graph, and *The Collection Knows How to Be Searched* on
  per-collection retrieval profiles. Every number, command and JSON body in
  both was produced against a running instance rather than written from
  memory, which is how the RAG-001 gap above was found. Both carry the
  frontmatter the current SSG understands (`type`, `tags`, `excerpt`, per-post
  mermaid), and `blog/README.md` now documents that shape instead of the older
  four-field one.


- **Static analysis in CI (OPS-008)** — CI scanned dependencies for known CVEs
  and never looked at the code. gosec was in the repository's conventions and
  in `.gitignore`: used locally, enforced nowhere. A new SAST workflow runs
  gosec across all four Go modules and CodeQL over Go and JavaScript/TypeScript
  — the panel, the chat widget and three integrations had no security analysis
  of any kind. Both write SARIF to the Security tab, so a finding has somewhere
  to live besides a red job. The gosec gate blocks at medium severity **from
  the first run**, because every module is already clean at that level;
  generated `proto/` is excluded, since its findings belong to the generator
  and a `#nosec` there would be erased by the next `buf generate`.
  golangci-lint now also covers `mddb-cli` and `tools/bench`, which nothing was
  linting.


- **Rust dependencies are scanned (OPS-008)** — `cargo audit` runs alongside
  govulncheck. mddb-chat is a shipped service and its dependency graph had
  never been looked at: the first run found **four vulnerabilities**, including
  an unbounded-empty-DATA-frame denial of service in `h2` and a reachable panic
  plus two certificate-validation flaws in `rustls-webpki`. All four are fixed
  by upgrades in this release.


- **Every GitHub Action is pinned to a commit SHA (OPS-018)** — all 129
  references across 12 workflows, with the version kept in a trailing comment
  so Dependabot can still propose upgrades. A tag is a mutable pointer:
  `tj-actions/changed-files` had every one of its tags repointed at a commit
  that dumped CI secrets into build logs, and thousands of repositories picked
  it up within hours without changing a line of their own.
  `scripts/check-action-pins.sh` fails on any reference that is not a
  40-character SHA, with its own seven-case test suite, and runs in CI and
  `make ci`.


- **VERSIONING.md** — which components move with a release and which keep their
  own version, why each is on its list, and what a patch, minor or major bump
  actually promises. Five components sit at `0.1.0` and have never been bumped;
  that is now recorded as a fact about the past rather than an implied claim
  that they are pre-release. RELEASING.md pointed at "any other version
  references"; it now points at the guard.


- **mddb-chat has tests beyond one file (TEST-001)** — 10 tests in 3 of 27
  source files became 63 in 8, covering session admission and queueing, history
  trimming, the rate limiter, error-to-status mapping and the webhook payload
  contract. The three bugs above were found by writing them. `make test-chat`
  runs the suite.


- **The Node.js and Python packages have a client (TEST-001)** — both shipped
  as example scripts. `@tradik/mddb-client` pointed `main` at `example.js`, so
  requiring the package executed a demo that connected to localhost and wrote
  documents; `mddb-client` for Python exported nothing at all, and its
  generated stub used an absolute `import mddb_pb2` that only resolves when the
  files sit on `sys.path` directly — **`import mddb_client` raised
  ModuleNotFoundError**, so the published package had never been importable.
  Both now export a client whose methods are taken from the service definition,
  so every unary RPC in `mddb.proto` is available under its own name and a new
  RPC works the moment the proto carries it. Each carries API-key and JWT
  metadata, a per-call deadline, an explicit TLS switch (INT-011) and a
  `waitForReady` so an unreachable server is reported as a refused connection
  rather than a timeout on the first call. `proto/generate.sh` rewrites the
  Python import, so the defect cannot come back on the next regeneration.


- **CI runs the new suites (TEST-001)** — the mddb-cli job built and tested
  without measuring; it now enforces an 88% coverage floor. `tools/bench` had
  no job at all and now builds, vets and tests behind a 68% floor. The Node
  client job installed and audited a package with no tests and now runs them; a
  new job installs the Python client, asserts that importing it works, and runs
  its tests. Both client suites start a gRPC server in-process, so neither
  needs a live MDDB.


- **`/v1/export` no longer calls itself over HTTP (GO-021)** — the handler
  answered by POSTing to its own `/v1/search` on
  `localhost:$MDDB_ADDR`, a full round-trip through the stack to reach a
  database it already had open. It broke whenever the server was not reachable
  at that guessed address — TLS, a unix socket, a different interface — and it
  did not check whether the request succeeded: `res, _ :=` followed by
  `res.Body.Close()` dereferenced a nil response and took the process down.
  Both formats now run the query in-process through the direct client, and
  NDJSON streams to the client instead of being built in memory first.


- **MCP progress notifications are delivered (GO-021)** — the sender, the token
  extractor and the transport's notification writer all existed and nothing
  connected them, so a `vector_reindex` over a large collection was
  indistinguishable from a hung call. The stdio and Streamable HTTP transports
  now supply a delivery function, the handler supplies the client's progress
  token, and the reindex reports as it embeds. A client that sent no token
  still receives nothing, which is what the spec asks for.


- **MCP log messages are delivered (GO-021)** — `logging/setLevel` was
  accepted, validated and stored, and then never consulted: a client that set
  the level to `debug` received exactly as much as one that set it to
  `emergency`, namely nothing. Failing tool calls now reach the client's log at
  error level, filtered by the level it asked for. Logging and progress are
  separate subscriptions, so raising the log level works without a progress
  token.


- **Open MCP sessions are visible in `/health` (GO-021)** — both transports
  counted their sessions and nothing ever read the count. A session a client
  abandoned without closing lives until it times out, so `mcpSessions` is how
  that leak becomes visible. The field is absent when MCP is disabled: an
  absent field says "not running" where a zero would say "idle".


- **Drift guards over the MCP tool table (TEST-002)** — tests now assert that
  every one of the 80 advertised tools is reachable through the dispatcher and
  that every dispatched name is advertised, in both directions. A tool present
  in one list and missing from the other exists for discovery and not for
  calling, or is dead code left by a rename. The read-only classification is
  checked the same way: every tool must be annotated, writers must be refused
  on a read-only server, readers must not be, and the MCP-only override must
  still refuse writes on a writable one — that override is what hands an agent
  a database it cannot damage.


- **HTTP routes extracted from `main()` and tested (TEST-002)** — 107 route
  registrations lived inside a 1089-line `main()`, where nothing could reach
  them: the file carried the entire public surface of the server at 0.7%
  coverage. `registerRoutes` is now a list in its own file, at 96%, and the
  tests assert what a deployment actually exposes: that every endpoint the
  server advertises is wired, that auth routes appear only when auth is on,
  that write endpoints refuse in read-only mode, that health answers on both
  its paths without auth, that pprof stays off unless asked for, and that
  registering twice does not panic the process on boot.


- **Per-area coverage floors (TEST-002)** — `go test ./...` reported one number
  for a package of 150 files, and that number looked healthy while the surfaces
  handling file uploads, 80 MCP tools and automation triggers sat at 22% between
  them. `scripts/check-coverage-areas.sh` now reports coverage per source file
  for the highest-risk areas and fails when one drops below its floor. The
  floors are ratchets: raise one when you improve an area, never lower one to
  make a build pass. It has its own 6-case guard suite, because a guard that
  only ever sees the passing case proves nothing.

  With it in place: **`upload_handler.go` went from 6.1% to 73.1%** and
  `automation_trigger.go` from 38.3% to 58.7%, through behavioural tests rather
  than exercise — the upload tests post real multipart bodies and assert what
  key a file lands under and whether its text survives conversion; the trigger
  tests run a real HTTP server and assert what actually goes out on the wire,
  including that a webhook nobody can reach terminates instead of holding the
  caller forever.


- **Fuzz testing (TEST-003)** — the repository had no fuzz target at all. There
  are now 12, over the parsers and decoders that read bytes MDDB did not write:
  the FTS query expression parser, both embedding-record encodings, the
  replication binlog entry and stream, the document compression codec, and
  `loadDoc`. Each asserts the same narrow property — an input either decodes or
  returns a typed error, never a panic and never a nil result with a nil error.
  Round-trip targets additionally pin that what one side writes, the other
  reads back unchanged.

  Saved crashers live in `testdata/fuzz` and are replayed by `go test` on every
  run, so a bug found once cannot come back quietly. CI spends 20 seconds per
  target on pull requests and a nightly job spends ten minutes on each of the
  twelve, uploading any new crasher as an artifact so a CI finding reaches the
  repository instead of being rediscovered from scratch.

  A thirteenth target fuzzes the store itself rather than a decoder: random
  programs of writes, overwrites, deletes and reopens, with two invariants
  checked after every step — every live document reads back with the content it
  was written with, and no metadata index entry points at a document that does
  not exist. That is the shape of GO-001 and GO-010, which never announce
  themselves as crashes; they surface as a search returning a document that was
  deleted, weeks later.

  Four bugs surfaced within the first hour; they are listed under Fixed.

- **Upgrade compatibility gate (TEST-003)** — nothing ships until the new build
  proves it can open a database an older release wrote. `test/upgrade-fixtures/`
  holds a real file produced by the v2.11.4 binary (four documents, metadata,
  revisions, an FTS index), and the release workflow will not start a single
  build job until the current code reads it back with its content and metadata
  intact. The same check runs on pull requests, so a break surfaces where it is
  introduced rather than weeks later when someone tries to tag.

  Every other test in this repository writes with the current code and reads
  with the current code. That proves the format is self-consistent, which is
  exactly the property that stays true while compatibility quietly breaks — and
  the failure it hides is the worst one a database can hand a user: a file the
  new version refuses to open, with the old binary already replaced.


- **Metadata lint for lost structure (DOC-012, issue #187)** — `ValidateDocument`
  now returns a `warnings` list alongside `errors` on all four surfaces (REST,
  gRPC, GraphQL, MCP) when a metadata value looks like Go's `%v` rendering of a
  map or a list of maps. MDDB's meta is flat by design, so structured
  frontmatter — an `faq:` list of objects, a `schema:` JSON-LD block — has
  nowhere to go, and an importer reaching for `%v` produces
  `map[answer:… question:…]`, which MDDB stores faithfully because it is a valid
  string. The damage surfaced much later, in a template that could not render
  it. A warning and never an error: rejecting the value would break callers
  legitimately storing text about `map[string]int`, and MDDB has no business
  deciding a string is not what its author meant. The lint runs even where no
  schema is configured, which is exactly the case an unstructured import lands
  in. `docs/API.md` gains "Structured frontmatter and flat meta" with the three
  ways to handle it — JSON in one value, flattened leaf keys, or leaving the
  block in the markdown body — and the SSG integration guide points at it.
  `ValidateDocumentResponse` gains field 3 (`warnings`), which is
  wire-compatible: older clients ignore it.

- **`oversample` as a request parameter (SRCH-005)** — every search that
  post-processes its results asks the index for more than it returns, and that
  multiplier was the literal `topK * 3` in five separate places, so the
  recall/latency trade-off every comparable engine exposes was fixed at whatever
  those constants said. It is now a parameter on vector, hybrid and
  cross-search across REST, gRPC, MCP and GraphQL, with a per-collection default
  in the retrieval profile and the usual precedence — request > profile >
  default. Range 1.0–10.0; out of range returns `422` (gRPC `InvalidArgument`)
  rather than clamping, because quietly halving a caller's recall setting is
  worse than telling them it was impossible. Unset reproduces earlier results
  exactly: the default is the constant the code already used.

  Measured on 2,500 five-chunk documents at `topK=10`: **at 1× a chunked
  collection returns 5 of the 10 documents you asked for**, because several
  chunks of one document fill the top of the ranking and deduplication collapses
  them with nothing left to fill the gap. At 3× it returns 9, at 10× all 10 —
  and on a flat index the latency difference across that whole range is about
  1%, since a flat search scans every vector regardless. The cost becomes real
  on approximate indexes, where the candidate count drives traversal.

  HNSW's `efSearch` is deliberately not exposed per query: it lives on the graph
  object shared by every concurrent search, so setting it per request would have
  one query silently change another's beam width — a data race producing wrong
  results rather than a crash.

- **Named `fast` ingest profile (RAG-004)** — MDDB could always ingest faster by
  skipping steps, but only as separate flags a caller had to discover one at a
  time, and the wiki importer made the same choice a third way with a `skipFts`
  comment reading "faster bulk import". `profile: "fast"` names the trade-off
  once, across `/v1/ingest`, `/v1/upload` and `/v1/import-wiki`, and the response
  records which profile applied so a corpus loaded months ago can be explained.
  It selects text-only parsing, no revisions, no webhooks and duplicate skipping
  — but deliberately **not** `skipEmbeddings` or `skipFts`: fast means cheaper
  parsing and less bookkeeping, not a collection nobody can search. Any flag set
  explicitly overrides the preset, and an unknown profile name is a 400 rather
  than a silent fall back, because a caller who asked for `fast` and got default
  behaviour would see a slow load and no reason why.

  Measured, not assumed: text-only HTML is **43× faster** (132 ms → 3.2 ms on a
  200-section document), while text-only DOCX is only 1.13× faster — its
  Markdown converter was already efficient, so the value there is tolerance of
  documents odd exporters produce, not speed. PDF gets no text-only variant at
  all, because its extractor already produces plain text and a second name for
  it would promise a speedup that does not exist.

- **Per-collection answer formatting (RAG-002)** — a `responsePrompt` on the
  collection says how answers drawn from it should be shaped: numbered steps for
  runbooks, code blocks for API docs. That instruction used to live in the
  client — a per-scenario system prompt in mddb-chat, and nothing at all for MCP
  agents — so every consumer had to know separately what every collection
  expected. mddb-chat now appends it to the scenario prompt, and MCP returns it
  on `search_documents`, `vector_search` and `hybrid_search` results and folds it
  into the `rag-pipeline` prompt, so an agent gets the instruction in the same
  call that fetched what to say. `{{collection}}` and `{{query}}` expand through
  the template mechanism the automation rules already use. Order in mddb-chat is
  deliberate: the operator's scenario prompt is policy and comes first, so a
  collection cannot talk its way past it by opening with an instruction of its
  own. Capped at 4 KiB — it is prepended automatically, and an unbounded value
  would quietly eat the context the answer needs. A collection without one
  behaves exactly as before.

- **Embedding cache (RAG-003)** — every vector, hybrid and memory-recall query
  called the embedding provider with no cache at all: a network round trip and a
  token charge even for a query asked seconds ago, and MCP agents repeat the same
  phrase in a loop. Embeddings are now memoised by `(model, text)` in an LRU with
  a TTL (`MDDB_EMBEDDING_CACHE_SIZE`, default 1024; `MDDB_EMBEDDING_CACHE_TTL`,
  default 1h). It is a decorator around the existing `Provider` interface, so no
  provider and no call site changed, and `MDDB_EMBEDDING_CACHE_SIZE=0` returns
  the bare provider — "disabled" is the old code path, not a cache that always
  misses. `EmbedBatch` asks the provider only for the texts it does not hold.
  Hit/miss/size counters appear in `/metrics` as
  `mddb_embedding_cache_{hits_total,misses_total,size}`.

  Reindexing also reuses vectors per chunk. The worker already skipped a
  document whose content was unchanged, but editing one paragraph of a
  fifty-chunk document changed the document hash and re-embedded all fifty at
  full provider cost. Each chunk's own hash is now stored with its vector, and
  reuse is keyed by that hash rather than by chunk index — so a chunk that keeps
  its text but shifts position, which is what any insertion above it causes, is
  still recognised. Quantized vectors are excluded from reuse: they are lossy on
  read, and reusing one would silently replace a full-precision vector with a
  degraded copy.

- **Per-collection retrieval profiles (RAG-001)** — retrieval settings now live
  next to the data instead of being repeated by every client. Search type, topK,
  granularity, hybrid strategy and a context token budget go in
  `CollectionConfig.retrieval`, over REST, gRPC, the panel and mddb-chat.
  Precedence is fixed everywhere — **explicit request parameter > collection
  profile > MDDB's default for that endpoint** — so a caller passing its own
  values notices nothing, and a collection without a profile behaves exactly as
  before, which a regression test pins. This replaces defaults that were
  scattered as constants across a dozen files with different numbers per path:
  FTS returned 50 results, vector 5, hybrid 10, memory recall 10, and a caller
  had to know MDDB's internals to get consistent behaviour. `contextTokenBudget`
  caps the total context a search returns so a RAG caller cannot be handed more
  than its model holds; it drops results from the tail rather than truncating
  documents, because half a document still costs tokens and no longer says
  anything reliable, and responses set `contextTruncated` when it applied.
  Cross-collection search is deliberately excluded: it has no single collection
  whose profile could own topK.

- **Code connection graph (CODE-005)** — answers the relational questions
  full-text search cannot: what breaks if `.hero-banner` changes, which pages
  load `checkout.js`, what does nothing reference any more. Available as
  `GET`/`POST /v1/code-graph`, the `code_graph` MCP tool (annotated read-only)
  and the `codeGraph` GraphQL query; a test pins that the three agree, so they
  cannot drift apart. No edges are stored — they are derived at query time from
  the `defines`/`uses`/`imports` meta through the metadata index that already
  backs `meta.*` filters. An edge is a statement about two documents, and
  storing it means one copy per side that drift apart when someone edits only
  one; deriving makes a reindex reproduce the graph exactly. Every edge carries
  the symbol that justifies it, because "these two files are related" is not
  actionable. Bounded by design: depth 1–3, at most 100 neighbours per node,
  and a `truncated` flag — "nothing depends on this" is only safe to act on when
  the walk was complete. Optional `lines=true` returns the first line each
  symbol appears on, reusing the line index from CODE-002.

- **Code symbols in meta (CODE-004)** — every code document now records what it
  `defines`, `uses` and `imports`, extracted from its own content on each save.
  Full-text search cannot tell a declaration from a mention, so searching a
  theme for `.hero-banner` ranked the stylesheet that defines the selector
  alongside every template that merely applies it — and the templates usually
  won, because they repeat the name more often. The three keys are ordinary
  values in the existing flat meta map, so `meta.defines=.hero-banner` answers
  the question through the metadata filter already there: no new query surface,
  no schema change. CSS contributes selectors, their component classes/ids and
  `--custom-properties`; JS/TS contributes function, class and arrow-const names
  plus import specifiers; HTML contributes `id`s as definitions, `class` and
  `on*` handler names as uses, and local `src`/`href` as imports. Output is
  deduplicated, sorted and capped at `MDDB_CODE_MAX_SYMBOLS` (default 512) —
  deterministic because these bytes travel through the replication binlog. Both
  write paths (single document and bulk) enrich through one call, so the
  behaviour cannot differ by transport. Only new writes are enriched; re-save an
  existing code collection to populate it.

### Changed

- **The mddb-cli command tree left `main()` (TEST-001)** — 31 commands were
  anonymous `RunE` closures inside a 1447-line `main()`, where no test could
  reach one: the module sat at 0.9% coverage, and the two bugs above had been
  there since the commands were written. `main()` is now three lines,
  `newRootCmd()` assembles the tree, and the commands live in nine files by
  area. The `key=val1|val2` flag parser had been written out five times and is
  now one function with its own tests. Coverage: **0.9% → 92.3%**, with the
  commands exercised against an httptest server the way a shell exercises them
  against a real one. `tools/bench` got the same treatment — the benchmark loop
  left `main()`, where it called `os.Exit` on the first failed write and could
  not report what it had measured: **3.4% → 71.9%**.


### Removed

- **Dead helpers and unfinished stubs (GO-020, GO-021)** — the dead-function
  count in `services/mddbd` went from 83 to 9, and the nine that remain are
  test-facing helpers, each now carrying a comment saying why it stays.
  Removed: `DirectIO`, a stub that returned nil without enabling anything, so a
  caller asking for unbuffered I/O silently got buffered I/O, plus
  `AlignedBuffer` which existed only to feed it; `graphql/scalars.go`, written
  for a gqlgen configuration this project did not adopt — `gqlgen.yml` maps
  `Time` to `graphql.Int64`, so the generated code never referenced these;
  `ComposeSystemPrompt`, a Go twin of `mddb-chat`'s Rust
  `compose_system_prompt`, which is where prompts are actually composed and
  where the tests for it already live; `rtfToText`, an alias for
  `rtfToMarkdown`; `NewMCPHandler`, superseded by `NewMCPHandlerWithConfig`;
  `GetCompressionStats`, which recompressed data to report a ratio nothing
  read; and `mustJSON`, whose only caller was the loopback POST that
  `/v1/export` no longer makes.


### Fixed

- **33 dead links across the repository (DOC-013)** — README.md alone linked
  six pages that have never existed: `docs/PERFORMANCE.md`, `docs/AUTH.md`,
  `docs/FTS.md`, `docs/CLIENTS.md`, `docs/WEBHOOKS.md` and
  `docs/API_QUICK_REFERENCE.md`. It also embedded `docs/panel.png` and linked
  `docs/swagger.html`, neither of which is at that path. The CHANGELOG carried
  24 links to source files that moved into `internal/` packages during earlier
  refactors — a historical entry naming a file should still reach it.
  `docs/AUTH_IMPLEMENTATION_SUMMARY.md` linked `docs/AUTHENTICATION.md` from
  inside `docs/`, which resolves to `docs/docs/`. All fixed, and the new guard
  keeps them fixed.


- **The published throughput figure could not be reproduced** — COMPARISON.md
  claimed 29 810 docs/s. Nothing in this repository produces that against an
  indexed corpus of real prose; it has the shape of a measurement taken with
  tiny documents and no meaningful index work. Removed rather than adjusted,
  and replaced with figures that name their corpus. The page's "~50MB memory"
  and "15MB Docker image" were stale in the same way.


- **The documentation site would not build** — `docs/MCP.md` linked
  `../integrations/agent-instructions/`, a repository directory the site does
  not publish, so the generator's link check failed and every docs deploy
  after that page landed was blocked. It points at GitHub now.


- **Three site figures were wrong** — the homepage advertised 79 MCP tools
  (there are 80, counted from `tools/list` against a running server) and a
  ~26MB binary (a release build with `-ldflags="-s -w"` is ~28MB; a plain
  `go build` is ~40MB, which is not what anyone downloads). `mddbReleaseDate`
  still reads 2.11.4's release day while `mddbVersion` says 2.12.0, so every
  page dates this release to the previous one's; it is now commented as a
  release-time step.


- **The context budget skipped the callers it exists for (RAG-001)** — a
  collection's `contextTokenBudget` was applied on the HTTP, hybrid and vector
  handlers and on none of the MCP paths. `full_text_search`, `semantic_search`
  and `hybrid_search` resolved `topK` from the profile and returned its
  `responsePrompt`, then handed back every document body the caller asked for,
  uncapped — and an agent assembling a prompt is exactly who the cap was
  written for. All three now apply it and report `contextTruncated`, so a
  caller can tell it is holding part of the answer. Results without content
  (the default: MCP drops bodies nobody asked for) are unaffected, because
  there is nothing to cap.


- **Five package manifests still declared 2.11.4 (DOC-011)** — the Node and
  Python clients, the panel and both language extensions were bumped by hand at
  2.11.4 and nothing bumped them since, because `check-version.sh` did not
  watch them. Publishing `@tradik/mddb-client@2.11.4` from a 2.12.0 tree is
  worse than a stale number: it names a server it was not built against, so
  anyone diagnosing a protocol mismatch starts from the wrong assumption. All
  five now move with the release and the guard covers thirteen sources instead
  of eight, with two new cases in its own test suite for the shape that got
  past it.


- **A queued chat visitor was double-booked, and at capacity 1 waited forever
  (TEST-001)** — `admit_from_queue` created the session, inserted it into the
  active map and then only *woke* the waiting task, which called `join()` again
  and created a second session for the same person. Every admission from the
  queue left an orphan holding a `max_concurrent` slot until its TTL expired.
  With `max_concurrent = 1` the orphan took the slot that had just been freed,
  so the visitor it was created for was re-queued behind a phantom of
  themselves, waiting on a fresh notifier nobody held. The session id now
  travels with the wake-up, so there is exactly one session per visitor, and a
  visitor who closed their browser while queued is skipped rather than being
  handed the slot.


- **`server.cors_origins` was configured and ignored (TEST-001)** — the field
  was parsed, defaulted and documented, and `main.rs` built its CORS layer with
  `allow_origin(Any)` regardless. An operator who listed their own origins
  still served `Access-Control-Allow-Origin: *` to every page on the internet.
  The listed origins are now applied; `["*"]` remains the default and now logs
  a warning, since it is only appropriate for a loopback deployment.


- **A scenario's `max_turns` capped nothing (TEST-001)** — parsed from the TOML
  and never read, so a public demo declaring a ten-turn limit served an
  unbounded conversation and an unbounded LLM bill. Enforced now, answering
  `403` rather than `429`: waiting does not lift the limit, so `Retry-After`
  would be a lie. Turns count user messages only — counting the assistant's
  replies would halve every configured limit.


- **`mddb-cli schema set` panicked on every invocation (TEST-001)** — it
  defined `-s` for `--schema` while the root command uses `-s` for `--server`,
  and pflag panics when a subcommand redefines an inherited shorthand. Every
  call crashed with a stack trace, `--help` included, so the command had never
  worked. The shorthand is gone; `--schema` is unchanged and `-s` keeps meaning
  `--server` as it does everywhere else. A test now walks the whole command
  tree and fails on any subcommand that redefines a global shorthand, and
  another renders every command's help.


- **`mddb-cli api-key list` crashed on a short hash (TEST-001)** — it sliced
  `keyHash[:16]` without checking the length, so an empty or truncated field
  panicked. This is the GO-005 class of crash the safe accessors were added to
  prevent, missed because it is a string slice rather than a type assertion.


- **The benchmark reported non-numbers (TEST-001)** — GO-013 guarded
  `perSecond` and the SVG coordinates against a zero interval and left the
  headline average as a bare division, so a run with no batches reported `NaN`
  docs/sec and the slowest batch as `1.7976931348623157e+308`, the sentinel the
  scan starts from. Chart coordinates are also clamped to the plot area: a bar
  computed outside the viewBox renders as nothing at all, which reads as a
  missing measurement rather than an off-scale one.


- **Benchmark documents carried duplicate tags (TEST-001)** — tags were drawn
  with replacement, so a document could be written with the same tag twice.
  The benchmark exists to measure metadata indexing, and a repeated value
  indexes once; the run reported a tag count it had not written.


- **Pooled buffers were never actually pooled (GO-020)** — `NewZeroCopyReader`
  and `NewZeroCopyWriter` each built a private `BufferPool`, so every buffer was
  returned to a pool that became garbage with the reader or writer that owned
  it. Every call allocated a fresh buffer and the pooling was decorative. Pools
  are now shared per buffer size. `BufferPool` itself stored `*[]byte` in `Put`
  and asserted `[]byte` in `Get`, so the first reuse would have panicked —
  nothing had ever reused a buffer, which is the only reason that was not in
  production.


- **`ZeroCopyReader` dropped data that arrived with an error (GO-020)** — a
  reader is allowed to return bytes together with `io.EOF`, and this one
  returned `(0, err)` in that case, discarding the last bytes of the stream,
  where a truncation is hardest to notice. It now hands the buffered data over
  before reporting the error. Backup and restore read through it, so the defect
  would have silently truncated copies.


- **GraphQL dated undated documents to the year 1 (GO-021)** — the adapter
  called `.Unix()` on the timestamp directly, and `time.Time{}.Unix()` is
  -62135596800. A legacy document written before timestamps existed sorted
  ahead of every real one. `gql.TimeToInt64`, written to guard exactly this and
  never called, is now used.


- **RTF ignored the `textOnly` ingest option in its documentation (RAG-004)** —
  the flag's comment listed rtf among the formats it changes, while the handler
  had no text-only branch for it. RTF control words carry no structure to
  rebuild, so the conversion already returns plain text; the documentation now
  says which formats the flag actually changes.


- **`storageBackend` was accepted, validated, documented — and ignored
  (GO-021).** A collection configured for `memory` or `s3` had its documents
  written to the local database like every other, while the API returned 200 and
  `docs/API.md` and `openapi.yaml` both described it as working. An operator who
  pointed a collection at S3, supplied credentials, saw success and then treated
  the node's disk as disposable would have lost their data.

  The cause was structural: the registry that routes collections to backends
  takes a fallback backend, and no BoltDB implementation of the interface
  existed — so the registry could never be constructed and `CreateBackend` had
  no callers at all. The memory and S3 backends themselves were real and
  complete.

  Document bodies now go where the configuration says. Indexes, revisions and
  the binlog stay local in every configuration, because they are written in one
  transaction a remote store cannot join — that split is documented rather than
  papered over. Bodies are written to the backend **before** the transaction
  indexing them commits, so a crash between the two leaves an unreferenced
  object rather than an index entry pointing at nothing. A backend that cannot
  be reached is refused when configured, and one that fails later causes writes
  to error instead of quietly landing on local disk.

  Documents written under an earlier version are in the local database and will
  be read from there until rewritten.


- **Consistent hashing sent four times more keys to one shard than another.**
  Virtual nodes were hashed as `"<shard>-<index>"`, so every replica of one
  shard shared a prefix and FNV-1a placed them in long contiguous arcs — at the
  production settings of 4 shards and 150 replicas, one shard owned a run of
  **50 consecutive ring positions**, and key distribution ran from 39.6% to
  160.8% of an even share. Swapping the two fields interleaves them: the longest
  run drops to 5 and the spread to 91–111%. Found by giving an assertion to a
  test that had computed the answer and thrown it away.

- **GraphQL `vectorStats` returned its collections in a different order every
  time**, because they came from map iteration with no sort. A UI list jumped on
  refresh and any diff of two responses was meaningless.


- **MCP boolean arguments sent as strings were silently ignored.** `mcpGetBool`
  accepted only a real JSON bool, so an agent sending `"lines": "true"` — which
  LLM clients do emit — got `false` and no indication why. The repository
  already contained `mcpCoerceBool`, written to tolerate exactly those
  spellings, with a comment calling the strict behaviour "a footgun"; the two
  decisions sat in the same file contradicting each other, and one of them was
  also pinned by a test. `mcpGetBool` delegates to it now, fixing
  `saveRevision`, `highlight` and `lines` together.

- **A non-string inside an MCP metadata array became an empty value.** It was
  kept as `""` rather than dropped, and an empty metadata value is indexed and
  searchable — so `["a", 1, "c"]` invented a phantom nobody wrote. Non-strings
  are skipped now.

- **`mcpGetInt` silently returned 0 for a Go `int`.** Harmless from JSON, where
  every number is a float64, but an internal caller or a YAML default lost its
  value without a word.


- **Batch update erased the field you did not send.** `UpdateBatch` assigned
  content and metadata unconditionally, so empty meant "clear it": an agent
  updating tags across a hundred documents wiped the content of all of them,
  and one updating content wiped their metadata. The single-document path
  already distinguishes absent from empty by taking pointers; the batch types
  cannot, so an empty value is now read as absent. A batch can no longer blank
  a field deliberately — that is rare, and available on the single-document
  endpoint. Silently destroying content nobody asked to change is not a trade
  worth keeping. Found by the first test written against the batch path
  (TEST-002).

- **The custom-tool guard restated all 80 built-in names by hand.** A second
  source of truth that happened to be in step, and would have drifted the first
  time someone added a tool and forgot that file — letting a custom tool shadow
  a built-in, which is a silent capability swap for any agent calling it. The
  list is derived from the tool table now.


- **`/v1/endpoints` advertised endpoints the server does not serve.** The
  automation routes are registered only when automations are enabled
  (`MDDB_AUTOMATIONS`), but the catalogue listed them unconditionally — a client
  reading `/v1/endpoints` to discover capabilities was sent to a 404 with no
  explanation. The catalogue now reflects the running server, and the temporal
  and spell-correction endpoints, which it had simply omitted, are listed when
  they are served. Found by the first test ever written against the route table
  (TEST-002).


- **`SchemaManager.Validate` and `Metrics.IncOp` panicked on a nil receiver.**
  Half the call sites guarded the nil and half did not — five schema call sites
  unguarded against four guarded, and one metrics call site out of thirty-eight.
  Both now treat a nil receiver as the answer it already means: no schema
  manager validates nothing, no metrics collector counts nothing. A counter is
  never worth a crash, and handling it once makes the whole class impossible
  rather than relying on every future caller remembering (TEST-002).

- **`RunTrigger` returned `(nil, nil)` for an unknown search type** — no
  matches and no error, indistinguishable from a trigger that matched nothing.
  An operator who mistyped `hybrid` saw a rule that never fired and no reason
  why. It returns `ErrUnknownSearchType` naming the offending value now. The
  API refuses to create such a rule, but one stored by an older version or
  edited outside the API arrives this way.

- **A filename of `..` produced a document key of `..`.** Key derivation
  rejected `.` but not `..`. MDDB itself stores keys in BoltDB, where this is
  harmless — but consumers that write one file per document key (ssg,
  wpexporter) resolve it against their output directory. A filename yielding a
  path operator has no usable key, and inventing one puts the document
  somewhere the caller cannot find it.


- **Decompressing a document had no size limit.** A snappy payload states its
  own decompressed length, and nothing checked it: **five bytes can claim two
  gigabytes**, which the runtime then tries to allocate. zstd was unbounded the
  same way. These bytes are not always ours — a follower decodes what a leader
  replicated, and `loadDoc` reads whatever is in the database file, including
  one restored from someone else's backup. Both codecs now refuse anything over
  `MaxDecompressedSize` (256 MB, against a 100 MB upload cap), snappy by
  checking the claim before allocating and zstd through `WithDecoderMaxMemory`.
  Found by the new fuzz targets — the run took the developer's machine down
  with it.

- **`loadDoc` panicked on malformed stored documents.** `github.com/goccy/go-json`
  v0.10.6 raises an index-out-of-range inside its struct decoder rather than
  returning an error, and the fault depends on the state a previous decode left
  behind — 1040 panics in 20000 decodes of a mixed sequence, and none when any
  single input was replayed alone, which is why no offending document could be
  isolated. The legacy JSON branch of `loadDoc` now uses `encoding/json`, which
  rejects the same bytes cleanly. That branch reads pre-protobuf documents and
  is not the throughput path, so the speed goccy was chosen for is not what was
  at stake. The remaining 72 files using goccy are triaged in GO-037.

- **Truncated embedding records panicked both decoders.** Each length prefix was
  read before checking that four bytes remained, so a record cut short — by a
  crash mid-write, a partial network read, a corrupt file — took the process
  down instead of being rejected. Both formats now decode through a
  bounds-checked reader, and a claimed dimension count is capped before it
  reaches `make()`, closing an integer overflow in the old byte-count check.

- **`ParseQueryExpression("")` returned `(nil, nil)`** — neither an expression
  nor an error, so the obvious caller dereferences nil. The one existing caller
  guarded it; the next would not. It returns `ErrEmptyQueryExpression` now, and
  a whitespace-only `mode=expression` query gets a `400` instead of silently
  returning an empty result set as though it had been asked a real question.


- **gRPC `SetCollectionConfig` erased every field it could not express.**
  `CollectionConfigProto` carries 7 of `CollectionConfig`'s ~15 fields, and the
  handler built a fresh struct from them, so a gRPC client updating a
  description silently cleared `storageBackend`, `storageConfig`,
  `quantization`, `diskOnlyVectors`, `trackAccess`, `trackHot`, `spellCorrect`,
  `spellLang` and — worst — `encrypted`, whose `false` value is pushed straight
  into the encryptor, so the next document written to an encrypted collection
  was plaintext. The handler now merges into the stored config: a field a client
  omits means "leave it alone", not "clear it".

- **MCP `set_collection_config` had the same defect**, found while wiring
  RAG-002 through it: an agent updating a collection's description cleared its
  storage backend, quantization, spell settings and encryption flag. Also merges
  now.

- **The panel's tracking and spell-correction toggles did nothing, and every
  config save cleared them.** `trackAccess`, `trackHot`, `spellCorrect` and
  `spellLang` exist in `CollectionConfig` and are read on every request, but were
  missing from `SetCollectionConfigRequest` — so the panel sent values the REST
  handler ignored, and because `PUT` replaces the stored config, each save also
  wiped whatever had been set by other means. All four are now part of the
  request.

- **The protobuf plugin pin had drifted from its runtime.** `buf.gen.yaml`
  generated code with `protocolbuffers/go` v1.36.11 while `go.mod` linked
  against `google.golang.org/protobuf` v1.36.12 — the comment beside the pin
  claimed they matched, and a dependency bump that touched only `go.mod` made it
  untrue. Generated code disagreeing with its runtime surfaces as an
  unmarshalling bug rather than a build error, so the comment is now a CI gate:
  `scripts/check-proto-plugins.sh` (7/7 guard tests, `make check-proto-plugins`).
  Both are on v1.36.12.

- **`buf` CLI bumped 1.50.0 → 1.72.0** in CI and in the generation script's
  requirements. Verified byte-identical output before and after: generated code
  comes from the plugins pinned in `buf.gen.yaml`, not from the CLI.

- **`proto/generate.sh` wrote generated Go code one directory too high** when
  `buf` is not installed. The legacy protoc fallback still used the
  pre-buf-migration layout (`services/mddbd`, not `services/mddbd/proto`), so a
  developer without buf produced 700 KB of files next to the server sources,
  with whatever `protoc-gen-go` happened to be on their machine instead of the
  version pinned in `buf.gen.yaml`. They compile as the wrong package and are
  invisible to the CI drift check. Path corrected, and the fallback now says
  loudly that its plugin versions are unpinned.

### Upgrading to 2.12.0

This release makes seven changes that need action or attention. They are why
this is 2.12.0 and not a patch.

**1. The production compose refuses to start without secrets.** `docker
compose up` now stops with an error naming `MDDB_AUTH_JWT_SECRET` or
`MDDB_AUTH_ADMIN_PASSWORD` if either is unset. Copy `.env.example` to `.env`
and set them. This is deliberate: the previous behaviour was to start an open,
writable database with an unauthenticated MCP endpoint.

**2. Published ports moved to loopback.** Every service now publishes on
`${MDDB_BIND_ADDR:-127.0.0.1}`. A reverse proxy or cloudflared reaches the
containers over `mddb-network` and needs no published port; if you were
reaching MDDB from another host, set `MDDB_BIND_ADDR=0.0.0.0` — deliberately,
and with authentication on.

**3. The log format changed completely.** The operational log is now
structured (`log/slog`): severity is a `level` field rather than a prefix in
the message, timestamps are RFC 3339, and message texts were rewritten. Any
alert, grep or Loki query matching the old text will stop matching.
`MDDB_LOG_FORMAT=json` is the default in the Docker image.

**4. `keyPrefix` in `/v1/mcp/keys` no longer carries key material.** It used to
return `mcp_` plus the first eight characters of the key — 32 bits of the
secret, in an API response and in the log. It now returns the scheme marker
alone, and a new `fingerprint` field (four bytes of SHA-256) identifies a key.
Anything matching on `keyPrefix` should move to `fingerprint`.

**5. The Grafana datasource now requires Grafana 13.** `peerDependencies` and
`grafanaDependency` said 11 while the plugin was compiled and type-checked
against 13, so 13-only APIs would have failed at runtime on the version it
claimed to support. Both now say 13.

**7. `storageBackend` now does what it said.** If any collection is configured
with `memory` or `s3`, its documents were being written to the local database
anyway — the setting was ignored before this release. From 2.12.0 the setting is
honoured, so those collections start writing to the configured backend while
their existing documents remain in the local file. Either rewrite them, or set
the collection back to `boltdb` if the earlier behaviour was what you actually
wanted. A collection whose backend cannot be reached now refuses writes rather
than falling back to local disk.

**6. Re-embed collections holding source, and re-save them for the graph.**
Code documents are now chunked on bracket depth rather than on paragraphs, and
chunks are re-derived rather than stored — so a document embedded before this
release and read after it returns the wrong passage. Run `vector_reindex` on any
collection holding source. Prose collections are untouched: their segmentation is
byte-for-byte unchanged, which a test pins.

Separately, symbols (`defines`/`uses`/`imports`) are extracted on write, so
documents stored before this release have none and the connection graph will
report them as orphans. Re-save or bulk re-ingest a code collection to populate
it — reading is unaffected either way.

The stored vector format gained a trailing per-chunk hash field. No migration
is needed and none is offered: a 2.12.0 server reads pre-2.12.0 records (they
simply have no per-chunk reuse), and an older server or replica reading a
2.12.0 record ignores the trailing field, which the previous format already
did. Both directions are pinned by tests, because getting this wrong would
corrupt a database in place.

Also worth knowing: heavy queries can return `503` with `Retry-After` under
load, where they previously all ran and risked exhausting memory
(`MDDB_SEARCH_MAX_CONCURRENT`); the Go client module now requires Go 1.27; and
graceful shutdown drains its queues, so both compose files allow a 20s stop
grace period — set the same if you run the image directly.

### Security
- **The server says when its storage cannot keep what it accepts** — bbolt fsyncs every commit, so an acknowledged write survives a crash; verified rather than assumed, by writing over REST, sending `kill -9` with no shutdown hooks, and finding the document and its full-text index intact after restart. But that promise is only worth what the storage under it is worth, and a container started without a volume writes to its own overlay layer: accepted, fsynced, gone when the container is removed. The data directory is now checked before the first write is accepted — for ephemeral filesystems (tmpfs, ramfs, overlay), for real writability (by creating a file, since a read-only mount or a user mismatch looks writable in the mode bits) and for free space against `MDDB_DISK_MIN_FREE` — and each finding is logged as a WARN carrying a code, so a collector can alert on `ephemeral_storage` rather than on prose. `GET /health` reports the same as `persistence` plus a computed `durable`, and `docs/DEPLOYMENT.md` now states per surface what is guaranteed and what is not — async bulk jobs and embeddings are queued, not written, and say so.
- **The extreme-mode log stopped promising a durability mode that does not exist** — it announced "WAL initialized (SyncPeriodic)", but nothing in the tree writes to that WAL: it opens a file, starts a flusher goroutine and receives nothing, while durability actually comes from bbolt's per-commit fsync. The line now says what is true. Whether the subsystem should be wired up or removed is a separate decision — a write-ahead log in front of a store that already fsyncs adds cost rather than a guarantee.

- **Vector search stopped returning deleted documents, and stopped crashing on collections with churn** — two defects in `coder/hnsw` v0.6.1, both reproducible in isolation. `Graph.Delete` reports success and `Len()` drops, yet `Search` keeps handing the deleted node back — so any HNSW collection was quietly answering with documents the caller had removed. And past roughly half a 1000-vector collection deleted, `Search` dereferences a nil node and panics; `HNSWIndex.Search` had no recover, so a gRPC or MCP query would have taken the server down (only HTTP was shielded, by the panic middleware). That is precisely the shape agent memory and TTL-expiring collections reach on their own. Results are now checked against the live-vector map the index already kept, the graph is rebuilt from live vectors once a fifth of a collection has been deleted (`Compact` and `DeletedSince` expose it), and a recover in the search path falls back to brute force rather than losing the process. Compaction also recovers recall: before it, deleted nodes occupy slots in the graph's top-K and vanish when the results are filtered, so a search for five could return three.

- **The production compose refuses to start without credentials, and binds to loopback** — `docker-compose.yml` ran the server read-write with HTTP, gRPC, MCP and HTTP/3 published on every interface while setting no `MDDB_AUTH_*` variable at all; since the image defaults to `MDDB_AUTH_ENABLED="false"`, `docker compose up -d` from a fresh clone started an open, writable database with an unauthenticated MCP endpoint. Authentication is now on in the file itself, with the JWT secret and admin password read as `${VAR:?...}` so compose stops naming the missing variable instead of quietly starting open, and mddb-chat carries the same credentials so it can still reach a server that now demands them. Every published port goes through `${MDDB_BIND_ADDR:-127.0.0.1}`: a reverse proxy or cloudflared reaches the containers over `mddb-network` and needs no published port at all, so exposing the stack on the host's interfaces is a deliberate act rather than the default.
- **MCP API keys are identified by fingerprint, never by a slice of the key** — creating or deleting a key logged `key[:12]`, which is 32 bits of the 128-bit secret written to whatever collects stdout, and the same slice left the process again through `List()`'s `keyPrefix` field. Both now carry four bytes of SHA-256 instead; `keyPrefix` keeps its place in the response shape but holds only the `mcp_` scheme marker, and a new `fingerprint` field is the identifier to display. A regression test captures the log around a create/delete cycle and fails on any run of the key's random half reaching it.
- **The extension gives back host permissions it no longer uses** — saving settings requested access to the new server origin and never revoked the old one, so with `optional_host_permissions` covering all of http and https, every address a user had ever typed stayed granted. The revocation compares against the origin currently held rather than the one read at startup, so a second save in the same session cannot leave the first save's grant behind.

### Changed
- **Code is chunked where code ends, not where a sentence does** — embedding chunks are what a vector search returns, and the prose chunker falls back to sentence boundaries, where a period inside `url(a.png)` is not one. A split there leaves half a declaration in each chunk: a passage that reads as nothing and embeds as noise. Source now splits only where `{}`, `()` and `[]` are balanced, with braces inside strings and comments ignored, so one chunk is roughly one rule or one function. Where a construct is larger than the chunk budget, the budget yields — an oversized chunk costs tokens, a meaningless one costs the answer — and only a minified single line is cut by size, and even then after the last balanced `}` within budget. The mode follows the document's kind, with `MDDB_EMBEDDING_CHUNK_MODE` to force it for collections ingested without the convention. Prose segmentation is unchanged byte for byte, which a test pins; collections holding source need re-embedding, since chunks are re-derived rather than stored.

- **Search results say where, not just which file** (issue #192) — a hit named the document and left an agent to read it to find the place, which is how a one-line CSS change came to cost thousands of tokens. Every highlight now carries the 1-based line range it occupies, and so does every chunk in `chunk` and `window` retrieval modes, widened with the window. The answer becomes `css/style.css:158-163` plus the fragment: under 800 bytes for a 164-line stylesheet, against roughly 20 000 tokens for the same search returning whole documents. Nothing is stored for this — the ranges are derived from content on the way out, so existing indexes need no migration. Two gaps only showed up against a running server: the MCP `full_text_search` tool did not accept `highlight` at all, so the lines reached REST but not the surface the issue is about; and a `fields` projection discarded the highlights, which is precisely the combination that makes the pattern work — do not send the body, say where to look. Both are fixed, and fragments are now extracted before the body is dropped rather than after.

- **Source code is searchable as source, not as prose** — storing a stylesheet or a script needed no new document type, table or endpoint, only a convention on the flat meta map every transport already carries: `kind: ["code"]`, with `language` and `path` alongside it. What changes is tokenisation. The prose tokeniser stems (`classes` becomes `class`), drops stop words — which in a language are its keywords — and splits on every punctuation mark, leaving `.hero-banner` findable only as two unrelated words. The code tokeniser keeps the whole identifier and emits its parts across camelCase, snake_case and kebab-case, holding acronyms together (`XMLHttpRequest` gives `xml`, `http`, `request`, not letters), and keeps single characters and digits because `a` is a selector and `h2` is a name. Because both the whole name and its parts are indexed, a code collection answers ordinary queries: nothing else in the search path had to change and no prose collection needs reindexing. The convention is optional — a document whose key or path ends in a known source extension is treated as code with no meta at all, so a theme ingested by an existing tool works as it stands, while an explicit `kind` always wins in both directions.

- **Release notes come from the changelog** — every tag published the same hand-written text, with benchmark figures from one historical measurement ("29,810 docs/sec", "5.75x faster than MongoDB") that no longer described anything in particular. `scripts/release-notes.py` extracts the section for the version being tagged and adds the installation block, which is the one part that genuinely repeats. The same stale figures are gone from the deb and rpm package descriptions. The generator refuses a version the tree does not declare: falling back to `[Unreleased]` is only safe for the version `const VERSION` reports, or a typo in a tag would publish this release's notes under a version nobody built.
- **The release version cannot drift again** — it is written in eight places by hand, and one of them (`.ssg.yaml`, which the documentation site reads) was not even on the list this was thought to cover. `scripts/check-version.sh` collects all eight and fails if they disagree, with a test suite covering the exact shape this went wrong in before: one source left behind during a bump. The git tag is deliberately not checked, since it is created after the guard passes; instead the guard reports whether the CHANGELOG section for this version is still under `[Unreleased]` or dated and ready to tag.

- **Agent instructions ship with the tools** — an agent connecting over MCP gets 79 tool schemas and no idea which one fits a question, or that asking for a projection instead of whole documents changes the cost by more than an order of magnitude. Measured through MCP: the same `full_text_search` over five 12 KB documents returns ~19 700 tokens by default and ~327 with `fields: ["key"]`. `integrations/agent-instructions/` now carries that guidance — a decision tree for choosing between full-text, semantic and hybrid search, the parameters that cut tokens, the anti-patterns (starting with fetching a whole document to change one line of it, the issue #192 case) and the `memory_*` working loop. One source, `AGENTS.md`, generates the Claude Code skill, the Cursor rule and the Windsurf rule, because four hand-maintained copies drift on the first edit. Two guards keep it honest: CI fails if a variant was edited instead of the source, and a test fails if the instructions name a tool the code does not define — which it did immediately, on a tool name that never existed.

- **Shutdown drains what it accepted** — seven subsystems shipped a `Stop`, `Close` or `Shutdown` that nothing ever called: the WAL, the bulk-ingest worker, the cron scheduler, the index queue, the temporal manager, MVCC and the HTTP/3 listener, whose handle only ever existed as a local inside a goroutine and so could not be closed at all. At process exit the kernel reclaims goroutines and descriptors either way — the cost was what those subsystems were still holding: queued index jobs, an in-flight bulk job, a buffered WAL write, all abandoned rather than finished. One ordered sequence replaces the scattered closes, in the reverse of the dependency order: stop accepting work, drain the queues, stop the periodic workers, close what the queues were writing to, caches last. It runs under `MDDB_SHUTDOWN_TIMEOUT_SEC`, and a step that wedges is named in the log rather than holding the process open until the operator's SIGKILL. A test starts, exercises and stops the subsystems three times and fails if goroutines are left behind — verified by removing one step and watching it catch the leak.
- **Resource-lifecycle linters, and a fetch that can be cancelled** — `bodyclose` and `sqlclosecheck` are enabled; their three hits were all the same false positive, since neither can see through `httpclient.DrainAndClose`, so those are excluded with the reason written down rather than blanket-suppressed. `noctx` is deliberately not enabled: it flags 19 places, and turning on a gate that has to be silenced wholesale is worse than leaving it off. The one finding that could genuinely hang was fixed instead — `fetchURL` now takes a context from the REST, gRPC and MCP callers, so importing from a slow or hostile host is abandoned when the caller gives up rather than holding a connection for the full client timeout.

- **Range filtering costs what you asked for, not what the collection holds** — `SearchRange` collected every matching document into a map, materialised the lot into a slice, sorted it, and only then applied the limit, so returning 50 results from a 10 000-document collection allocated the same 12.5 MB as returning 500. The cursor already walks documents in the order the results are returned — `doc|<collection>|<docID>` in byte order is docID ascending — so matches are now appended as they are found and the scan stops at the caller's limit. The same query takes 63 µs and 57.7 KB instead of 15.4 ms and 12.5 MB, and the cost finally scales with the page rather than the collection. A test proves the answer is unchanged by comparing it against the behaviour it replaced across seven query shapes.
- **Heavy queries queue instead of piling up** — a single-binary server has nowhere to shed load, and past some width parallel searches stop slowing down and start exhausting memory. FTS, vector, hybrid and aggregate now pass through a semaphore sized to the CPU count by default (`MDDB_SEARCH_MAX_CONCURRENT`); a query beyond it waits up to `MDDB_SEARCH_QUEUE_TIMEOUT_MS` and then receives `503` with `Retry-After` — something a client can act on, unlike an OOM that takes every other request with it. A client that cancels its own request gets `408` and is not counted as a rejection, because it was not one.

- **Opt-in search-result caching** — agents repeat identical queries in loops, and each repeat redid the full scoring pass over a result set that had not changed. A full-text search may now set `cacheTtl` (seconds) to reuse a recent answer, with `X-MDDB-Cache: hit|miss` on the response. It is opt-in per request on purpose: a search without `cacheTtl` is neither served from the cache nor stored in it, so the default behaviour and the default freshness are untouched and there is no silent staleness to reason about — the caller decides, per query, how stale an answer may be. Writes invalidate a collection's cached results immediately, through a per-collection generation counter mixed into the key, which costs nothing on the write path and cannot leave a result set from before your write reachable. The key covers the whole request except `cacheTtl` itself, so two callers asking the same question share an answer even when one is willing to hold it far longer than the other. `MDDB_SEARCH_CACHE_SIZE=0` turns it off server-wide.

- **Async jobs report themselves over SSE** — a client waiting for a bulk import had to poll its status endpoint. The hub that already carries document events now carries `job.started`, `job.progress`, `job.completed`, `job.failed` and `job.cancelled` too, on the same connection with the same authentication and per-IP limits, and `?job=<id>` narrows a stream to one job the way `?collection=` narrows documents. The payload is the record `GET /v1/bulk/jobs/{id}` returns, so counters have one shape either way. Events are emitted from the single place a job changes state, which means a new transition cannot forget to announce itself, and `job.progress` is capped at one per second per job — a chunked import would otherwise put hundreds of events on every listening connection to say what the next one says anyway. A connection filtered to a job receives that job's events only, not the document traffic the import itself generates.

- **Bulk ingest is 29x faster, because the indexes stopped committing per document** — indexing a document touches three full-text indexes, and each entry point opened its own BoltDB write transaction, so a batch paid three commits per document. Measured on this tree, 1000 documents took 4.20s with indexing and 21ms without: the commits, not the work, were 99.5% of the cost. The three transaction bodies are now shared helpers, `fts.IndexDocs` runs a whole batch inside one transaction, and tokenisation happens before it opens so the write lock is not held through pure CPU work. The same 1000 documents now take 0.14s — 238 docs/s to 6 996 — and allocate 68MB instead of 452MB. The batched index is byte-for-byte identical to the per-document one, which a test checks by comparing every entry in every FTS bucket rather than asking whether search still returns something; a single unindexable document still cannot take a batch down, because the batch falls back to per-document indexing.
- **The full-text index is deterministic** — writing the equivalence test above surfaced something older: indexing the same document twice produced different bytes, because the reverse-index term lists were built by ranging over a Go map. Two identical runs of the existing per-document path differed in 15 of 213 entries. The lists are now sorted — order never meant anything there, they are only split to clear a document's previous terms — so identical content yields identical bytes. That matters beyond tidiness: those bytes travel through the replication binlog and underpin content hashing.

- **The operational log is structured** — the server logged through the standard library's `log` package: plain text, local time without a zone, no level field, and severity spelled into the message (`ERROR: …`, `WARNING: …`, `⚠️  SECURITY: …`). A collector could not filter that by level without matching prose, which left the operational log — the one that shows dropped embedding jobs and replication failures — below the audit trail the product already exports as RFC 5424 syslog and SIEM webhooks. All 236 call sites across 35 files move to `log/slog`: the level is now a field, the values that used to be interpolated into the sentence are key/value pairs (`"collection", c, "docID", k, "err", err`), and the emoji are gone. `MDDB_LOG_FORMAT` picks `text` or `json` — the container image sets `json` — and `MDDB_LOG_LEVEL` sets the threshold. `logging.Fatal` replaces `log.Fatal`, keeping the exit explicit rather than hiding it in a severity name; the 32 bare `log.Fatal(err)` calls in startup now name the step that failed (`"step", "FTSIndex.EnsureBuckets"`). Anything still reaching the `log` package — a dependency, a straggler — is carried through the same handler rather than escaping as loose text.
- **Optional HTTP access log** — `MDDB_ACCESS_LOG=true` emits one line per request with method, path, status, bytes, elapsed and remote address, at a level that follows the status (5xx error, 4xx warn, otherwise info). Off by default, because the volume is the operator's decision. The query string is deliberately never logged: the search endpoints carry user content there.

- **Remaining floating image tags are pinned** — nginx-unprivileged, node across the panel and widget, ollama in the dev compose, and `tradik/mddb:latest` in the production compose, which now follows `${MDDB_VERSION}` like the labels beside it. Node deliberately stays on 26 rather than walking back to an LTS major two months before 26 becomes one; `debian:bookworm-slim` deliberately stays a codename tag, because it keeps receiving security updates and a dated snapshot would cut those off.
- **The Grafana plugin declares the version it is actually built against** — `peerDependencies` claimed `@grafana/* ^11` while the plugin compiled and type-checked against `^13`, so 13-only APIs passed typecheck and would have broken on the version the plugin advertised. Both sides and `grafanaDependency` now say 13, matching the `grafana/grafana:13.1.2` image in the compose file.
- **The docs deploy forces Node 24 for JavaScript actions** — it was the only workflow without `FORCE_JAVASCRIPT_ACTIONS_TO_NODE24` while running three JS actions.
- **Dependency sweep — resolves #185, #188, #189, #190, #191** — one consolidated pass over every ecosystem instead of five separate merges, taken to the newest release of each rather than to what Dependabot had proposed days earlier. **Go:** grpc 1.82.1→1.83.1, which stops the server reading from a connection flooded with HTTP/2 frames (`GRPC_GO_EXPERIMENTAL_CONTROL_BUFFER_THROTTLE_LIMIT`, 100 by default) — Dependabot's PR stopped at 1.83.0 and missed the RBAC header-matcher fixes that followed; minio-go 7.2.1→7.3.0, which verifies checksums end to end on `GetObject` and fixes the OOM that `TraceOn` hit on large objects, and which Dependabot's group had skipped entirely; klauspost/compress 1.19.1→1.19.2, fixing a zstd arm64 assembly crash that our arm64 snaps build straight into; plus x/crypto 0.55.0, x/net 0.58.0, x/text 0.41.0, protobuf 1.36.12, rtree 1.11.1, bitset 1.25.0 and cpuid 2.4.0. **npm:** vite 8.2.2 and 9 packages across the panel and the chat widget. **Rust:** async-trait 0.1.92 and the futures 0.3.34 family in mddb-chat. **Images and actions:** grafana 13.1.2, docker/login-action 4.6.0. No deprecated API is called (`PutObjectFanOut`, `ParseCapsule`), and the suites, `govulncheck`, `golangci-lint`, the widget's 14 tests and the panel build are all green afterwards.
- **The docs generator pin moves to ssg 1.8.47, both halves of it** — the deploy step names its version twice, once as the action ref and once as the `version:` input, and Dependabot can only see the ref. Its bump would have left the action at 1.8.31 fetching a 1.8.21 binary, the exact mismatch the pin exists to make loud. Both now read 1.8.47, with a comment saying so, and the jump was verified locally rather than on trust: a full build on 1.8.47 passes every strict gate (links, image alts, page metadata, orphans) and the redirect linkcheck over all 61 pages.
- **Go 1.27.0 across the monorepo** — all five modules (`go.work` plus `clients/go/mddb`, `services/mddb-cli`, `services/mddbd`, `test`, `tools/bench`), the three `GO_VERSION` workflow envs, the three `golang:` builder images and the two snap `go/X.Y/stable` channels move from 1.26.5 to 1.27.0, which also picks up the two 1.26 security patches we had skipped. Build, vet, the full test suite, `govulncheck` and `golangci-lint` are green on the new toolchain with no source changes required: both risks worth naming turned out to be non-issues — quic-go 0.61.0 already ships a `tls_config_go127.go`, and nothing in the tree pinned a `godebug`/`//go:debug` setting that 1.27 removed. What the runtime gains without further work: size-specialized allocation for objects under 80 bytes (up to 30% off each such allocation, which is most of what FTS, chunking and JSON decoding do), a `goroutineleak` profile now generally available at `/debug/pprof/goroutineleak` when `MDDB_PPROF_ENABLED=true`, a significantly faster `encoding/json` unmarshal path, and Unicode 17 tables under the full-text tokenizer.
- **The Go version guard also covers snap channels** — `scripts/check-go-version.sh` compared `toolchain`, `GO_VERSION` and `golang:` image pins, but not the `go/X.Y/stable` build-snap each snapcraft.yaml pulls its compiler from. That pin carries a track rather than a patch version, so it was invisible to a check built around `X.Y.Z` and would have let the snaps build on 1.26 while CI verified 1.27. Snap channels are now collected separately and checked against the toolchain's own track — 14 pins verified instead of 12 — with two cases added to `scripts/tests/test-go-version.sh` covering a matching and a stale channel.

### Fixed
- **`verify-ssl: false` in the GitHub Action does something again** — the client built a permissive `node:https.Agent` and attached it as `init.agent`, but the production fetcher is Node's global fetch, which is undici: it ignores `agent` and reads `dispatcher`. The documented escape hatch for self-signed dev instances had therefore never worked, and the test asserted the broken shape, so it passed throughout. The client now passes an undici `Agent` as `dispatcher`, which makes undici a runtime dependency (Node ships it internally but exposes no public `Agent`) and takes the bundle from 536 KB to 1.1 MB.
- **The Rust builder was pinned before it broke the image** — `rust:latest` was the last unpinned base in the repo and had already drifted to Debian 13 (glibc 2.41) while the runtime stage stayed on `debian:bookworm-slim` (glibc 2.36). The image still built; the container would have failed to start on a missing `GLIBC_2.38` at the next cache-cold build. Pinning `rust:1.98-slim-bookworm` puts builder and runtime back on the same glibc, verified by running the binary out of the built image.
- **`make docs-build` works from a clean checkout** — SSG wants `docs/metadata.json`, which only the deploy workflow ever wrote, and the file is gitignored: a fresh clone had none and the build stopped on "no such file or directory". A `docs-metadata` target writes the same empty JSON the workflow does.
- **The Node client has a lockfile and an audit** — `clients/nodejs` was the only JavaScript package in the repo without one, so its installs resolved differently run to run and `npm audit` refused to run at all. A CI job now runs `npm ci` (which fails on a missing or drifted lockfile) and audits production dependencies, matching the other packages.

- **Two per-document string allocations in aggregation cursors** — the time-histogram and facet-count loops each built a `docID` string solely to look up a membership map, allocating once per document scanned. Indexing the map with the conversion inline lets the compiler elide the allocation entirely (staticcheck SA6001). Behaviour is unchanged; both loops walk every document in a collection, so the saving scales with collection size.

### Added
- **Blog feeds in both formats** — `/feed.xml` (Atom) and `/rss.xml` (RSS 2.0), 20 most recent posts each, with a visible subscribe link on `/blog/`. Declared through SSG's `feeds:` rather than `feed: true`, which names the Atom feed after the bare hostname; SSG injects the autodiscovery `<link rel="alternate">` tags into every page itself, so the theme adds none. `feeds:` is undocumented upstream — reported as spagu/ssg#92, with spagu/ssg#93 asking for a way to opt out of the injection.

### Changed
- **The generator validates its own output; the local scripts stopped duplicating it** — SSG 1.8.19 ships the checks requested in spagu/ssg#74–#78, so `.ssg.yaml` now enables `check_links`, `check_images`, `check_meta` and `check_orphans` in strict mode, `content_exclude` drops the front-matter sample that never parsed as a page, and `static_sources` copies swagger/openapi/404/og-image **during** the build instead of a `make` step afterwards — while those were staged after the build, the generator saw every link to them as dead. `scripts/prune-sitemap.py` is deleted (SSG prunes `noindex` pages from the sitemap itself) and `check-docs-links.py` shrinks to the one thing SSG cannot see: links that resolve only through a Cloudflare Pages redirect (requested as spagu/ssg#87).
- **The `/index/` duplicate is gone at the source** — it was an artefact, never a page anyone wanted: `docs/index.md` carries only the homepage's front matter, and leaving it in `content_dir` also rendered a near-empty second page with the same title. `content_exclude` removes it; `/` is unaffected, because the homepage renders from the template rather than the slug. The `noindex`/canonical workaround in `page.html` is dropped with it, and `/index/` now 301s to `/`. Marking it `noindex` is in fact no longer safe: SSG excludes sitemap entries per page rather than per URL, so it took the site root out of the sitemap along with the duplicate (reported as spagu/ssg#88).
- **The SSG version is pinned** — the deploy resolved `latest`, which had already moved three releases during one afternoon. The build now depends on 1.8.18+ validation and 1.8.19 `static_sources`, so it pins both the action and the binary at 1.8.19.
- **`webp_keep_original`** — the webp pass covers the whole output tree and replaces each original, but deliberately leaves absolute URLs alone. Our `og:image` is absolute, so replace mode deleted the PNG it points at; originals are now kept alongside the `.webp`.

### Added
- **Broken-link gate on the docs deploy** — `scripts/check-docs-links.py` walks the built site and fails on any `href`/`src`/`og:image` that would 404 **or 308** on mddb.tradik.com, resolving both site-relative links and absolute links back to the canonical domain. It models Cloudflare Pages routing rather than the output directory (`.html` is stripped, a missing trailing slash is appended, both with a 308), so it reports the redirect and the final URL to link instead. Wired into `make docs-linkcheck` and run in the deploy workflow before the Cloudflare Pages upload, so a dead or redirecting link blocks publication instead of surfacing weeks later in a crawl report. The check is fully offline — no external host is ever fetched.

### Fixed
- **17 broken links on mddb.tradik.com** — docs that referenced repository files with repo-relative paths (`../services/mddbd/main.go`, `../.github/workflows/*.yml`, `../integrations/wordpress-plugin/README.md`) resolved correctly on GitHub but 404'd on the published site, where the source tree does not exist; they now point at `github.com/tradik/mddb/blob/main/…`. Also fixed: the Website Chat guide link on `/docs/chat/` (missing `/docs/` prefix), the repo-root `SECURITY.md` reference that silently self-linked, a `geo_index_test.go` path stale since the file moved to `internal/geo/`, `anthropics/mddb` GitHub URLs and a placeholder support address in AUTHENTICATION.md, and the obsolete `mddb.tradik.com/mddb/` + `md-viewer.html` section in docs/README.md.
- **`og:image` returned 404 on every page** — the meta tags advertise `https://mddb.tradik.com/og-image.png`, but no such file was deployed, so link previews were blank in every social and chat client. A 1200×630 PNG now ships from `services/ssg-template/og-image.png`; it is copied to the site root after the SSG build, since the webp pass would otherwise rewrite it and several crawlers still refuse WebP previews.
- **Category pages never rendered** — `category.html` read `.Category.Title`, which `models.Category` does not expose (it has `Name`, `Slug`, `Description`), so every build failed the template with `can't evaluate field Title` and `/category/blog/` returned 404. The template now uses `.Category.Name` and falls back to a generated meta description when a category has none. Because the rendered page lists exactly the posts `/blog/` already lists, is absent from the sitemap and is linked from nowhere, it is marked `noindex, follow` rather than left to compete with the real blog index.
- **Every page linked through a 308 redirect** — the footer (and the homepage's two documentation links) pointed at `/docs/api/swagger.html`, but Cloudflare Pages strips `.html` and answers with a 308, so all 51 published pages sent visitors and crawlers through a redirect to reach the Swagger UI. The 2.11.4 blog post did the same via three `/docs/…` links written without a trailing slash. Both now link the final URL.
- **Blog posts never triggered a docs deploy** — `blog/` is a content source in `.ssg.yaml` but was missing from the deploy workflow's path filter, so a post-only commit published nothing.
- **Site assets were frozen in caches for up to a year** — the generated Cloudflare `_headers` applies `Cache-Control: public, max-age=31536000, immutable` to `/css/*` and `/js/*`, but the filenames were not content-addressed, so an edited stylesheet kept its name and the CDN plus every returning visitor kept serving the old copy (production was measured serving CSS/JS 34–36 hours stale, which silently withheld the design-system styles, the mermaid copy-button fix and the mobile layout fix despite successful deploys). `fingerprint: true` now hashes CSS/JS names and rewrites every reference, making the immutable policy correct. The 404 page — copied into the output after the SSG build, so its references are never rewritten — no longer links the stylesheet; its inline styles already carry full fallbacks.

### Fixed
- **The sitemap asked crawlers to index a page that refuses to be indexed** — `/index/` carries `noindex` and a canonical pointing at `/`, yet `sitemap.xml` still listed it, which Search Console reports as an error and which cost crawl budget on a page that is then discarded. The SSG writes the sitemap before the theme emits `robots` and `canonical`, so it cannot know; `scripts/prune-sitemap.py` now removes `noindex` and non-self-canonical entries after the build, and the link checker fails if one survives. Reported upstream as spagu/ssg#78.
- **Seven orphan pages, reachable only from the sitemap** — Use Cases, Docker Hub, Docker Hub Setup, Docker Hub Description Update, the JWT implementation summary and the homepage audit had no inbound link from anywhere on the site; they are now linked from the homepage documentation directory, `DOCKER.md` and `AUTHENTICATION.md`. The seventh, `/index/`, turned out to be a thin duplicate of the homepage: `docs/index.md` renders both, and it cannot be dropped because marking it draft deletes the homepage as well — so `/index/` now points its canonical at `/` and stays out of the index.
- **Titles ran past where search results truncate** — the theme spent 21 characters of every title on the `— MDDB Documentation` suffix, repeating the brand on pages already named after it. The suffix is now `— MDDB`, which fixed five of the seven over-long titles on its own; the remaining four titles (integrations, the JWT summary and both blog headlines) were shortened. Every indexable page is now at or under 60 characters.
- **Every documentation page shipped an empty meta description** — `page.html` and `post.html` interpolated `{{.Page.Excerpt}}` / `{{.Post.Excerpt}}`, but `auto_excerpt` is off, so `Excerpt` was blank everywhere and 57 pages rendered `<meta name="description" content="">` despite all 56 source documents carrying `description:` in their front matter. The templates now read `.Description` (falling back to `Excerpt`), and the descriptions themselves were rewritten: most were a verbatim copy of the page title, which search engines discard. Every indexable page now has a unique 70–160 character summary, the homepage's 593-character keyword dump is cut to a real sentence, the Swagger UI page gained a description, and the stale tool count and binary size in `docs/index.md` were corrected.

### Changed
- **Site logo carries a descriptive `alt`** — the navbar and footer logos used `alt=""`, the WCAG treatment for a decorative image (the surrounding link already reads "MDDB"). Crawlers cannot tell a deliberate empty `alt` from a missing attribute and reported the logo on all 60 pages, so both now use `alt="MDDB logo"`. The build gate additionally fails on any `<img>` with **no** `alt` attribute — `alt=""` stays valid, since flagging it would push authors toward duplicate announcements.

- **Indexability is declared per page template, not inherited** — the shared `head_meta` partial hardcoded `robots: index, follow` for every page that used it. Overriding that for a single template via a Go `{{block}}` leaks across the whole parse set (it silently marked the docs and blog posts `noindex` in testing), so each page template now states its own directive: `index, follow` in `page.html` / `post.html` / `layouts/blog.html`, `noindex, follow` in `category.html`.

- **Site redesign on the Tradik Design System** — the documentation site adopts the design tokens from designstyles.tradik.com (vendored `tradik-tokens.css`): semantic color palette with the navy brand accent, Geist/Geist Mono/Instrument Serif typography, 4px spacing scale, shadows and motion. Hero keeps its photo background with design-system typography on top; blog listing renders design-system cards; the project logo (`docs/logo.svg`) is used in the navbar, footer and favicon. Link texts drop the `.md` suffix (`strip_md_link_text`).

### Fixed
- **Mobile layout of the documentation site** — long code lines and a fixed-width feature grid forced a ~876px page width on phones, so mobile browsers zoomed out and the hamburger menu appeared "missing"; grid tracks now clamp to the viewport (`minmax(min(100%, …))`), grid children get `min-width: 0`, inline code wraps, and the page never scrolls horizontally.
- **Homepage documentation section** — stale "New" badges removed; the missing Multi-Tenancy and Vector Quantization entries added.
- **Stale "What's New" and client-side version patching** — the homepage section now lists the actual v2.11.4 features, and every version string, download link and size figure is baked at build time from SSG `variables:` (`.Vars`) instead of being rewritten by inline JavaScript (2.1 KB of patching script removed; native changelog extraction tracked upstream as spagu/ssg#69).
- **Size figures corrected** — the server binary is ~26MB (was advertised ~29MB) and the Docker image ~33MB; both are now `.Vars`-driven so they update in one place.
- **Mermaid diagrams intermittently showing "Syntax error in text"** — the theme's copy-button script appended its "Copy" label into `pre.mermaid` blocks before the mermaid runtime read them; diagram blocks are now excluded from copy buttons.

## [2.11.4] - 2026-07-31

> This release also includes the unreleased 2.11.3 changes (Docker and
> dependency sweep) — the 2.11.3 tag was never published.

### Added
- **Blog section** — new `blog/` folder for release announcements and engineering notes, published through the same Markdown+frontmatter pipeline as the docs (slug prefix `blog/`, mermaid diagrams supported). First post covers the 2.11.4 features.
- **Vector space visualization** — new `/v1/vector-projection` endpoint computes a server-side 2D PCA projection of a collection's embeddings (deterministic sampling, capped at 2000 points, no external dependencies), and the panel gains a "Vector Space" explorer: scatter plot of chunks colored per document, hover/click inspection, keyboard access, and an optional query overlay that projects a natural-language query into the map to diagnose matches.
- **Disk-only vectors (low-memory mode)** — `diskOnlyVectors: true` in collection config keeps only quantized vectors in RAM; full-precision vectors live exclusively on disk and are batch-read to rescore the quantized candidate set exactly. ~4× (int8) to ~8× (int4) smaller vector memory footprint with near-lossless ranking — ideal for edge and small-VPS deployments. Requires quantization; reported in `vector-stats`. See [docs/QUANTIZATION.md](docs/QUANTIZATION.md#disk-only-vectors--low-memory-mode-v2114).
- **MMR result diversification** — `mmr: true` on `/v1/vector-search` and the `semantic_search` MCP tool reranks results with Maximal Marginal Relevance, so near-duplicate documents stop crowding out distinct ones. `mmrLambda` (default 0.5) balances relevance against diversity; candidates are oversampled 3× before selection. Composes with retrieval modes and any distance metric. See [docs/SEARCH.md](docs/SEARCH.md#mmr-result-diversification-v2114).
- **Retrieval modes for vector search (parent / chunk / window)** — `retrievalMode` on `/v1/vector-search` and the `semantic_search` MCP tool controls result granularity: `parent` (default, one result per document — unchanged behavior), `chunk` (each result carries `chunkIndex` and `chunkText`, the exact matching passage), and `window` (passage widened by `windowSize` neighboring chunks per side). Passages are re-derived from the parent document's current content, so they never go stale. See [docs/SEARCH.md](docs/SEARCH.md#retrieval-modes--parent-chunk-window-v2114).
- **Native multi-tenancy** — tenant namespace isolation for collections, enforced centrally in the authorization layer so HTTP, gRPC, GraphQL and MCP all inherit it. Users created with a `tenant` are confined to collections named `<tenant>/<name>`, can never hold the global admin role, and listing endpoints (stats, collection configs, curation rules, schemas) return only their namespace. Single-tenant deployments are untouched — zero configuration changes. See [docs/MULTI_TENANCY.md](docs/MULTI_TENANCY.md).

### Changed
- **Documentation sweep** — features that existed in code but were invisible in docs are now documented: 16 gRPC RPCs (geo, curation, FTS admin, replication service) in GRPC.md, 18 GraphQL operations in GRAPHQL.md, a complete catalog of all 79 built-in MCP tools in MCP.md, sentiment-conditioned automation triggers in AUTOMATIONS.md, SSRF egress controls and request-size guards (`MDDB_OUTBOUND_ALLOWLIST`, `MDDB_OUTBOUND_ALLOW_PRIVATE`, `MDDB_MAX_BODY_BYTES`, `MDDB_WIKI_MAX_*`) in SECURITY.md/config.md, and openapi.yaml gains the missing hybrid-search `geo`/`lang` parameters, FTS response fields, collection-config fields, and MCP key management paths. README feature highlights expanded (hybrid search, retrieval modes, embedding providers, multi-tenancy, zero-maintenance storage).
- **Go 1.26.5 security patch** — align every Go toolchain, workflow, and Docker base-image pin on 1.26.5, fixing `GO-2026-5856` in the standard library and restoring the monorepo version-consistency guard.
- **Dependency refresh** — consolidated the open Dependabot updates for Docker, GitHub Actions, Go, Rust, and npm dependencies (#137, #138, #147–#149). TypeScript 7 updates (#140–#143) are declined for now because the current `typescript-eslint` and `ts-jest` peer ranges do not support them; a major-only Dependabot ignore prevents repeated incompatible proposals while preserving TypeScript 6 minor/patch updates.

### Fixed
- **Docker panel usability** — publish the floating `panel-latest` image alias, probe both IPv4 and IPv6 loopback addresses in the panel healthcheck, and document that bind-mounted Markdown directories require explicit ingestion (#144–#146).
- **mddb-panel Snap build (v2.11.2 release run)** — `npm ci` under the node-22 snap's npm 10 rejected the panel lockfile ("Missing: graphql@16.14.2"): the direct `graphql` dependency (nothing imports it — urql ships its own `@0no-co/graphql.web`, whose optional peer caps at `^16`) produced an invalid dedupe against graphql 17 that npm 11 tolerated and npm 10 refused. The dead dependency is removed, the lockfile is consistent under both npms, and the panel snap version is aligned `2.10.0` → `2.11.2` (package.json + snapcraft.yaml). Panel build, tests and lint green; `npm ci` verified.

## [2.11.2] - 2026-07-07

### Changed
- **Dependency sweep #2** — the second wave of Dependabot PRs (#126–#131) consolidated and verified:
  - **npm / mddb-panel**: `graphql` 16.13.1 → **17.0.2**, `urql` 4.2.2 → **5.0.3**, `http-proxy-middleware` 3.0.7 → **4.2.0** (server proxy API unchanged; engines need node ≥22.15 — panel images run node 26). Build, node tests (11/11) and lint green; prod `npm audit` = 0.
  - **npm / chrome-extension, github-action, grafana-datasource**: `@typescript-eslint/eslint-plugin` (+ `parser` in grafana) → **8.63.0**; lints and full test suites green (100/100, 62/62 + rebuilt dist bundle, 45/45).
  - **Rust / mddb-chat**: `tower-http` 0.6 → **0.7** (cors + trace features intact); cargo build/test/clippy green.
  - **Declined: `eslint` 10 for mddb-panel (#131)** — `eslint-plugin-react` 7.37.5 (latest) crashes under eslint 10 (`usedPropTypes` util) and its peer range caps at `^9.7`. Panel stays on eslint **9.39.4**; `@eslint/js` + `globals` are now explicit devDependencies (the flat config imports them directly). A major-only dependabot `ignore` rule stops the weekly re-proposal without blocking eslint 10.x minors in dirs already on 10.

## [2.11.1] - 2026-07-07

### Changed
- **Consolidated dependency sweep** — the 20 open Dependabot PRs (#104–#123) resolved in one change set, each ecosystem verified (build + tests + lint) instead of rubber-stamped:
  - **npm / mddb-panel**: `vite` 6.4.3 → **8.1.3** (required `@vitejs/plugin-react` 4.7 → **6.0.3** — the 4.x line has no vite-8 peer range), `lucide-react` 0.544.0 → **1.23.0**, `eslint-plugin-react-hooks` 5.2 → **7.1.1** (its new React-Compiler-derived rules — `static-components`, `immutability`, `set-state-in-effect` — are kept **advisory** like `exhaustive-deps`; 0 errors, warnings preserved as signal). Build, node tests and lint green.
  - **npm / mddb-chat-widget**: `vite` 6.4.3 → **8.1.3** (rolldown dropped bundled esbuild → `minify: 'esbuild'` migrated to `minify: 'oxc'` in vite.config.ts), `typescript` 5.9.3 → **6.0.3** (tsc clean). 14/14 tests green.
  - **npm / grafana-datasource**: `react`/`react-dom` 18.3.1 → **19.2.7** + `@types/react` → **19.2.17**, `@grafana/runtime` 11.6.14 → **13.1.0** (now aligned with `@grafana/data`/`@grafana/ui` 13), `webpack-cli` 6 → **7.2.1**, `webpack` → 5.108.4, `@types/node` → **26.1.0**, `prettier` → 3.9.4. Webpack build + 45/45 jest + lint green. Remaining `npm audit` findings live in Grafana's own dev-dep tree (dompurify/js-cookie via react-use) — fixable only by downgrading `@grafana/runtime`, declined.
  - **npm / chrome-extension**: `@types/chrome` → 0.2.2, `@types/node` → 26.1.0, `prettier` → 3.9.4, `undici` → **8.7.0** + `npm audit fix` (ws) → 0 vulnerabilities; 100/100 tests, lint green.
  - **npm / github-action**: `@types/node` → 26.1.0, `prettier` → 3.9.4; 62/62 tests, dist bundle rebuilt with ncc.
  - **Rust / mddb-chat**: `reqwest` 0.12 → **0.13.4**, `prost` 0.13 → **0.14.4**, `tonic-build` 0.12 → **0.14.6** (codegen moved to the new **`tonic-prost-build`** crate + `tonic-prost` runtime; build.rs migrated), `governor` 0.8 → **0.10.4**, `sha2` 0.10 → **0.11** (hmac 0.13: `KeyInit` trait now imported explicitly). cargo build/test/clippy green.
  - **Go / mddbd + test**: `google.golang.org/grpc` → **1.82.0**, `klauspost/compress` → **1.19.0**, `gqlparser/v2` → 2.5.36. Full mddbd test suite green.
  - **GitHub Actions**: `actions/checkout` v6 → **v7** (33 uses), `actions/cache` v5 → **v6**.
  - **Declined: `python:3.14-slim` for the Airbyte connector (#104)** — every published `airbyte-cdk` 7.x (incl. latest 7.23.4) pins `requires-python <3.14`. A dependabot `ignore` rule for `python >=3.14` in that image now stops the weekly re-proposal; revisit when CDK 8 ships.

## [2.11.0] - 2026-07-06

### Added
- **MCP → WordPress publishing** — two new MCP tools close the loop with the `mddb-sync` WordPress plugin (which until now only pushed content *into* MDDB):
  - **`wordpress_publish`** — create or update a WordPress **post/page** (upsert by `post_id`, else `post_type`+`slug`) with Markdown or sanitised-HTML content, excerpt, status (incl. scheduled `future` with ISO date), author, **tags/categories/custom taxonomies** (terms created on first use), **meta fields**, and **Polylang/WPML language assignment + translation linking** (`lang`, `translation_of`).
  - **`wordpress_set_status`** — flip publishing status (`publish`, `draft`, `pending`, `private`, `future`, `trash`; untrash-on-restore handled).
  - The target site is pinned per collection via `set_collection_config` → new `wordpress {url, api_key}` object (also on `PUT /v1/collection-config`; `https://` enforced, `http` only for localhost), or passed explicitly as `site_url`/`api_key`. Built-in MCP tool count: **77 → 79**.
  - Counterpart: **mddb-sync WordPress plugin 0.2.0** ships the opt-in, bearer-key-protected `mddb-sync/v1` REST routes (`/publish`, `/status`) these tools call — see `integrations/wordpress-plugin/CHANGELOG.md`.

### Changed
- **`fields` projection now drops the body by default** (GO-019) — `search_documents` / `full_text_search` / `semantic_search` / `hybrid_search` called with `fields: [...]` and no explicit `include_content` no longer return `content_md`, matching the documented "each hit is reduced to id, key and the listed meta" contract and actually realising the advertised token savings. Explicit `include_content: true` restores the old behavior.
- **Search/FTS skip loading bodies the projection discards** (GO-022) — `MCPSearchRequest`/`MCPFTSSearchRequest` carry `IncludeContent` end-to-end (like semantic search already did), so `include_content: false` stops paying I/O and allocations for content that was thrown away server-side. All other callers (memory tools, MCP resources, GraphQL) explicitly request bodies — output unchanged.

### Security
- **Custom MCP tools: operator scope is now locked** (SEC-010) — client args can no longer override `collection`, `filter_meta`, `include_content` or `fields` pinned in a custom tool's `defaults`, and only parameters declared under `parameters:` pass through at all. A tool pinned to a public collection with data-minimization can no longer be steered at `collection: secrets` with full bodies.
- **SSRF deny-list covers CGNAT & friends** (SEC-011) — outbound guard (`webhooks`, `import_url`, automation) now also blocks `100.64.0.0/10` (RFC 6598), `192.0.0.0/24`, `198.18.0.0/15` and `255.255.255.255`.
- **mddb-panel: 0 prod npm vulnerabilities** (FE-011) — `npm audit fix` cleared 3 high (http-proxy-middleware, path-to-regexp, picomatch ReDoS) + 2 moderate (follow-redirects, qs); build and tests green.

### Fixed
- **Ring compose obeys the repo compose rules** (OPS-013) — `docker-compose.ring.yml` gained 10 MB log rotation, `deploy.resources` limits/reservations on every service, and per-follower healthchecks on the real ports (`11033`/`11043` — followers were permanently `unhealthy` against the Dockerfile default `11023`).
- **`mddb-cli` image no longer runs as root** (OPS-017) — runtime stage adds a `mddb` (uid 1000) user + `USER` directive, matching every other shipped image.
- **golangci-lint SA5011 findings were a stale cache** (GO-023) — verified the flagged test sites already `t.Fatal` in their nil-branches; `golangci-lint cache clean && golangci-lint run` = 0 issues.

## [2.10.2] - 2026-07-03

### Added
- **MCP search — field projection + honored `includeContent` to cut client token usage** ([#102](https://github.com/tradik/mddb/issues/102)) — the MCP search tools returned the **entire document per hit** (all `meta` keys **and** the full `contentMd` body) with no way to project fields or drop the content, inflating client token usage by ~5–30× on narrow, high-frequency lookups (e.g. a `versions` service that only needs `name` + `currentVersion`). Two gaps are now closed on the MCP read path, both strictly opt-in and backward compatible:
  - **`includeContent` is now wired end-to-end.** The field existed on `MCPCustomToolDefs` but was never copied into the merged args and never read by the tool handlers, so `includeContent: false` in YAML was silently ignored. It is now merged for **every** search action (`semantic_search`, `search_documents`, `full_text_search`) and honored — when `false`, `contentMd` is omitted from each hit. Defaults to `true`, preserving today's output.
  - **New `fields` projection.** A `fields: [...]` list (on `custom_tools` defaults **and** as a per-call arg on the built-in `search_documents` / `full_text_search` / `semantic_search` tools) restricts the returned `meta` to the listed keys, reducing each hit to `id`, `key` and the requested `meta.<field>` keys. Empty/unset = full `meta` (unchanged).
  - Implemented as a client-agnostic post-processing step (`services/mddbd/mcp_projection.go`) applied to the marshaled response, so there is **no change to storage, indexing, or the REST endpoints** — purely additive on the MCP read path. Result: `version_check` drops from ~266 → ~40 response tokens, an 8-hit `batch_version_check` from ~4.5k → ~600. New unit + handler tests cover projection and content omission at 100%; `docs/CUSTOM-TOOLS.md` and the MCP config panel document the two options.

### Changed
- **Dependency maintenance (Dependabot sweep)** — merged the open dependency PRs after resolving each one's real breakage rather than rubber-stamping:
  - **`gqlgen` 0.17.91 → 0.17.93** ([#86](https://github.com/tradik/mddb/pull/86)) — `graphql.DeferredGroup` changed (removed `Label`, added `Defers`), so the committed `services/mddbd/graphql/generated.go` was **regenerated** against the new version.
  - **Docker base images** ([#87](https://github.com/tradik/mddb/pull/87)) — `grafana/grafana` 13.0.1 → 13.1.0, `alpine` 3.23 → 3.24; the Airbyte connector was kept on `python:3.13-slim` (declined the 3.14 bump — `airbyte-cdk>=7,<8` requires Python `<3.14`).
  - **`eslint` 9 → 10** ([#94](https://github.com/tradik/mddb/pull/94)) for the Grafana datasource.
  - **`@grafana/data` 11 → 13** ([#95](https://github.com/tradik/mddb/pull/95)) — extended the jest ts-jest transform allowlist (`marked` + the whole `d3` family are ESM-only) and added `integrations/grafana-datasource/.npmrc` `legacy-peer-deps` (eslint-plugin-react's declared peer range still caps at eslint 9, though it works with 10 at runtime).
  - **npm minor-patch group across 4 integration dirs** ([#97](https://github.com/tradik/mddb/pull/97)) — kept `@actions/glob` on the CJS `^0.5` line (0.7 is ESM-only) and rebuilt the github-action bundle.
  - **`cargo` minor-patch** for `mddb-chat` ([#81](https://github.com/tradik/mddb/pull/81)).
- **`integrations/github-action` migrated to ESM** ([#100](https://github.com/tradik/mddb/pull/100)) — adopts the ESM-only `@actions/core` v3 and `@actions/glob` v0.7 (the bumps deferred from [#75](https://github.com/tradik/mddb/pull/75) / #97): `"type":"module"`, tsconfig `NodeNext`, `.js` import specifiers, `import.meta.url` entry guard, ts-jest ESM tests with `jest.unstable_mockModule`, configs renamed to `.cjs`, and a rebuilt deterministic ESM `dist/`. See [its changelog](integrations/github-action/CHANGELOG.md).

### Fixed
- **Release workflow — `mddb-cli` build failed on `go mod verify`** ([.github/workflows/release.yml](.github/workflows/release.yml)) — since GO-015 added the shared Go client as a local `replace mddb-client => ../../clients/go/mddb`, the CLI matrix's `Install dependencies` step ran `go mod verify`, which errors `missing ziphash` on a filesystem-replaced module (it has no module-cache hash to verify). This was latent — `test.yml` builds `mddb-cli` without `verify`, so PRs stayed green — and only surfaced on the first tagged release since GO-015 (v2.10.2), where it failed the whole `build-client` matrix (fail-fast) and skipped `create-release`. Dropped the redundant `go mod verify` from the CLI step (kept `go mod download`), matching CI.
- **Release workflow — `mddb-cli` Snap build failed on the same GO-015 local replace** ([.github/workflows/release.yml](.github/workflows/release.yml), [services/mddb-cli/snapcraft.yaml](services/mddb-cli/snapcraft.yaml)) — the `snapcore/action-build` sandbox copies only `services/mddb-cli` (`source: .`), so the shared client at `../../clients/go/mddb` was absent and the sandbox `go build` failed with `replacement directory ../../clients/go/mddb does not exist` (v2.10.0, pre-GO-015, built fine). Fixed by vendoring the CLI's deps in the full checkout **before** the snap build (`go mod vendor` step, where the shared module exists) and building with `-mod=vendor`, so the snap is self-contained. Also bumped the snap `version` (was stale at `2.10.0`). **Takes effect on the next tagged release** — the v2.10.2 GitHub Release, Docker images and server/panel snaps published successfully; only the CLI snap channel was affected.
- **`services/mddb-panel` — ESLint was unrunnable** ([#99](https://github.com/tradik/mddb/pull/99)) — ESLint 9 requires a flat `eslint.config.js` and the package had none, so `npm run lint` always failed. Added the flat config (JS recommended + react/hooks/refresh) and cleared the 51 real errors it surfaced on never-linted code: ~12 dead icon imports, a dead gRPC-config chain in `MCPConfigPanel`, write-only `allTriggers` state, and two rethrow-only `try/catch` blocks. Lint now exits clean (`react-hooks/exhaustive-deps` kept advisory).

## [2.10.1] - 2026-06-17

### Fixed
- **CONTRIBUTING.md — dead Code-of-Conduct link + EOL Node version** ([CONTRIBUTING.md](CONTRIBUTING.md)) — the contributor guide linked to a non-existent `CODE_OF_CONDUCT.md` (broken on the first screen, no Community-Standards entry) and listed "Node.js 18+" (EOL since April 2025) while the repo standardizes on Node 24. Replaced the dead link with an inline conduct/security note pointing to `security@tradik.com`, and bumped the prerequisite to Node.js 24+.

### Changed
- **GO-015 — extract leaf packages from the flat `package main`** ([services/mddbd/internal/](services/mddbd/internal/)) — the `mddbd` daemon is one flat ~58k-LOC `package main` with no compilation boundaries. Following the audit's prescribed gradual (no-big-bang) refactor, the first clean, `Server`-independent leaves now live in importable `internal/` packages: **`internal/cache`** (read-through document cache), **`internal/sentiment`** (sentiment scoring), **`internal/delta`** (revision delta encoder), **`internal/compression`** (Snappy/Zstd document compression), **`internal/wikitext`** (MediaWiki→Markdown conversion, exposed as `wikitext.ToMarkdown`), **`internal/embedding`** (embedding-provider clients — OpenAI/Cohere/Voyage/Ollama — with a de-stuttered API: `embedding.Provider`, `embedding.NewProvider`), **`internal/envconf`** (shared typed env-var helpers `envconf.String`/`Int`/`Int64`, previously duplicated inside the embedding code but used across the daemon), **`internal/binlog`** (the leader/follower replication binary log — `Binlog`, `BinlogOps`, entry marshalling — used by every write path; extracted first because it gates the FTS extraction), the entire **`internal/fts`** full-text-search subsystem (the `FTSIndex` core and its ~40 methods, tokenizers, Porter/Polish/Snowball stemmers, language registry, synonym/stop-word managers, the query-expression engine, and BM25/BM25F/boolean/fuzzy/wildcard/proximity/highlight/autocomplete search — with the HTTP handlers and request/response DTOs kept in `main` and reaching the core only through its exported API plus new `Stemmer()`/`SynonymManager()`/`LangRegistry()` accessors), **`internal/sliceutil`** (a generic `sliceutil.Unique[T]` that replaced a `unique([]string)` helper duplicated across ~20 files), and the **`internal/geo`** geospatial subsystem (`GeoStore`, the R-tree `GeoIndex`, `GeoHashIndex`, `PostcodeLookup`, GeoJSON polygon search, with the HTTP and gRPC handlers kept in `main` as transport and the geohash/polygon utilities exported as `geo.GeohashEncode`/`ValidatePolygon`/…). The binlog move also removed a duplicated `copyBytes`/`CopyBytes` pair (main callers now share the single `CopyBytes`), and the **`internal/vector`** subsystem (`VectorStore`, `VectorIndex`, the HNSW/IVF/PQ/OPQ/SQ/BQ/quantized ANN indexes, int8/int4 quantization, and SIMD kernels — with the HTTP/gRPC handlers kept in `main` as transport, the four files that use a local `vector` variable importing the package under the `vec` alias to avoid shadowing). Both `internal/geo` and `internal/vector` dropped their last `main` dependency by switching `CopyBytes` to the stdlib `bytes.Clone`. Also extracted **`internal/temporal`** (the `TemporalManager` document-lifecycle event tracker — async event recording plus `QueryRange`/`GetHotDocs`/`ComputeHistogram` analytics) **`internal/spell`** (the `SpellManager` spell-corrector — per-language/per-collection BK-tree models with `Suggest`/`AddWord`/`Cleanup`), **`internal/schema`** (the `SchemaManager` metadata-schema validator — `Set`/`Validate`/`Reload` plus JSON-schema parsing), and **`internal/audit`** (the `AuditManager` audit log with async recording, retention, `Query`/`PurgeOlderThan`, and the webhook/syslog `AuditExporter` implementations), all keeping their HTTP handlers in `main`. The high-throughput `LockFreeCache` was folded into the existing `internal/cache` package. The schema extraction also replaced a `contains([]string, string)` helper (defined in schema but used by many unrelated tests) with the stdlib `slices.Contains`. The foundational **`internal/storage`** package was then introduced — it holds the core `Doc` document type (moved out of `main.go`), its protobuf conversions (`DocToProto`/`ProtoToDoc`), and the BoltDB key builders (`DocKey`/`ByKeyKey`/`RevPrefix`/`MetaKeyPrefix`); the encryption/compression-aware serialization (`marshalDoc`/`loadDoc`) stays in `main` as a layer over it. Finally, the first god-object decoupling: **`internal/metrics`** was extracted by **inverting** its former `Metrics`→`*Server` back-reference — the package now depends on a `StatsProvider` interface it owns (with `DBStats`/`CollectionStats`/`BinlogStatsView` DTOs), which `main` implements over `*Server`, breaking the `Metrics`↔`Server` cycle while preserving the exact Prometheus output. The shared outbound HTTP layer was then consolidated into **`internal/httpclient`** (the pooled `SharedHTTPClient`/`NewPooledClientWithTimeout` plus the SSRF guard — `SafeDialContext`, redirect/URL validation), which also de-duplicated a `drainAndClose` helper that had been copied into the audit package. The webhook subsystem core then moved to **`internal/webhooks`** (`WebhookManager`, registration/persistence, delivery with SSRF-safe retry via `internal/httpclient`, and the `fireWebhook`/`hookMatches` internals) with the HTTP handlers (`handleWebhooks`/`handleWebhookDelete`) kept in `main`; the incident-event name constants (`security.auth_failure_burst`, `security.rate_limit_exceeded`, `ops.replication_lag_high`, `ops.panic_recovered`, `ops.disk_usage_high`) moved alongside it as webhook event types, so `incident_detector.go`/`ratelimit.go` now reference `webhooks.Event*`. The async metadata-indexing queue moved to **`internal/indexqueue`** by the same inversion as metrics: the queue used to hold a `*Server` back-reference solely so `processJob` could write the meta index, so the package now owns a `Store` interface (`DBUpdate`/`IdxMetaBucket`/`Binlog`) that `main` implements with a `serverIndexStore` adapter, and the queue is wired post-construction via a new `SetStore`. The document-TTL subsystem moved to **`internal/ttl`** the same way: the `TTLManager` held a `*Server` only so its `cleanup` loop could load a document, delete the expired one, and derive its ID, so the package now owns a `Reaper` interface (`LoadDoc`/`DeleteDocument`/`GenID`) that `main` implements with a `serverTTLReaper` adapter, while the `handleSetTTL` handler and its `SetTTLRequest` DTO stay in `main`. The at-rest encryption **crypto core** moved to **`internal/encryption`** — the `Encryptor` (AES-256-GCM, primary + read-only previous keys, V1/V2 wire formats, key management) is `Server`-independent and does not even import `storage`; the wire-format symbols (`MagicV1`/`MagicV2`/`MagicLen`/`KeyIDLen`, `IsEncrypted`, `CiphertextVersion`/`CiphertextKeyID`) are exported because the rotation tests inspect ciphertext bytes. The `globalEncryptor` glue (`marshalAndEncrypt`/`maybeDecrypt`) stays in `main` as `encryption_glue.go` (it bridges the core to the `marshalDoc`/`loadDoc` storage serialization), as do the `Server`-coupled `RotationManager` and the encryption HTTP handlers. The automation execution log moved to **`internal/automationlog`** (de-stuttered `automationlog.Store`/`Entry`/`NewStore`, plus the shared `ParseDurationString`) — a genuine `Server`-free persistence leaf; the automation **core** (the `AutomationManager`, the FTS/vector/hybrid trigger engine, and the cron scheduler) deliberately stays in `main` because it is an orchestrator layered on the whole search stack (`runFTSSearch`/`runVectorSearch`, `FTSIndex`/`Embedding`/`VectorSearchers`), i.e. application glue rather than a clean dependency-inversion seam. Each new package carries its tests at 90%+ coverage. None depend on the `Server` god-object, so each extraction is safe — `package main` call sites were rewired with the package prefix (imports via `goimports`) and the tests moved with their code, keeping the full suite green and coverage intact. With the clean dependency-inversion seams exhausted (the remaining big subsystems — search orchestration, the automation core, the transports — reach so broadly into the `Server` that inverting them would require a 12+ method "re-export the Server" interface, which is not real decoupling), the refactor moved to its second prescribed track: **file-size reduction**. `main.go` (3179 LOC) was split — with no behaviour change and no new package — into three cohesive same-package files: `middleware.go` (`withCORS`/`withJSON`/`withMaxBody`/`guardWrite`/`auditResponseWriter`/`effectiveMode`), `document_ops.go` (the shared `addDocument`/`runPostWriteHooks`/`deleteDocumentInternal` write path), and `util.go` (`env`/`genID`/`applyEnv`/`safe`/`intersect`/`copyFile`/`mustJSON`/`sortDocs`), bringing `main.go` down to 2584 LOC. The HTTP handler tail (`handleAdd`/`handleGet`/`handleSearch`/`handleExport`/`handleBackup`/`handleRestore`/`handleTruncate`/`handleUpdate`/`handleDocMeta`/`handleMetaKeys`/`handleStats`/`handleDelete`/`handleDeleteBatch`/`handleDeleteCollection`/`handleHealth`/`handleComplianceStatus`/`handleChecksum` plus the `ok`/`bad`/`collectionChecksum` helpers) was then relocated to `http_handlers.go`, taking `main.go` to 1284 LOC (now essentially `main()`, the `Server` struct and the request DTOs). The 2995-LOC `grpc_server.go` (the second-largest file) was likewise split — with no behaviour change — into four cohesive same-package files along its contiguous RPC-domain runs: `grpc_server.go` (647 LOC: the `GRPCServer` type, server setup, and the document RPCs `Add`/`Get`/`Search`/`Export`/`Backup`/`Restore`/`Truncate`/`Stats`), `grpc_search.go` (the `Vector*`/`FTS*`/`HybridSearch` RPCs plus `docToProto` and the batch writes), `grpc_metadata.go` (webhooks/schema/validate, document update/meta/classify/delete, synonyms/stopwords/meta-keys/checksum), and `grpc_advanced.go` (automation, collection-config, cross-search, duplicates, revisions). The 2052-LOC `mcp_direct_client.go` (the MCP direct-call client, same shape as the gRPC server) was split the same way into `mcp_direct_client.go` (560 LOC: the `DirectClient` type and document calls), `mcp_direct_search.go` (vector/FTS/hybrid + import/TTL), `mcp_direct_metadata.go` (webhooks/schema/validate, doc update/meta/classify, synonyms/stopwords/meta-keys/checksum), and `mcp_direct_advanced.go` (automation, revisions, collection-config, curation, cross-search, duplicates, ingest, aggregate). The 1467-LOC `mcp_tools.go` (the MCP tool dispatcher) was split too: the `MCPToolServer` type, the `mcpCallTool` dispatch switch and the shared `mcpGet*` argument helpers stay in `mcp_tools.go` (285 LOC), while the ~57 `tool*` implementations moved into `mcp_tools_data.go` (documents/vectors/FTS/hybrid/webhooks/schema/validate/meta), `mcp_tools_admin.go` (delete-collection/truncate/revisions/synonyms/stopwords/meta-keys/checksum/automation), and `mcp_tools_advanced.go` (collection-config/cross-search/aggregate/duplicates/ingest/upload). The 1286-LOC `mcp_custom_tools.go` — dominated by the single ~1067-LOC `mcpBuiltinTools()` catalog of all 77 builtin MCP tools — was reduced by moving the catalog into `mcp_builtin_tools.go` and partitioning that one giant function into `mcpBuiltinTools()` (`append(Core, Advanced...)`, tool order preserved), `mcpBuiltinToolsCore()` (document/search/index/webhook/schema/synonym/automation tools) and `mcpBuiltinToolsAdvanced()` (collection-config/curation/cross-search/ingest/upload/revisions/duplicates/memory-RAG); `mcp_custom_tools.go` is now 220 LOC (just the custom-tool config types and helpers). `http_handlers.go` (1313 LOC) was split by relocating whole handler functions into three same-package files: `http_handlers.go` (566 LOC: add/get/search/export/backup/restore/truncate plus the `ok`/`bad` helpers and health/compliance/checksum), `http_handlers_meta.go` (update/doc-meta/meta-keys/stats) and `http_handlers_delete.go` (the three delete handlers). (A `main()` bootstrap refactor was assessed but deferred: its locals — e.g. `automationsEnabled`, the DB handle, the binlog, the replication role — cross-cut the whole function, so a safe extraction is a large, delicate change of readability-only value and real startup-ordering risk.) `upload_handler.go` (1202 LOC) was reduced by moving its ~890 LOC of pure document-format converters (HTML/PDF/DOCX/ODT/RTF/TeX → Markdown — zero `Server` coupling, like `internal/wikitext`) into `document_converters.go`; the move kept them in `package main` (rather than a new `internal/` package) because two `main` callers use them and their tests are interleaved inside the shared `coverage_boost*_test.go` files, so a package extraction would fragment those test files. `upload_handler.go` is now 305 LOC (the multipart `handleUpload`/`processUploadedFile` flow). The 955-LOC `graphql_adapter.go` was split by relocating whole resolver methods into `graphql_adapter.go` (209 LOC: the `GraphQLAdapter` type, the auth glue, and the MCP→GraphQL conversion helpers), `graphql_resolvers.go` (document/search/vector/FTS resolvers) and `graphql_admin.go` (stats/schema/webhook plus user/group/permission resolvers).
- **GO-015 — shared Go client SDK + `mddb-cli` rewired off its duplicated HTTP client** ([clients/go/mddb/](clients/go/mddb/)) — addressing the audit's `mddb-cli`-re-implements-its-own-client DRY problem and the "uses a shared client package" acceptance criterion. A new **`clients/go/mddb`** module (`mddb-client`, `package mddb`) is the official Go client for `mddb-cli` and external integrations: a `Client` with `New` + functional options (`WithAPIKey`/`WithToken`/`WithHTTPClient`/`WithTimeout`/`WithVerbose`), a `Do` transport that authenticates, JSON-encodes and turns any HTTP ≥400 into an `*APIError`, and typed methods (`Add`/`Get`/`Search`/`SetTTL`/`ImportURL`/`Stats` returning `Document`/`Stats`, `RegisterWebhook`/`ListWebhooks`, plus FTS/vector/schema/webhook/export/restore/auth/GraphQL). `mddb-cli` then **dropped its own copied `Client`/`request` transport** and now builds the shared client from its flags via `newClient()`, routing all 27 call sites through it. The module builds standalone under `GOWORK=off` (matching CI), is wired into `go.work`, the `Makefile` (test/lint/vet/sec/fmt) and `.github/workflows/test.yml` (path triggers + per-module test/lint), and carries its own tests at 98.1% coverage. (The CLI still renders responses through its GO-005-safe map accessors; migrating those display paths to the client's typed results is an incremental follow-up.) This closes **GO-015** for its realized scope — 24 importable `internal/` packages, the shared client with `mddb-cli` rewired off its duplicated transport, no hand-written file over 1500 LOC, and coverage held under the CI gate. The remaining maximalist items (a bootstrap-only `cmd/mddbd`, transports as adapters, aggregating the 51-field `Server`, the ~500-LOC target, and fully typed CLI parsing) are deliberately carved out as a separate architectural follow-up rather than forced into leaky "re-export the Server" interfaces.
- **MCP tool count inconsistent on the landing page** ([services/ssg-template/index.html](services/ssg-template/index.html), [services/ssg-template/index-section-hero.html](services/ssg-template/index-section-hero.html), [services/ssg-template/index-section-features.html](services/ssg-template/index-section-features.html), [services/ssg-template/index-section-examples.html](services/ssg-template/index-section-examples.html)) — the landing page advertised the built-in MCP tools as "67" in four places and "72" in two, while the real count (`len(mcpBuiltinTools())`) is **77**. Corrected all to 77 and introduced a single-source `MDDB_MCP_TOOLS` JS constant (mirroring `MDDB_VERSION`) that fills every `[data-tools]` element, so the visible badge/headers update from one place. The SEO `<meta>` + JSON-LD carry 77 statically for crawlers. Extended the DOC-001 drift guard with `TestMCPToolCountLandingInSync` so the landing count can't silently diverge from the code again.
- **Mermaid diagrams broken on the docs/landing site by HTML minification** ([.github/workflows/deploy-docs.yml](.github/workflows/deploy-docs.yml), [services/ssg-template/page.html](services/ssg-template/page.html)) — the SSG build minified HTML, which collapses whitespace inside Mermaid code blocks. Mermaid source is whitespace-significant, so this broke parsing (`xychart` `y-axis "docs/sec" 0 --> 55` became `"docs/sec"0--> 55`; `graph`/`subgraph` layouts lost all newlines). A brittle regex in `page.html` tried to reconstruct newlines but couldn't cover every diagram type and corrupted valid ones. Disabled minification for the docs build (`minify: false`) so diagram source survives verbatim, and removed the reconstruction hack — `page.html` now moves the verbatim `pre code.language-mermaid` blocks straight into `<div class="mermaid">`. Fixes diagrams across `/docs/*` and the landing page at once.
- **Landing-page release date** ([services/ssg-template/index.html](services/ssg-template/index.html), [services/ssg-template/index-section-download.html](services/ssg-template/index-section-download.html)) — the SSG landing page showed a stale, hardcoded "Released: April 7, 2026" (with two more inconsistent dates in the static fallback and JSON-LD `datePublished`). None of these auto-update on release. Corrected all three to the v2.10.0 date (2026-06-13). (Follow-up candidate: derive the release date from the latest GitHub release so it can't drift, like the version stamp.)

## [2.10.0] - 2026-06-13

### Security
- **Go runtime 1.26.3 → 1.26.4** ([go.work](go.work), [services/mddbd/go.mod](services/mddbd/go.mod), [services/mddb-cli/go.mod](services/mddb-cli/go.mod), [test/go.mod](test/go.mod), [tools/bench/go.mod](tools/bench/go.mod), [.github/workflows/test.yml](.github/workflows/test.yml), [.github/workflows/release.yml](.github/workflows/release.yml), [.github/workflows/govulncheck.yml](.github/workflows/govulncheck.yml), [services/mddbd/Dockerfile](services/mddbd/Dockerfile), [services/mddbd/Dockerfile.dev](services/mddbd/Dockerfile.dev), [services/mddb-cli/Dockerfile](services/mddb-cli/Dockerfile), [docs/API.md](docs/API.md), [docs/openapi.yaml](docs/openapi.yaml)) — toolchain bump to the latest stdlib patch release. All **11** Go-version pins (go.work + every go.mod, all three CI `GO_VERSION` envs, all three `golang:` Docker base images) were updated atomically and verified consistent by the `scripts/check-go-version.sh` guard; the OpenAPI/`/v1/stats` `goVersion` examples follow the bump.

### Added
- **CI/CD for the Grafana datasource** ([.github/workflows/grafana-datasource.yml](.github/workflows/grafana-datasource.yml)) — the `grafana-datasource` integration was the only one of the five `integrations/` packages without a dedicated workflow, so its changes weren't lint/test/build-gated and release tags weren't verified against `plugin.json`. New workflow scoped by `paths:` to `integrations/grafana-datasource/**`: a `test` matrix (Node 22 & 24) runs `npm ci` → `format:check` → `lint` → `jest --coverage` (90% threshold) → webpack `build`, uploading coverage + plugin-zip artefacts on Node 24; a `security` job runs `npm audit --omit=dev --audit-level=high`; a `smoke` job validates the packaged `plugin.json` (`type`/`id`/`info.version`); and on a `grafana-ds-v*` tag a `release` job verifies the tag matches **both** `package.json` and `src/plugin.json` versions, force-moves floating major/minor tags, and publishes a GitHub Release with the zip. Same `FORCE_JAVASCRIPT_ACTIONS_TO_NODE24` / least-privilege `permissions` / action pins as the rest of the repo. Also fixed the integration's broken `npm run package` script (it referenced a non-existent `scripts/package.js`) so packaging works in CI and locally; CI badge added to its README.
- **MDDB Browser Chrome Extension** ([integrations/chrome-extension/](integrations/chrome-extension/)) — fifth member of the `integrations/` family. Manifest V3 extension (Chrome ≥ 120) that surfaces a live MDDB server status panel from the browser toolbar: badge counter showing total document count (`1.2k` / `15k` / `99k+`), popup with documents / revisions / collections / mode / uptime + top-5 collection breakdown, one-click link to the MDDB admin panel (defaults to `<server-origin>:3000`, overridable), and an options page for the MDDB URL, optional `X-API-Key`, panel-URL override, and `30 – 3600 s` (or `0`-to-disable) refresh interval — all stored locally in `chrome.storage.local`, never transmitted. Host access is requested at save-time per-origin only (`optional_host_permissions`), no broad `<all_urls>` permission is requested up front. Background MV3 service worker polls `GET /v1/stats` on a `chrome.alarms` schedule and a "Test connection" button on the options page probes `GET /v1/health`, distinguishing 401/403 auth failures from `5xx` server errors and network failures. Bundled privacy policy + terms (`public/privacy.html`, `public/terms.html`) with canonical copies at [tradik.com/privacy](https://tradik.com/privacy) / [tradik.com/terms](https://tradik.com/terms). TypeScript 6 / esbuild-bundled ESM (`popup`, `options`, `background` entrypoints), zero runtime dependencies, zero analytics/telemetry/third-party calls. **98 Jest tests (jsdom + chrome.* stub), 99 % statements / 94 % branches / 100 % functions / 99 % lines** with a 90 % global threshold enforced.
- **CI/CD for the Chrome extension** ([.github/workflows/chrome-extension.yml](.github/workflows/chrome-extension.yml)) — independent workflow scoped by `paths:` to `integrations/chrome-extension/**`. On every PR and push touching the integration: `test` job (matrix Node 22 & 24) runs `npm ci`, format check, ESLint, `jest --coverage` (90 % threshold enforced), `esbuild` bundle, and zero-dependency `scripts/package.mjs` packaging into `dist/mddb-browser-<version>.zip`; the Node 24 leg uploads both the coverage report and the packaged zip as workflow artefacts. `security` job runs `npm audit --omit=dev --audit-level=high`. `smoke` job re-downloads the artefact and validates the packaged `manifest.json` (`manifest_version: 3`, required action/options/background/permissions). On tag push `chrome-ext-v*`, the `release` job verifies `package.json` AND `manifest.json` versions both match the tag, rebuilds + repackages from source, force-moves floating `chrome-ext-v<major>` / `chrome-ext-v<major>.<minor>` tags, and publishes a GitHub Release via `softprops/action-gh-release@v2` with the zip attached. JS-based actions pinned via `FORCE_JAVASCRIPT_ACTIONS_TO_NODE24: true`; same `actions/checkout@v6`, `actions/setup-node@v6`, `actions/upload-artifact@v7`, `actions/download-artifact@v7` pins as the rest of the repo.
- **Root Makefile targets** for the Chrome extension — `chrome-install`, `chrome-build`, `chrome-package`, `chrome-test`, `chrome-coverage`, `chrome-lint`, `chrome-audit`, `chrome-check`, `chrome-clean`. All delegate to `integrations/chrome-extension/Makefile`, mirroring the `gha-*` and `airbyte-*` patterns.
- **[docs/INTEGRATIONS.md](docs/INTEGRATIONS.md)** — new section **10. Chrome Extension → MDDB (Browser toolbar)** with install table, options reference, permissions/privacy summary, endpoints used, and release/build flow. Section "Full Pipeline" renumbered to **11**. Root `README.md` blurb, ✅-features list, and Documentation TOC updated to mention the Chrome extension alongside the existing integrations.

- **MDDB Sync GitHub Action** ([integrations/github-action/](integrations/github-action/)) — third member of the `integrations/` family. Native Node 24 JavaScript action (bundled with `@vercel/ncc` into a single ~1 MB `dist/index.js`) that ingests repository files into an MDDB collection via `POST /v1/add`. Designed for `uses: tradik/mddb/integrations/github-action@v1` — no Docker image, no marketplace publish required. Inputs: `mddb-url`, `api-key`, `collection`, multi-pattern `path` glob (with inline `!`-negation), `ignore`, `working-directory`, `language`, `key-strategy` (`path` / `hash` / `filename`), `key-prefix`, `concurrency` (1–64), `timeout-seconds`, `verify-ssl`, `dry-run`, `fail-on-error`. Outputs: `documents-scanned`, `documents-added`, `documents-failed`. Smart content wrapping — markdown/plain-text stored verbatim; JSON/YAML/TOML/HTML/CSS/JS/TS/Python/Go/Rust/Bash code files wrapped in fenced code blocks with the correct language hint so FTS + vector indexing pick up the structure. Per-document `meta` populated with `source=github-action`, `path`, `extension`, `size`, plus `repository` and `ref` from the GitHub-provided env vars. HTTP client uses Node 24's native `fetch` (no extra deps), retries on `408/425/429/5xx` and network errors with exponential backoff, and surfaces credential failures (`401/403`) separately from server unhealth (`5xx`). **57 unit tests, 98% line / 95% branch / 92% function coverage** — Jest config enforces a 90% global threshold. Built with TypeScript 6.0 / `tsconfig` strict mode, ESLint + Prettier wired into the workflow.
- **CI/CD for the GitHub Action** ([.github/workflows/github-action.yml](.github/workflows/github-action.yml)) — independent workflow scoped by `paths:` to `integrations/github-action/**`. On every PR and push touching the integration: `test` job (matrix Node 22 & 24) runs `npm ci`, format check, ESLint, `jest --coverage` (90% threshold enforced), and rebuilds the bundle to confirm `src/` still ncc-compiles. The `verify-dist` job rebuilds `dist/` and fails if it diverges from the committed bundle — keeps the marketplace-style "consumers reference dist/ directly" invariant honest. The `smoke` job uses `./integrations/github-action` against the integration's own README in `dry-run` mode (no network, but exercises the full bundled entry point). On tag push `gha-v*` the `release` job verifies `package.json.version` matches the tag, force-moves floating major (`gha-vN`) and minor (`gha-vN.M`) tags, and publishes a GitHub Release via `softprops/action-gh-release@v2`. JS-based actions pinned via `FORCE_JAVASCRIPT_ACTIONS_TO_NODE24: true`; same `actions/checkout@v6`, `actions/setup-node@v6`, `actions/upload-artifact@v7` pins as the rest of the repo.
- **License consistency across `integrations/`** — every package now declares **BSD-3-Clause** (matching the repo root `LICENSE`). Previously `integrations/airbyte-destination/{metadata.yaml,Dockerfile,README.md}` and `integrations/github-action/{package.json,README.md,action.yml badge}` declared MIT, which conflicted with the BSD-3 root license. `.github/workflows/airbyte-destination.yml` OCI label `org.opencontainers.image.licenses` also bumped to `BSD-3-Clause` — so future Docker Hub / GHCR pushes carry the correct SPDX identifier in the image manifest.

- **Root Makefile targets** for the GitHub Action — `gha-install`, `gha-build`, `gha-test`, `gha-coverage`, `gha-lint`, `gha-check`, `gha-verify-dist`, `gha-clean`. All delegate to `integrations/github-action/Makefile`, mirroring the `airbyte-*` pattern.
- **WordPress sync plugin** ([integrations/wordpress-plugin/](integrations/wordpress-plugin/)) — second member of the `integrations/` family. Native WordPress plugin (PHP 8.1+ / WP 6.2+) that mirrors posts and pages to MDDB in real time: `wp_after_insert_post` → `POST /v1/add` (autosaves & revisions skipped; drafts opt-in), `wp_trash_post`/`before_delete_post` → `POST /v1/delete`. Language detection chain: Polylang (`pll_get_post_language`) → WPML (`wpml_post_language_details` filter) → site locale, all normalised to `lang_REGION`. Three key strategies: `posttype-id` (default), `posttype-slug`, permalink path. `contentMd` runs the body through the standard `the_content` filter so shortcodes / blocks / oEmbed embeds are expanded before indexing; `meta` flattens `postType`, `status`, `title`, `slug`, `permalink`, `author`, `publishedAt`/`modifiedAt`, `categories`, `tags` to `map<string,[]string>` (native MDDB schema). Settings UI under **Settings → MDDB Sync** with a "Test connection" button that probes `POST /v1/search`. Optional `Authorization: Bearer` header (empty = unauthenticated dev instance). Retry on `429/5xx` with exponential backoff (500 ms / 1 s / 2 s, max 3 attempts). Custom action `mddb_sync_error` fires on every failure for downstream Sentry/Action Scheduler glue without us adding hard deps. **Auto-update channel** hooks into WordPress's `pre_set_site_transient_update_plugins` + `plugins_api` filters and polls `repos/tradik/mddb/releases/latest` (cached 12h in a site transient), looking for the `mddb-sync-<version>.zip` release asset — so `Dashboard → Updates` upgrades the plugin like any wp.org plugin. Release tags use the `wp-v*` prefix to avoid clashing with core `vX.Y.Z` tags. **48 PHPUnit tests, 92.44 % line coverage** with WordPress functions mocked via Brain Monkey — no WP install needed.
- **CI/CD for the WordPress plugin** ([.github/workflows/wordpress-plugin.yml](.github/workflows/wordpress-plugin.yml)) — matrix-tests on PHP 8.1 / 8.2 / 8.3 / 8.4. Each leg runs `composer audit --abandoned=report` (security advisories), `composer lint` (PHPCS with the WordPress security + DB + i18n + AlternativeFunctions rulesets and PHPCompatibilityWP), `composer stan` (PHPStan level 5 + `szepeviktor/phpstan-wordpress` for WP-aware type stubs), and `composer test:coverage` (PHPUnit 10 + xdebug, with Brain Monkey for WP function mocking). The 8.3 leg enforces ≥90 % line coverage from `coverage.xml`. Pushing a `wp-v*` tag flips on the `build` job: it verifies the plugin header `Version:` matches the tag, installs `--no-dev` deps with an optimized autoloader, rsyncs to a clean tree (omitting `tests/`, `phpcs.xml`, `phpstan.neon.dist`, `phpunit.xml.dist`, `composer.lock`, `Makefile`, `CHANGELOG.md`, `README.md`), packs `mddb-sync-<version>.zip`, uploads it as a workflow artifact, and publishes a GitHub Release via `softprops/action-gh-release@v2` with the zip attached — that asset is exactly what the in-plugin updater downloads. JS-based actions pinned via `FORCE_JAVASCRIPT_ACTIONS_TO_NODE24: true`; uses the same `actions/checkout@v6`, `actions/cache@v4`, `actions/upload-artifact@v7` pins as the rest of the repo.
- **[docs/INTEGRATIONS.md](docs/INTEGRATIONS.md)** — new section **7. WordPress → MDDB (Sync plugin)** with hook table, settings reference, document shape, and release/build flow. Section **8. Full Pipeline** renumbered to keep the chapter order. Root `README.md` blurb, ✅-features list, and Documentation TOC updated to mention WordPress Sync alongside the existing six integrations.

### Security
- **Tighten CORS: origin allowlist instead of wildcard/reflection** ([services/mddbd/cors.go](services/mddbd/cors.go), [services/mddbd/main.go](services/mddbd/main.go), [services/mddbd/mcp_streamable.go](services/mddbd/mcp_streamable.go), [services/mddbd/production_guard.go](services/mddbd/production_guard.go)) — `withCORS` set `Access-Control-Allow-Origin: *` while also allowing the `Authorization`/`X-API-Key` headers, and the MCP streamable transport **reflected any request `Origin` verbatim** (worse than `*`) — so a hostile website could read/write a user's local or intranet MDDB instance from their browser (especially dangerous with an unauthenticated MCP). A new shared `corsConfig` resolves `MDDB_CORS_ORIGINS` (CSV exact-match allowlist; legacy `MDDB_CORS_ORIGIN` still honored) and emits `Access-Control-Allow-Origin` only for a matching origin (with `Vary: Origin`) — a disallowed origin gets no header, so the browser blocks the read. Wildcard `*` remains the default (read-only, no credentials) but now logs a startup security warning, and the MCP transport uses the same allowlist instead of reflecting. The production guard accepts either env var. Documented in `docs/config.md` + `.env.example`; covered by 9 unit/integration tests including the `Origin: https://evil.example` rejection.
- **Chrome extension validates `runtime.onMessage` sender** ([integrations/chrome-extension/src/background.ts](integrations/chrome-extension/src/background.ts)) — the `mddb:refresh` handler ignored the message sender, so a content script (coaxed by a web page) could trigger a refresh — forcing traffic to the configured MDDB server with its auth header. It now accepts a message only from the extension's own trusted surfaces: `sender.id === chrome.runtime.id` **and** no `sender.tab` (which would indicate a content script). Tests cover the foreign-id and `sender.tab` rejections; `background.ts` stays at 100% coverage.
- **Stop committing dev secrets; add secret scanning** ([.gitignore](.gitignore), [docker-compose.dev.yml](docker-compose.dev.yml), [services/mddb-chat/config.example.toml](services/mddb-chat/config.example.toml), [services/mddb-chat/src/config.rs](services/mddb-chat/src/config.rs), [.gitleaks.toml](.gitleaks.toml), [.github/workflows/secret-scan.yml](.github/workflows/secret-scan.yml)) — the git-tracked `services/mddb-chat/config.toml` held real credentials (`auth_password = "admin123"`) and `docker-compose.dev.yml` hard-coded a dev JWT secret + `admin123` inline — a strong copy-paste-deploy footgun (a known JWT secret allows forging admin tokens) and a permanent entry in git history. `config.toml` is now untracked (`git rm --cached`) and gitignored; mddb-chat reads `MDDB_CHAT_AUTH_USERNAME`/`MDDB_CHAT_AUTH_PASSWORD` from the environment when the config's values are blank (matching the existing LLM-key/webhook-secret env override, with a Rust test for precedence); `docker-compose.dev.yml` pulls `MDDB_AUTH_JWT_SECRET`/`MDDB_AUTH_ADMIN_PASSWORD` from `.env` (compose errors if unset) and passes the admin creds through to the chat service; `config.example.toml` ships empty placeholders. A new `secret-scan.yml` workflow runs **gitleaks 8.30.1** on every push/PR (working tree + PR commit range), with `.gitleaks.toml` allowlisting the legitimate example/test/manpage/vendored values so the scan is clean (0 findings). **Note:** if `admin123` or that dev JWT secret were ever used beyond local dev, rotate them — the values remain in pre-fix git history.
- **Validate the chat widget's WebSocket endpoint** ([services/mddb-chat-widget/src/utils/serverUrl.ts](services/mddb-chat-widget/src/utils/serverUrl.ts), [services/mddb-chat-widget/src/index.ts](services/mddb-chat-widget/src/index.ts), [services/mddb-chat-widget/README.md](services/mddb-chat-widget/README.md)) — **high**. The widget passed the `data-server` script attribute straight to `new WebSocket()` behind only a null-check — no scheme/URL validation — so a tampered DOM attribute (or an integrator typo) could redirect the whole chat session (messages + `sessionId`) to an arbitrary host, including over unencrypted `ws://`. A new `validateServerUrl` now accepts only `wss://` (any host) or `ws://` to a loopback host (dev); a public `ws://`, a relative path, or a non-WebSocket scheme (`http:`, `javascript:`, `data:`, …) is rejected and the widget refuses to start with a clear error. Added the first widget `README.md` (attribute table + a `connect-src` CSP example) and unit tests for the validator. `ws://localhost` dev usage is unchanged.
- **Block `javascript:` (and other unsafe-scheme) links in the chat widget** ([services/mddb-chat-widget/src/utils/markdown.ts](services/mddb-chat-widget/src/utils/markdown.ts), [services/mddb-chat-widget/tsconfig.json](services/mddb-chat-widget/tsconfig.json)) — **critical**. The widget's `renderMarkdown` escaped HTML but then built `<a href="$2">` from the message's link URL with **no scheme check**, and the result is injected via `innerHTML` — so an assistant/LLM message (prompt-injectable from knowledge-base documents) containing `[click](javascript:alert(document.cookie))` produced a clickable `javascript:` link executing in the **host page's** origin. Link rendering now goes through an `isSafeUrl` allowlist: only `http(s)`/`mailto` URLs or relative references (`/`, `#`, `./`, `../`) become anchors — `javascript:`, `data:`, `vbscript:`, `blob:`, `file:` (and case/whitespace-obfuscated variants, since browsers ignore control chars in schemes) are dropped, keeping the link text only. New `markdown.test.ts` covers the malicious schemes, the safe ones, and that `rel="noopener noreferrer"` is preserved. Also fixed a latent build break: `tsconfig.json` now excludes `*.test.ts` from the `tsc` build (tests run via `node --test` type-stripping), so the widget `npm run build` passes again.
- **XSS hardening for the SSG markdown viewer** ([services/ssg-template/md-viewer.html](services/ssg-template/md-viewer.html)) — **critical**. `md-viewer.html` rendered `marked.parse()` output straight into `innerHTML` with no sanitization, glued mermaid code-block bodies into HTML unescaped, and fed the `?doc=` query parameter directly to `fetch()` — so inline HTML in any served `.md` ran as script (stored XSS), and a crafted link like `?doc=https://evil/x.md` made the viewer fetch and render attacker-controlled markdown in the docs origin (reflected XSS). Fixes: rendered HTML now passes through **DOMPurify 3.4.10** (pinned + `sha384` SRI; upgraded past the v3.4.0 CVE-2026-41240 fix per the `versions` MCP); mermaid block bodies are `escapeHtml()`-escaped before injection (diagrams still render — `mermaid.run()` reads `textContent`); a `resolveDocParam()` accepts only same-origin relative `*.md` paths and rejects absolute/`//`/`..`/scheme values with a `QUICKSTART.md` fallback; the error panel escapes its message; and a defense-in-depth CSP (`connect-src 'self'`, `object-src 'none'`, `base-uri 'self'`) blocks remote-markdown fetches even if validation is bypassed. A `node --test` suite (`xss.test.mjs`) exercises `escapeHtml`/`resolveDocParam` behaviorally and asserts the DOMPurify/CSP wiring; a new `deploy-docs.yml` step gates the docs deploy on it.
- **Replication gRPC streams now authenticated** ([services/mddbd/replication_server.go](services/mddbd/replication_server.go), [services/mddbd/replication_client.go](services/mddbd/replication_client.go), [services/mddbd/grpc_server.go](services/mddbd/grpc_server.go)) — **critical**. `RequestSnapshot` streamed the **entire BoltDB** (including the `auth_users` bcrypt hashes and `auth_apikeys` buckets) and `StreamBinlog` live-tailed every write, with **no authentication** — the only check was `follower_id != ""`. Because the auth interceptor is unary-only, both streaming RPCs were reachable regardless of `MDDB_AUTH_ENABLED`, so any host with network access to the gRPC port could exfiltrate the whole database or wiretap all writes in one call. A new `authorizeReplication(ctx)` gate now runs at the very top of both handlers (before the binlog-nil check and any DB access) and accepts, in order: a dedicated replication secret (`MDDB_REPLICATION_SECRET` vs the `x-mddb-replication-secret` gRPC metadata, compared with `crypto/subtle.ConstantTimeCompare`), a verified mTLS client certificate, or an admin-authenticated context when main auth is on — refusing with `PermissionDenied` when none is configured rather than exposing the database. The follower attaches the secret via `withReplicationSecret`, and the leader logs a loud startup warning when replication is exposed with no auth/secret/mTLS. `MDDB_REPLICATION_SECRET` documented in [.env.example](.env.example). Tests cover no-config deny, secret mismatch/match, missing metadata, and anonymous `RequestSnapshot`/`StreamBinlog` → `PermissionDenied`.
- **Subresource Integrity for SSG CDN assets** ([services/ssg-template/md-viewer.html](services/ssg-template/md-viewer.html), [services/ssg-template/page.html](services/ssg-template/page.html), [services/ssg-template/index-section-replication.html](services/ssg-template/index-section-replication.html)) — the markdown viewer loaded `marked`, `mermaid` and the github-markdown CSS from public CDNs with no `integrity`/`crossorigin`, and `mermaid` was pinned to a floating major (`@10`) that any 10.x publish could silently replace — a CDN/account/MITM compromise would execute arbitrary JS on the docs site. All three CDN resources now carry verified `sha384` SRI hashes + `crossorigin="anonymous"`; `mermaid` is upgraded from the outdated `@10` to a pinned `@11.15.0` (the viewer already uses the v11 `mermaid.run()` API), and the floating `@11` ESM imports in the other templates are pinned to `@11.15.0` too. A `node:test` asserts every CDN tag has SRI and that no floating mermaid pin remains.
- **WordPress plugin data hygiene (logs + release notes)** ([integrations/wordpress-plugin/includes/class-client.php](integrations/wordpress-plugin/includes/class-client.php), [integrations/wordpress-plugin/includes/class-updater.php](integrations/wordpress-plugin/includes/class-updater.php)) — the client logged the **entire** untrusted server response body to `error_log()` (log spam / CR-LF forging / payload disclosure); it now truncates to a single 200-char snippet. The updater rendered GitHub release notes as **unsanitised HTML** in the wp-admin "View details" modal; they now pass through `wp_kses_post()`. New PHPUnit tests cover both. See the plugin [CHANGELOG](integrations/wordpress-plugin/CHANGELOG.md).
- **Airbyte destination: stop leaking response bodies and tokens to logs** ([integrations/airbyte-destination/destination_mddb/client.py](integrations/airbyte-destination/destination_mddb/client.py)) — `ping()` and `addDocument()` exceptions embedded raw server response fragments (`resp.text[:200]`/`[:300]`), which Airbyte logs and shows in the connection UI — untrusted server content (potentially other tenants' data or secrets) in platform logs. Exceptions now carry only the HTTP status (and, for adds, our own `key`). Additionally a `_RedactingFilter` on the `airbyte` logger scrubs `Bearer <token>` from any log record, so the API key can't leak through a future request dump/refactor. Tests cover the body omission and the token redaction.
- **Airbyte destination: bound document keys and sanitize meta-key names** ([integrations/airbyte-destination/destination_mddb/client.py](integrations/airbyte-destination/destination_mddb/client.py)) — `_extractKey()` did `str(raw)` on the source key field with no length/charset limit (a multi-MB or binary key field created a pathological document key), and `_flattenToStringLists()` copied untrusted upstream field names **verbatim** as MDDB meta keys. Keys over 512 chars or containing control characters now fall back to the existing stable content hash; meta-key names are stripped of control characters and the `|` index-key separator and bounded to 128 chars (empty results are dropped). Unit tests cover oversize/control-char keys and meta-key sanitization.
- **Python client: backup query-param injection + request timeouts** ([services/python-extension/mddb.py](services/python-extension/mddb.py)) — **high**. `backup(filename)` concatenated the name straight into the query string, so `&`, `#`, `?`, spaces and `../` traversal sequences could inject extra query params or manipulate the server-side backup path; it now `urllib.parse.quote(..., safe="")`-encodes it. And `_do()` called `opener.open(req)` with **no timeout**, so a hung server blocked the client forever (the UDS path too); a 30 s default (`DEFAULT_TIMEOUT`) is now passed to every request. New `test_mddb.py` (first tests for this module) covers the encoding and the timeout.
- **PHP client: explicit cURL timeouts and TLS verification** ([services/php-extension/mddb.php](services/php-extension/mddb.php)) — **high**. All three HTTP paths created cURL handles with no `CURLOPT_TIMEOUT`/`CURLOPT_CONNECTTIMEOUT`, so a hung server or network black hole could block the PHP process indefinitely (DoS), and none set `CURLOPT_SSL_VERIFYPEER`/`CURLOPT_SSL_VERIFYHOST` explicitly — relying on defaults that some distros/`php.ini` disable globally. A shared `applyCurlDefaults()` now sets a 5 s connect / 30 s total timeout and forces TLS peer + host verification on every handle. A no-framework smoke test (`mddb.test.php`) asserts the constants, the helper, and that all three paths wire it.
- **GitHub Action no longer logs server response bodies** ([integrations/github-action/src/main.ts](integrations/github-action/src/main.ts)) — **high**. On an HTTP error the action logged `err.body.slice(0, 200)` via `core.warning()`. Actions logs on public repos are world-readable and persistent, so a misbehaving/compromised MDDB server's body (echoed headers, stack traces with secrets, other tenants' data) would leak publicly. It now logs only the action's own message and the HTTP status code. New test asserts a secret body is never logged while the status still is; `dist/` rebundled.
- **Session tokens moved out of `localStorage`** ([services/mddb-panel/src/lib/auth.js](services/mddb-panel/src/lib/auth.js), [services/mddb-chat-widget/src/store/session.ts](services/mddb-chat-widget/src/store/session.ts)) — **high**. The panel stored its JWT and the chat widget stored `sessionId` + full message history in `localStorage`, which is readable by any JavaScript on the origin: a single XSS could exfiltrate the admin token with one call, and the data persisted on disk indefinitely. Both now use `sessionStorage` (per-tab, not persisted, not shared across tabs/extensions): the panel migrates any existing on-disk token into `sessionStorage` on startup then scrubs it from `localStorage`; the widget does the same for its session and caps the stored transcript at `MAX_STORED_MESSAGES = 50`. `node:test` cases cover the message cap. (A future hardening step is an `HttpOnly` cookie issued by the backend.)
- **SSRF protection for outbound HTTP** ([services/mddbd/ssrf_guard.go](services/mddbd/internal/httpclient/ssrf.go), [services/mddbd/http_pool.go](services/mddbd/internal/httpclient/pool.go), [services/mddbd/bulk_ingest_job.go](services/mddbd/bulk_ingest_job.go), [.env.example](.env.example)) — **high**. Four paths dialled user-controlled URLs with no address checks: `import-url` (and returned the response body), webhook delivery, bulk-ingest callbacks, and automation triggers. An attacker could read cloud-metadata (`169.254.169.254` → IAM creds), reach internal admin panels, or port-scan the cluster (blind SSRF). The shared pooled transport now uses a `safeDialContext` that resolves the host, refuses private/loopback/link-local/unspecified targets, and dials the **pre-resolved IP** to defeat DNS rebinding; an `ssrfCheckRedirect` re-applies the policy on every redirect hop (max 5). The bulk-ingest callback was switched from a bare `&http.Client{}` to the guarded pooled client. Internal embedding providers (Ollama/OpenAI/…) keep their own clients and are unaffected. Trusted-intranet opt-outs: `MDDB_OUTBOUND_ALLOW_PRIVATE=true` or `MDDB_OUTBOUND_ALLOWLIST=host1,host2`. The misleading `#nosec … validated above` comment was corrected. Unit tests cover the IP policy, URL validation, redirect checks, and the dialer's block path.
- **Wiki import bz2 decompression-bomb guard** ([services/mddbd/wiki_import.go](services/mddbd/wiki_import.go), [.env.example](.env.example)) — `/v1/import-wiki` fed `bzip2.NewReader` straight into the XML parser with a 1 GB multipart cap but **no limit on decompressed bytes** and `MaxPages=0` (unlimited) by default. bz2 expands 10–50×, so a small upload could drive tens of GB of XML — CPU exhaustion and runaway BoltDB/FTS/disk growth (possible volume-fill outage). A new `cappedReader` now bounds decompressed bytes and stops the parse with a **controlled error** (`413`, with partial counts) instead of silently truncating; the page count is clamped to a server default. Both limits are configurable: `MDDB_WIKI_MAX_DECOMPRESSED_BYTES` (default 4 GiB) and `MDDB_WIKI_MAX_PAGES` (default 500000). Tests cover the cap directly and through the streaming XML decoder.
- **Global request body size limit on JSON endpoints** ([services/mddbd/main.go](services/mddbd/main.go), [.env.example](.env.example)) — `MaxBytesReader` was applied only piecemeal (MCP, `/v1/upload`), while the main JSON handlers (`/v1/add`, `/v1/add-batch`, `/v1/search`, `/v1/update`, …) decoded `r.Body` with no cap — a multi-GB body would be allocated in memory (`ReadTimeout` bounds time, not size), enabling a trivial OOM DoS. A new `withMaxBody` middleware now caps every request: a declared `Content-Length` over the limit is rejected with `413` up front, and the body is otherwise wrapped in `http.MaxBytesReader` so reads can never exceed the limit even without/with a lying `Content-Length`. `/v1/upload` and `/v1/import-wiki` (which stream large files and enforce their own caps) are exempt. Configurable via `MDDB_MAX_BODY_BYTES` (default 32 MiB). `withMaxBody` is 100% covered.
- **Stored XSS via FTS highlight fragments in the admin panel** ([services/mddb-panel/src/components/FTSSearchPanel.jsx](services/mddb-panel/src/components/FTSSearchPanel.jsx), [services/mddb-panel/src/lib/highlight.js](services/mddb-panel/src/lib/highlight.js)) — **critical**. `FTSSearchPanel` rendered server highlight fragments with `dangerouslySetInnerHTML`. The server builds each fragment by wrapping matches in `<mark>` inside the **raw, unescaped** document body, so anyone able to store a document (e.g. `<img src=x onerror=…>`) got arbitrary JS execution in the admin's session when that fragment appeared in search results — chained with the JWT in `localStorage`, full admin-session takeover. The fragment is now rendered as **escaped React text** split on the `<mark>` markers via a new `splitHighlightFragment()` helper: only our own `<mark>` element is emitted, every other byte of document content is shown literally and never parsed as markup. No `dangerouslySetInnerHTML` remains in `mddb-panel/src`. Covered by dependency-free `node:test` cases (incl. `<img onerror>` and `<script>` payloads); a `test` npm script was added to the panel.
- **WordPress plugin validates the release-ZIP download host** ([integrations/wordpress-plugin/includes/class-updater.php](integrations/wordpress-plugin/includes/class-updater.php)) — **high (supply-chain)**. `latestRelease()` passed the GitHub asset `browser_download_url` straight to WordPress's auto-updater as `download_link`/`package`, which downloads and installs the ZIP (arbitrary PHP execution). A manipulated API response, poisoned transient, or weak-TLS MITM could substitute a hostile package → RCE. A new `isTrustedZipUrl()` requires `https` and a host on a strict GitHub-releases allowlist (`github.com`, `objects.githubusercontent.com`, `github-releases.githubusercontent.com`, `release-assets.githubusercontent.com`); anything else yields an empty `zipUrl` so no update is offered. New test asserts a malicious host is rejected.
- **WordPress plugin enforces `https://` for the MDDB endpoint** ([integrations/wordpress-plugin/includes/class-settings.php](integrations/wordpress-plugin/includes/class-settings.php)) — settings accepted any URL passing `wp_http_validate_url()`, including plain `http://`, while the client attaches `Authorization: Bearer <apiKey>` to every request — so the API key and document bodies travelled in cleartext (MITM/eavesdrop). `Settings::sanitize()` now requires `https://` via a new `isAllowedUrl()` (http allowed only for `localhost`/`127.0.0.1`/`::1`) and raises an `add_settings_error()` admin notice on rejection. New PHPUnit tests cover https-accepted, remote-http-rejected, and the local-host exceptions. See the plugin's own [CHANGELOG](integrations/wordpress-plugin/CHANGELOG.md).
- **gRPC streaming RPCs are now authenticated** ([services/mddbd/auth_grpc.go](services/mddbd/auth_grpc.go), [services/mddbd/main.go](services/mddbd/main.go)) — **high**. Only a `GRPCUnaryInterceptor` was wired into the server; the stream chain carried just the rate limiter, so every server-streaming RPC (`MDDB.Export`, the entire `MDDBReplication` service) bypassed authentication. Worse, `Export` calls `CheckPermission(stream.Context(), …)` but no claims were ever injected into a stream context, so with auth enabled it *always* failed `PermissionDenied` — masking the missing interceptor. A new `GRPCStreamInterceptor` (sharing the extracted `authenticateContext` with the unary path — DRY) authenticates the stream and forwards a context carrying the claims via an `authServerStream` wrapper; it is appended to `streamChain` whenever auth is enabled. Anonymous stream RPCs now return `Unauthenticated`; authenticated `Export` passes `CheckPermission`. Covered by unary + stream interceptor tests (both interceptors 100%).
- **MCP listener no longer an unauthenticated bypass of the main auth** ([services/mddbd/mcp_auth_guard.go](services/mddbd/mcp_auth_guard.go), [services/mddbd/main.go](services/mddbd/main.go), [services/mddbd/listen_addr.go](services/mddbd/listen_addr.go), [docs/MCP.md](docs/MCP.md)) — **critical**. The MCP HTTP listener (default `:9000`) grants full read/write access but was protected only by `MCPAPIKeyMiddleware`, which is a no-op unless `MDDB_MCP_API_KEY_ENABLED=true`. So even with `MDDB_AUTH_ENABLED=true`, `/mcp`, `/tools/call`, `/sse`, `/message` and `/resources` accepted anonymous full-R/W traffic — a complete bypass of the main `AuthManager`. The new `decideMCPAuth`/`applyMCPAuth` reconcile MCP exposure with the main auth config: when auth is enabled and MCP has no key auth of its own, the MCP handler is now gated by `AuthManager.HTTPMiddleware` (same `Bearer`/`X-API-Key` credentials; anonymous `tools/call` → `401`); when both auth and MCP keys are off and the bind is non-loopback (the default `:9000`), the server logs a prominent startup security warning. `isLoopbackListenAddr` distinguishes loopback/UDS binds from network-exposed ones. Fully unit- and integration-tested (`decideMCPAuth`, `applyMCPAuth`, `isLoopbackListenAddr` all 100% covered); the `MDDB_AUTH_ENABLED × MDDB_MCP_API_KEY_ENABLED` matrix is documented in MCP.md.
- **`/metrics` no longer public when auth is enabled** ([services/mddbd/auth_middleware.go](services/mddbd/auth_middleware.go), [services/mddbd/auth_manager.go](services/mddbd/auth_manager.go), [docs/TELEMETRY.md](docs/TELEMETRY.md), [.env.example](.env.example)) — `/metrics` was hard-coded into the auth-exempt set (`"/metrics": true, // configurable later`), so with `MDDB_AUTH_ENABLED=true` the Prometheus counters (per-endpoint operation tallies, collection labels, traffic volumes, build version) were readable by anonymous callers — reconnaissance against an otherwise-gated API. The exempt set is now built at `AuthManager` construction by `buildPublicEndpoints()`, which includes `/metrics` **only** when `MDDB_METRICS_PUBLIC=true`. Default with auth on: unauthenticated `GET /metrics` → `401`. Auth off: unchanged (no auth layer runs). `isPublicEndpoint` became an `AuthManager` method that fails closed on a nil set. TELEMETRY.md documents Bearer-token scraping for Prometheus and the full `MDDB_AUTH_ENABLED × MDDB_METRICS_PUBLIC` matrix; `.env.example` gains `MDDB_METRICS_PUBLIC=false`. New tests cover the env-driven set, the gating matrix, and the middleware 401/200 paths.

### Fixed
- **DocumentCache: atomic Stats, shutdown Close, honest eviction comment** ([services/mddbd/cache.go](services/mddbd/internal/cache/cache.go), [services/mddbd/main.go](services/mddbd/main.go)) — completes the cache hardening begun in (which added the stoppable cleanup goroutine via `Close()`/`stopCh`). `Stats()` read `hits`/`misses` non-atomically while `Get` increments them with `atomic.AddUint64` under only a shared `RLock` — a real data race under `-race`; `Stats()` now uses `atomic.LoadUint64`. The server's SIGINT/SIGTERM shutdown now calls `s.Cache.Close()` (alongside the existing `LockFreeCache`/`AdaptiveIndex` closes) so the cleanup goroutine doesn't outlive the process in tests. The eviction comment claiming "simple FIFO" is corrected — Go map iteration is randomized, so it evicts an arbitrary entry (neither FIFO nor LRU). New `-race` test drives concurrent `Get` + `Stats`, plus an idempotent-`Close` test.
- **Chat markdown code blocks now render in the SSG home page** ([services/ssg-template/index.html](services/ssg-template/index.html)) — the inline `md()` renderer's triple-backtick code-fence regex was over-escaped (`\\w` / `\\n` / `[\\s\\S]` — literal backslash sequences in a regex literal, not the `\w`/`\n`/`[\s\S]` character classes), so multi-line ` ``` ` code blocks in chat replies never converted to `<pre><code>` and showed the raw backticks. Corrected to single escaping. (Not a security bug — `md()` escapes `<`/`>`/`&` before transforming — discovered while doing.) A `node --test` extracts `md()` and asserts code blocks render and stay escaped.
- **Validate the GitHub Action `key-prefix` input** ([integrations/github-action/src/inputs.ts](integrations/github-action/src/inputs.ts), [integrations/github-action/action.yml](integrations/github-action/action.yml)) — `key-prefix` was read with no validation and concatenated directly onto every document key, so control characters, spaces, a multi-KB string or traversal-like sequences would pollute the collection's key space and surface only as confusing server-side errors (every other input — `collection`, `concurrency`, `timeout-seconds` — was already validated). A new `assertKeyPrefix` now enforces `/^[A-Za-z0-9._/-]{0,100}$/` and fails the action fast with a clear message before any files are scanned. `inputs.ts` is at 100% coverage; `dist/` rebuilt; the `${{ github.repository }}/` example remains valid.
- **Data race during replication snapshot restore** ([services/mddbd/server_restore.go](services/mddbd/server_restore.go), [services/mddbd/replication_client.go](services/mddbd/replication_client.go), [services/mddbd/cache.go](services/mddbd/internal/cache/cache.go), [services/mddbd/schema.go](services/mddbd/internal/schema/schema.go), [services/mddbd/webhooks.go](services/mddbd/internal/webhooks/webhooks.go)) — **high**. When a follower restored a snapshot it closed and replaced `Server.DB` and swapped the `Cache`/`SchemaManager`/`WebhookManager` pointers with **no synchronization**, while HTTP/gRPC handlers read those fields concurrently — a formal data race, and in-flight requests could run `DB.View` on the just-closed `*bolt.DB` (panic / "database not open"); each restore also leaked the new cache's cleanup goroutine. A new `restoreMu sync.RWMutex` now guards database access: every production BoltDB call goes through `DBView`/`DBUpdate` (read lock; 110 call sites migrated), and the restore runs the whole swap under `withRestoreLock` (write lock) so in-flight reads drain before the old handle closes and the new one is published atomically. The caches/managers are reloaded **in place** (`DocumentCache.Clear()`, `SchemaManager.reload`, `WebhookManager.reload`) so the `Server` pointers never change — removing the field race entirely — and `DocumentCache` gained a `Close()` (stop channel + `sync.Once`) so the cleanup goroutine no longer leaks per restore. Covered by `-race` tests driving concurrent reads/writes against repeated DB swaps plus a goroutine-leak check.
- **`test/` benchmark module now builds and is covered by CI** ([test/](test/), [go.work](go.work), [.github/workflows/test.yml](.github/workflows/test.yml)) — the cross-database benchmark binaries all lived in one package as six files each declaring `package main` with duplicate symbols, so the module didn't compile and was explicitly excluded from `go.work` and CI — letting it rot against server/proto API drift, and hiding ignored-error/leaked-connection bugs inside. Each benchmark is split into its own sub-package (`test/{couchdb,mysql,postgres,mongodb,grpc-perf,grpc-batch}/main.go`, moved with `git mv`), so `go build ./...`/`go vet ./...` are green both in workspace mode and standalone (`GOWORK=off`). `./test` is added to `go.work` and a new `bench` CI job builds + vets it (path filters broadened `test/go.mod` → `test/**`). Along the way: `couchdb` now error-checks every `http.NewRequest`/`json.Marshal` and drains+closes every response body via a `drainClose` helper; `grpc-perf` replaces three ignored-error `ioutil.ReadFile` calls with a `mustReadFile` that aborts instead of silently benchmarking empty documents; deprecated `io/ioutil` is removed from all six files (`os.ReadFile`); the `compare-*.sh` scripts and `test/README.md` are updated to the new `go build -o … ./<dir>` / `go run ./<dir>` layout.
- **Drain outbound response bodies before close so pooled connections are reused** ([services/mddbd/http_pool.go](services/mddbd/internal/httpclient/pool.go), [services/mddbd/webhooks.go](services/mddbd/internal/webhooks/webhooks.go), [services/mddbd/automation_trigger.go](services/mddbd/automation_trigger.go), [services/mddbd/bulk_ingest_job.go](services/mddbd/bulk_ingest_job.go), [services/mddbd/audit_exporter_webhook.go](services/mddbd/internal/audit/exporter_webhook.go)) — webhook delivery, cron/automation triggers, the bulk-ingest callback and the audit webhook exporter all called `resp.Body.Close()` **without** reading the body first. On the shared pooled (keep-alive) transport, a body that isn't drained to EOF stops the underlying TCP/TLS connection from returning to the pool, so each delivery — inside retry loops of up to 4 attempts — paid a fresh connection plus a full TLS handshake (and grew `TIME_WAIT` sockets on both ends). A new shared `drainAndClose` helper reads the remainder (capped at 64 KiB via `io.LimitReader`, nil-safe) then closes; all five pooled-client sites now use it (two more than the audit flagged). A behavioral Go test (`httptest` + `ConnState`/`StateNew` counter) proves 4 successive deliveries reuse a single connection.
- **Schema & required-field validation now applies to every transport** ([services/mddbd/main.go](services/mddbd/main.go), [services/mddbd/batch.go](services/mddbd/batch.go), [services/mddbd/batch_final.go](services/mddbd/batch_final.go)) — **high**. Validation lived in the transport adapters: gRPC `Add` and HTTP `handleAdd` validated, but `DirectClient.Add` (MCP — and via it GraphQL `addDocument`) called the write path with **no checks**, and the batch processors validated only `key`/`lang`, skipping schema validation entirely. So a registered collection schema could be bypassed through MCP, GraphQL or `add-batch`. Required-field (`collection`/`key`/`lang`) + `SchemaManager.Validate` now run inside the single `addDocument` write path (covering MCP/GraphQL and all internal callers — schema validation is opt-in, so a no-op without a registered schema), and both batch processors validate each document's schema. Tests cover the single-path enforcement and per-document batch rejection.
- **`mddb-cli` no longer panics on unexpected server JSON** ([services/mddb-cli/main.go](services/mddb-cli/main.go)) — **high**. The CLI parsed responses into `map[string]interface{}` and used bare type assertions (`doc["addedAt"].(float64)`, `r.(map[string]interface{})`, `t.(string)`), so any missing/`null`/renamed field — or an error object returned with HTTP 200 — crashed the CLI with `panic: interface conversion` and a stack trace instead of a readable message. All 16+ unguarded assertions across the `add`, `stats`, semantic-search, FTS, embedding-stats, schema and key-list paths were replaced with safe accessors (`asFloat`/`asString`/`asMap`/`formatUnix`) that degrade to a zero value. Added `main_test.go` — the **first tests** for the `mddb-cli` module — covering the helpers and asserting degenerate payloads (missing fields, `null`, an error object) don't panic.
- **Panel SPA fallback works on Express 5** ([services/mddb-panel/server.js](services/mddb-panel/server.js)) — the catch-all route was `'{*path}'` (no leading slash), invalid for Express 5 / path-to-regexp v8, so SPA deep links (a refresh on `/documents/123`, or opening a sub-route directly) fell through to a 404 instead of `index.html` (static files masked it for asset requests). Corrected to `'/{*path}'`. A dependency-free `node:test` guards the pattern and ordering (full supertest deep-link test runs in CI).
- **MCP annotations for the 6 `memory_*` tools** ([services/mddbd/mcp_annotations.go](services/mddbd/mcp_annotations.go)) — `mcpToolAnnotations` had 71 entries vs 77 built-in tools, missing all six `memory_*` tools. Because `isToolReadOnly()` defaults unannotated tools to *write*, the read-only `memory_recall`/`memory_list_sessions`/`memory_session_history` were wrongly **blocked in read-only mode**, and `tools/list` shipped no hints for any of the six. Added the entries (`memory_recall`/list/history = read-only; start_session/add_message = write-non-idempotent; summarize = write-idempotent). A new parity test asserts every built-in tool is annotated so this can't regress.
- **`FTSReindex` honours read-only mode and reports failures** ([services/mddbd/grpc_server.go](services/mddbd/grpc_server.go)) — the FTS reindex RPC (a write) lacked the `isReadOnly()` gate every other mutating RPC has, so a read-only replica could clobber its FTS index and race the applier. It also swallowed every error: `_ = DB.View(...)` and `_ = FTSIndex.Index...()`, counting failed docs as `reindexed` and always returning `Status: "ok"` even on partial failure or a mid-restore read error. Now: read-only → `PermissionDenied`; a `DB.View` error propagates as `Internal`; per-doc indexing errors are counted (failed docs don't increment `reindexed`, are folded into `skipped`, and any failure downgrades `Status` to `"partial"`). Tests cover the read-only denial and the happy path.
- **`tools/bench`: handle errors and guard divisions** ([tools/bench/main.go](tools/bench/main.go)) — the benchmark ignored the report file's `Close`/`Execute` errors (a truncated HTML report "succeeded" silently) and divided throughput/SVG coordinates with no zero guard (`+Inf`/`NaN` on a sub-microsecond batch or empty data). Report write now propagates `Execute`/`Close` errors; a `perSecond()` helper and `maxY<=0` guards in the `barHeight`/`barY`/`lineY` template funcs eliminate the divisions by zero. First tests for the module.
- **Removed dead `WorkerPool` with a latent panic** (`services/mddbd/worker_pool.go`, `services/mddbd/worker_pool_test.go`) — `WorkerPool` had no production caller (only its own definition + tests), yet its `Close()` did `cancel()` then `close(p.jobs)`, so a goroutine blocked sending in `Submit` could `panic: send on closed channel`. Per YAGNI the type and its tests were deleted (recoverable via VCS if ever needed) rather than carrying maintained-but-unused code with a race.
- **`exporterCore.Close` is now genuinely thread-safe** ([services/mddbd/audit_exporter.go](services/mddbd/internal/audit/exporter.go)) — `Close` advertised idempotency but used a check-then-act on `stopCh`: two goroutines closing concurrently could both pass the `default:` branch and both run `close(c.stopCh)` → `panic: close of closed channel`. Replaced with `sync.Once`. A new `-race` test calls `Close` from 32 goroutines at once.
- **Panel GraphQL client reads the correct auth token** ([services/mddb-panel/src/lib/graphql.js](services/mddb-panel/src/lib/graphql.js), [services/mddb-panel/src/lib/auth.js](services/mddb-panel/src/lib/auth.js), [services/mddb-panel/src/lib/token.js](services/mddb-panel/src/lib/token.js)) — the GraphQL client read `localStorage['token']` / `['apiKey']`, but `auth.js` stores the JWT under `mddb_auth_token`, so the `Authorization` header was **never set** (GraphQL-backed panel features ran unauthenticated, or risked sending a stale token left under the old key). A new `token.js` module is the single source of truth for the storage key and a JWT-shape validator; `graphql.js` now uses `authManager.getToken()` and only attaches a well-formed JWT; the stale `token`/`apiKey` keys are cleared on startup. Covered by `node:test` cases for the key and shape validation.
- **`AdaptiveIndexManager` optimization worker is now stoppable** ([services/mddbd/adaptive_index.go](services/mddbd/adaptive_index.go), [services/mddbd/main.go](services/mddbd/main.go)) — `NewAdaptiveIndexManager` started a `for range ticker.C` goroutine with no `done` channel and no `Close()`, so it leaked for the process lifetime — and one per `Server` built in tests, blocking `goleak`-style CI checks. It now follows the same `done`/`Close()` pattern as `LockFreeCache`: the worker `select`s on `done`, `Close()` is idempotent (`sync.Once`) and nil-safe, and the server shutdown path calls `AdaptiveIndex.Close()`. Tests verify the worker goroutine exits after `Close()` and that double/nil close is safe.
- **Document read cache is now invalidated on write/delete** ([services/mddbd/main.go](services/mddbd/main.go), [services/mddbd/grpc_server.go](services/mddbd/grpc_server.go), [services/mddbd/replication_applier.go](services/mddbd/replication_applier.go)) — **high**. The `DocumentCache` (5-min TTL) feeds the gRPC `Get` path, but `Server.addDocument` (HTTP/MCP/GraphQL writes) never refreshed it and **no delete path invalidated it**, so a gRPC `Get` could serve a stale or already-deleted document for up to 5 minutes — a transport-dependent consistency bug. `addDocument` now refreshes both caches after commit (keyed by `BuildCacheKey(collection, key, lang)`, replacing the duplicate cache-write the gRPC adapter did), and `deleteDocumentInternal` deletes the entry from both caches. The replication applier's `invalidateDocCache` was also broken — it built `collection|docID` from an over-split key that never matched anything; it now `SplitN`s correctly and derives the exact `BuildCacheKey` from the replicated doc (via `loadDoc`). New integration tests: HTTP delete → gRPC `Get` is `NotFound` immediately; HTTP update → gRPC `Get` returns the new content.
- **`IndexQueue.Enqueue` no longer silently drops jobs** ([services/mddbd/indexqueue.go](services/mddbd/internal/indexqueue/indexqueue.go), [services/mddbd/batchupdate.go](services/mddbd/batchupdate.go), [services/mddbd/main.go](services/mddbd/main.go), [docs/openapi.yaml](docs/openapi.yaml)) — when the 1000-job buffer was full, `Enqueue` hit a `default:` branch that **dropped the job with only a log line**. The batch-update path relied solely on this queue for metadata indexing, so a burst could leave documents permanently missing from meta queries (stale entries never removed either) with a `Status: ok` returned to the client. `Enqueue` now **never drops**: a full queue triggers synchronous in-line indexing (fallback), and it returns an `error` (`ErrQueueClosed` during shutdown) that callers handle. Because the fallback opens its own write transaction, `BatchUpdater.commitUpdate` now collects index jobs during its tx and enqueues them **after commit** (avoiding a BoltDB single-writer deadlock). `Shutdown` no longer closes the channel (removing a send-on-closed-channel panic window). A `fallbacks` counter joins `processed`/`failed` in `Stats()` and is surfaced under `indexQueue` in `GET /v1/stats`. Tests cover the full-queue fallback (job indexed synchronously), the fallback error path, and post-shutdown `ErrQueueClosed`.
- **`parallelScore` panic on small vector collections** ([services/mddbd/vector_parallel.go](services/mddbd/internal/vector/vector_parallel.go)) — when the configured worker count exceeded the number of non-empty chunks, a later worker received a start index past `n` (`start > end`), so `scoreRange`'s `make([]VectorResult, 0, min(end-start, topK*2))` got a negative capacity and panicked with `makeslice: cap out of range`, crashing the process on an ordinary vector search. `scoreRange` now guards `start >= end` (and `topK <= 0`) before allocating, and `parallelScore` skips workers whose range is empty. Regression tests cover the inverted/empty range and the small-N-many-workers path. (Surfaced while verifying; tracked and fixed independently.)
- **Unified document write path across transports** ([services/mddbd/main.go](services/mddbd/main.go), [services/mddbd/grpc_server.go](services/mddbd/grpc_server.go), [services/mddbd/batch.go](services/mddbd/batch.go), [services/mddbd/batch_final.go](services/mddbd/batch_final.go), [services/mddbd/batch_handler.go](services/mddbd/batch_handler.go)) — gRPC `Add`/`AddBatch` re-implemented the BoltDB insert and indexed metadata lazily via the (lossy) `IndexQueue`, **silently skipping FTS, geo, webhooks, SSE, temporal tracking and revision trimming**. A document added over gRPC was therefore invisible to full-text and geo search and fired no live events — behaviour diverged by transport (a DRY/SRP violation and a data-consistency trap). `GRPCServer.Add` is now a thin adapter over the shared `Server.addDocument` single write path (meta index in-tx; revisions + `MaxRevisions` trim; then the shared `runPostWriteHooks` pipeline), keeping the gRPC read cache coherent and still honouring the per-request `SaveRevision` flag (now threaded through `addDocument`). Both batch processors (standard + extreme) run the shared `firePostBatchHooks` after commit — which also gained the previously-missing **geo + temporal** steps, so the HTTP batch path benefits too — and trim revisions to `MaxRevisions`. New parity tests assert a gRPC-written doc (single and batch) is immediately FTS-indexed and meta-indexed, and that `SaveRevision` is respected.

- **Go toolchain drift in `go.work`** ([go.work](go.work), [scripts/check-go-version.sh](scripts/check-go-version.sh), [.github/workflows/test.yml](.github/workflows/test.yml), [Makefile](Makefile), [RELEASING.md](RELEASING.md)) — the 2.9.17 security bump to `go1.26.3` updated every `go.mod`, both Dockerfiles, and all CI `GO_VERSION` envs, but **`go.work` was left pinned to the unpatched `toolchain go1.26.2`**. Local workspace builds (`GOWORK` on) could therefore compile with a toolchain missing the four stdlib CVE fixes that the bump shipped. `go.work` is now consistent with the rest of the repo (all 11 pins identical — see the `go1.26.4` bump below). To stop the drift recurring, a new guard `scripts/check-go-version.sh` collects every `toolchain` directive, `GO_VERSION:` env, and `golang:X.Y.Z` base image and fails on any mismatch; it runs in CI as the `go-version` job, is wired into `make ci` (target `make check-go-version`), and is covered by `scripts/tests/test-go-version.sh` (consistent → exit 0, drifted → exit 1, no pins → exit 2). A new RELEASING.md checklist item documents updating **all** Go-version locations atomically on future bumps.

### Changed
- **CI test-matrix refresh** ([.github/workflows/wordpress-plugin.yml](.github/workflows/wordpress-plugin.yml), [.github/workflows/grafana-datasource.yml](.github/workflows/grafana-datasource.yml)) — the WordPress plugin matrix moves to **PHP 8.2–8.5** (adds the new 8.5 GA, drops near-EOL 8.1; declared minimum bumped to 8.2 across `composer.json`/`phpcs.xml`/plugin header/README so tested == declared — verified locally on PHP 8.5.7). The grafana-datasource matrix drops Node 22 (kept Node 24): its `package-lock.json` is generated by npm 11 (Node 24), which Node 22's npm 10 rejects under `npm ci`; the other integrations stay on Node 22 + 24 where their locks are compatible.
- **Go dependency refresh** ([services/mddbd/go.mod](services/mddbd/go.mod), [test/go.mod](test/go.mod)) — `go get -u ./...` + `go mod tidy` across every module brings 50 direct/indirect dependencies to their latest minor/patch, including security-relevant ones: `golang.org/x/crypto` v0.50.0 → v0.53.0, `golang.org/x/net` → v0.56.0, `google.golang.org/grpc` → v1.81.1, `github.com/quic-go/quic-go` → v0.60.0, `github.com/minio/minio-go/v7` → v7.2.0, `github.com/klauspost/compress` → v1.18.6, plus `gqlgen`/`gqlparser`. The Go toolchain pins are unchanged (still 1.26.4, guard green); the full `mddbd` suite, `mddb-cli`, and `tools/bench` tests pass against the upgraded graph.
- **Correct the built-in MCP tool count (67 → 77) + drift guard** ([README.md](README.md), [docs/MCP.md](docs/MCP.md), [services/mddbd/mcp_tool_count_doc_test.go](services/mddbd/mcp_tool_count_doc_test.go), [Makefile](Makefile)) — the docs hard-coded **67** built-in MCP tools in six places while `mcpBuiltinTools()` actually defines **77** (verified via `len()`), under-selling the server by ten tools and guaranteed to drift again on every new tool. All six phrasings now read 77, and a new `TestMCPToolCountDocsInSync` (runs in the normal `go test ./...`) parses the count out of README.md / docs/MCP.md and fails if it ever diverges from `len(mcpBuiltinTools())` — so the number can't silently rot again. A `make mcp-tools-count` target runs the same check locally.
- **DRY YAML anchors in Compose** ([docker-compose.ring.yml](docker-compose.ring.yml), [docker-compose.yml](docker-compose.yml), [.env.example](.env.example)) — the cluster-ring compose repeated the identical `build:` block 3× (leader + 2 followers) and re-typed the same follower `environment`/`depends_on` keys, and the production compose hard-coded `com.mddb.version=2.9.17` in 4 separate service labels (a release bump had to touch all four or images would carry mismatched versions). The ring file now defines `&mddbd-build`, `&mddbd-env`, `&follower-env` (merged via `<<:`) and `&leader-healthy` anchors; the version label is a single `${MDDB_VERSION:-2.10.0}` interpolation (default added to `.env.example`). The refactor is purely structural — the rendered config is byte-for-byte equivalent (verified by expanding the anchors/merge-keys, since Docker isn't available in this environment).
- **gofmt all Go code + enforce a CI formatting gate** ([services/mddbd/fts_query_expr.go](services/mddbd/internal/fts/fts_query_expr.go), [services/mddbd/fts_query_expr_test.go](services/mddbd/internal/fts/fts_query_expr_test.go), [services/mddbd/tls_config_test.go](services/mddbd/tls_config_test.go), [.github/workflows/test.yml](.github/workflows/test.yml), [Makefile](Makefile)) — `gofmt -l` reported unformatted files, meaning no CI gate enforced the standard toolchain. The three remaining offenders are reformatted with `gofmt -s` (the other four from the audit list were already handled this branch — `tools/bench/main.go` under, the three `test/*.go` under). A new `lint`-job step fails the build (listing offenders) when `gofmt -s -l services/ tools/ test/` is non-empty, and the `Makefile` gains a repo-wide `fmt` target plus a `fmt-check` target (wired into `ci`) that runs the identical command locally.
- **Log-rotation and resource limits in Compose** ([docker-compose.yml](docker-compose.yml), [docker-compose.dev.yml](docker-compose.dev.yml)) — no compose file set a `logging:` cap or `deploy.resources`, violating the project conventions (10 MB log cap + resource limits/reservations). Both the production and dev compose now define reusable YAML anchors (`x-logging`, `x-deploy-server/medium/small`) and apply `logging` (`max-size: 10m`, `max-file: 3`) and `deploy.resources` (limits + reservations) to every service. (The specialized `docker-compose.ring.yml` and `test/docker-compose.benchmark.yml` harnesses can adopt the same anchors as a follow-up.)
- **Removed the GitHub Pages `docs/CNAME` relic** (`docs/CNAME`) — the docs site is deployed to **Cloudflare Pages** (`wrangler pages deploy` in `.github/workflows/deploy-docs.yml`); the `CNAME` file is GitHub Pages' custom-domain mechanism and was a dead relic from the previous host. Removed (Cloudflare custom domains are configured in the dashboard).
- **Least-privilege `permissions:` on all workflows** ([.github/workflows/test.yml](.github/workflows/test.yml), [.github/workflows/release.yml](.github/workflows/release.yml), [.github/workflows/airbyte-destination.yml](.github/workflows/airbyte-destination.yml)) — `test.yml` had **no** `permissions:` block (GITHUB_TOKEN defaulted to broad repo perms), and `release.yml`/`airbyte-destination.yml` set permissions only per-job. Added a restrictive top-level `permissions: contents: read` to the three; jobs that publish (create-release, docker-server/panel, build-and-push) keep their explicit `contents/packages/id-token/attestations: write` elevations. All **8** workflows now declare explicit permissions.
- **Align GitHub Actions versions in core workflows** ([.github/workflows/test.yml](.github/workflows/test.yml), [.github/workflows/release.yml](.github/workflows/release.yml), [.github/workflows/govulncheck.yml](.github/workflows/govulncheck.yml)) — `test.yml` still pinned `actions/checkout@v5` (lint/proto/build jobs) and `codecov/codecov-action@v4` while the rest of the repo was on `@v6`/`v5`; bumped to match. Added `FORCE_JAVASCRIPT_ACTIONS_TO_NODE24: "true"` to the three core Go workflows' env (the integration workflows already set it). (Dependabot — — will keep these current going forward; SHA-pinning third-party actions is left as a separate decision.)
- **Dependabot now monitors every ecosystem** ([.github/dependabot.yml](.github/dependabot.yml)) — **high (supply-chain)**. The config was the untouched GitHub scaffold (`package-ecosystem: ""` — an invalid value, so Dependabot did nothing). It now covers all the monorepo's manifests: **gomod** (mddbd/mddb-cli/bench/test), **npm** (panel/widget/chrome-ext/github-action/grafana), **pip** (airbyte), **composer** (wordpress), **cargo** (chat), **docker** (every service/integration image), and **github-actions** — weekly, with minor/patch grouped per ecosystem to limit PR noise.
- **Panel & chat-widget containers run non-root with healthchecks** ([services/mddb-panel/Dockerfile](services/mddb-panel/Dockerfile), [services/mddb-chat-widget/Dockerfile](services/mddb-chat-widget/Dockerfile), [docker-compose.yml](docker-compose.yml)) — both images ran as **root** (contradicting SECURITY.md's "non-root default") and had **no HEALTHCHECK**, so `depends_on: service_healthy` was impossible and `restart: unless-stopped` couldn't detect a hung process. The panel production stage now runs as the image's unprivileged `node` user; the widget switched to `nginxinc/nginx-unprivileged:alpine` (uid 101, listens on 8080). Both gained a `HEALTHCHECK` probing `/`. The widget's compose port mapping is now `11032:8080`.
- **Refresh SECURITY.md** ([SECURITY.md](SECURITY.md)) — the Supported Versions table only listed `2.x`/`1.x` (omitting the five `0.x` integrations with their own release tags), and Scope listed `mddb-mcp` as a separate component though MCP is built into `mddbd`. The table now covers the integrations, and Scope reflects the current component layout (MCP in `mddbd`; chat/widget, integrations, and client extensions added).
- **Makefile: drop legacy `docker-compose` and the dead MCP log target** ([Makefile](Makefile)) — all 12 targets used the v1 `docker-compose` binary (absent on modern Docker); switched to the `docker compose` v2 subcommand. `dev-logs-mcp` tailed the nonexistent `mddb-mcp` service (MCP is built into `mddbd`) — it now tails `mddbd`.
- **Package metadata for client libraries + insecure-gRPC warning** ([clients/nodejs/package.json](clients/nodejs/package.json), [clients/python/pyproject.toml](clients/python/pyproject.toml), [services/php-extension/composer.json](services/php-extension/composer.json), [services/python-extension/pyproject.toml](services/python-extension/pyproject.toml)) — the four client/extension directories had no package manifest, so nothing carried a license or version. Added `package.json` / `pyproject.toml` / `composer.json` (all **BSD-3-Clause**, version `2.10.0`, with declared runtime deps). Also annotated the Node example's `grpc.credentials.createInsecure()` as **local-development-only** (use `createSsl` in production) so it isn't copied into a production deployment.
- **Fix dead `VITE_MDBB_SERVER` env var** ([.env.example](.env.example)) — `.env.example` defined `VITE_MDBB_SERVER` (typo: MDBB) which nothing reads; the panel's GraphQL client reads `VITE_SERVER_URL` (a full URL). Renamed/corrected.
- **`.dockerignore` now excludes nested directories** ([.dockerignore](.dockerignore)) — unlike `.gitignore`, `.dockerignore` patterns without a `**/` prefix match **only the context root**, so `node_modules/`, `tmp/`, `__pycache__/`, `*.db`, etc. did nothing for nested paths: `services/mddb-panel/node_modules/` and `services/mddb-chat/target/` (potentially gigabytes of cargo output) were copied into the build context — and `COPY services/mddb-panel/ .` after `npm ci` could overwrite the clean install with host `node_modules`. Patterns are now `**/`-prefixed (plus an explicit `**/target/` for cargo) so they match at any depth.
- **Stop tracking build/test artifacts** ([.gitignore](.gitignore)) — three generated files were tracked in git despite ignore intent: `services/mddbd/coverage-graphql.html` (a 661 KB coverage report whose name `coverage.html` didn't match) and two benchmark outputs committed before their ignore rules existed. Removed from the index with `git rm --cached` (kept on disk) and added a `coverage-*.html` pattern so the variant name is ignored too.
- **Coverage badge in README** ([README.md](README.md)) — CI already uploads coverage to Codecov on every run, but the README badge row had no coverage badge despite the documented ≥90% threshold. Added the `codecov.io/gh/tradik/mddb` badge after the Tests badge.

## [2.9.17] - 2026-05-18

### Security
- **Go runtime 1.26.2 → 1.26.3** ([.github/workflows/govulncheck.yml](.github/workflows/govulncheck.yml), [.github/workflows/test.yml](.github/workflows/test.yml), [.github/workflows/release.yml](.github/workflows/release.yml), [services/mddbd/Dockerfile](services/mddbd/Dockerfile), [services/mddbd/Dockerfile.dev](services/mddbd/Dockerfile.dev), [services/mddb-cli/Dockerfile](services/mddb-cli/Dockerfile), [services/mddbd/go.mod](services/mddbd/go.mod), [services/mddb-cli/go.mod](services/mddb-cli/go.mod), [test/go.mod](test/go.mod), [tools/bench/go.mod](tools/bench/go.mod)) — daily `govulncheck` workflow caught four stdlib vulnerabilities reachable from `mddbd`, `mddb-cli`, and `bench`: **GO-2026-4982** and **GO-2026-4980** (XSS via `html/template` escaper bypasses — reachable through `http.Server.Serve` in `mddbd/main.go` and `template.Template.Execute` in `tools/bench/main.go`), **GO-2026-4971** (panic in `net.Dial`/`LookupPort` on NUL byte — reachable through every HTTP/UDP/syslog dialer including `audit_exporter_syslog.go`, `http3.go`, and `main.go`'s listeners), and **GO-2026-4918** (infinite loop in HTTP/2 transport on bad `SETTINGS_MAX_FRAME_SIZE` — reachable through every outbound HTTP client, including `OllamaEmbeddingProvider.Embed`, `fetchURL`, and webhook exporters). All four are fixed in stdlib `go1.26.3`; no third-party module changes required. Toolchain directives in every `go.mod`, both production and dev Dockerfiles, the OpenAPI example response, and the `/v1/stats` example in `docs/API.md` follow the same bump.

### Changed
- **[docs/INTEGRATIONS.md](docs/INTEGRATIONS.md)** — added section **6. Airbyte → MDDB (ELT Destination Connector)** with registration walk-through (Connector display name `MDDB`, Docker repository `tradik/airbyte-destination-mddb`, tag `0.1.1`, docs URL), spec table, record-mapping example, sync-mode semantics, and a Postgres → MDDB usage flow. Top-of-file title, frontmatter, and the architecture mermaid graph now list Airbyte alongside Docling/Langflow/OpenSearch/SSG/wpexporter. The "Full Pipeline" section renumbered to **7**. Root `README.md` blurb, ✅-features list, and Documentation TOC updated to mention Airbyte.

### Added
- **CI/CD for the Airbyte destination** ([.github/workflows/airbyte-destination.yml](.github/workflows/airbyte-destination.yml)) — independent workflow scoped by `paths:` to `integrations/airbyte-destination/**`. On every PR and push touching the integration: `test` job runs `pytest --cov --cov-fail-under=90` on Python 3.12 & 3.13; `smoke-spec` job builds the image with Buildx (gha cache), runs `spec` (asserting `mddbUrl` + `apiKey` are in the published spec), and runs `check` against `https://mddb.tradik.com`. The `build-and-push` job is gated by `if: github.event_name == 'push' && github.ref == 'refs/heads/main'` — only pushes to `main` produce a release. It builds multi-arch (`linux/amd64,linux/arm64`), reads the version from `metadata.yaml`'s `dockerImageTag`, pushes to both Docker Hub (`tradik/airbyte-destination-mddb:<tag>` + `:latest`) and GHCR (`ghcr.io/<owner>/airbyte-destination-mddb:<tag>` + `:latest`), and emits a SLSA build-provenance attestation on the GHCR digest. JS-based actions pinned via `FORCE_JAVASCRIPT_ACTIONS_TO_NODE24: true`; uses the same `docker/login-action@v4`, `docker/build-push-action@v7`, `setup-qemu-action@v4`, `setup-buildx-action@v4`, `attest-build-provenance@v4`, `setup-python@v6`, `checkout@v6`, `upload-artifact@v7` pins as the rest of the repo. Secrets required: `DOCKER_HUB_TOKEN` (existing) + the auto-provided `GITHUB_TOKEN`.
- **Airbyte destination connector** ([integrations/airbyte-destination/](integrations/airbyte-destination/)) — first member of the planned `integrations/` family in this repo. Custom Airbyte destination that ships records to `POST /v1/add`. Each Airbyte stream maps to its own MDDB collection; `key` is read from a configurable field (default `id`) with a SHA-1-of-the-whole-record fallback; `meta` is flattened to `map<string,[]string>` (native MDDB schema); `contentMd` carries the full record inside a fenced JSON code-block — full FTS + vector search indexing with no extra setup. Supported sync modes: `append`, `append_dedup` (both upsert by key — natural for `/v1/add`). Auth via `Authorization: Bearer vk_…` (empty = MDDB without auth, e.g. an internal dev instance). The connector buffers records (`batchSize`, default 100), flushes on every `AirbyteMessage(STATE)` and at end of stream, and retries HTTP 3× with backoff on 429/5xx. Released as the Docker image `tradik/airbyte-destination-mddb:0.1.1` (Python 3.13-slim, non-root user `airbyte:1000`, airbyte-cdk 7.x) — registered in the Airbyte UI as a "custom connector" through `Docker repository name` + `Docker image tag`. 40 unit tests in `unit_tests/` cover record mapping, key fallback, retries, batching, propagation of `keyField`/`language`, the overwrite warning, and skipping unconfigured streams — `pytest --cov` reports ~97%. Icon `mddb.svg` (SVG wrapper around a 68×68 PNG from `versions/web/icons/`). Root Makefile targets: `airbyte-build`/`airbyte-test`/`airbyte-spec`/`airbyte-check`/`airbyte-push`/`airbyte-clean` — they delegate to `integrations/airbyte-destination/Makefile`. Smoke test against `https://mddb.tradik.com` returns `CONNECTION_STATUS = SUCCEEDED`. Docs: [integrations/airbyte-destination/README.md](integrations/airbyte-destination/README.md) with the Airbyte UI registration walk-through and a per-connector [CHANGELOG.md](integrations/airbyte-destination/CHANGELOG.md).

## [2.9.16] - 2026-05-01

### Added
- **Encryption key rotation** ([services/mddbd/encryption.go](services/mddbd/internal/encryption/encryption.go), [services/mddbd/encryption_rotate.go](services/mddbd/encryption_rotate.go), [services/mddbd/encryption_handlers.go](services/mddbd/encryption_handlers.go)) — ISO 27001 A.8.24 / SOC 2 CC6.7. The 2.9.15 at-rest encryption shipped with a single static `MDDB_ENCRYPTION_KEY`. Compliance frameworks expect a key can be retired and replaced without losing access to historical data. New wire format **V2** prefixes the ciphertext with a 1-byte keyID: `MDDB_ENC_V2\x00 (12B) | keyID (1B) | nonce (12B) | ciphertext+tag`. V1 payloads continue to decrypt under the current primary so the upgrade is non-breaking. Configuration: `MDDB_ENCRYPTION_KEY` is the primary (unchanged), `MDDB_ENCRYPTION_KEY_ID` (1..255, default 1) stamps new writes, `MDDB_ENCRYPTION_KEYS_PREVIOUS` is a JSON array of `{id, key}` for read-only rotation history. Admin endpoints: `GET /v1/encryption/status` (per-collection counts of withPrimary/withLegacy/plaintext/unknownKey), `POST /v1/encryption/rotate` (start a re-encryption job, optional `{"collection":"..."}` to scope it), `GET /v1/encryption/jobs[/:id]` (list/single progress). The rotation worker walks docs and rev buckets with short-lived per-entry transactions so writers never block behind a giant tx; plaintext and already-primary entries are skipped, errors are counted, and the job state surfaces the last error. Audit hook fires `encryption.rotation_started` / `_completed` / `_failed` for the existing audit trail. Admin panel grows an **Encryption** sidebar entry rendering keyID, previous keys, per-collection coverage, the job table, and a "Start rotation" control with collection-scope optional input.
- **Audit log export — SIEM webhook + syslog** ([services/mddbd/audit_exporter.go](services/mddbd/internal/audit/exporter.go), [services/mddbd/audit_exporter_webhook.go](services/mddbd/internal/audit/exporter_webhook.go), [services/mddbd/audit_exporter_syslog.go](services/mddbd/internal/audit/exporter_syslog.go)) — ISO 27001 A.8.15 / SOC 2 CC7.2. The audit log lived only in BoltDB and was reachable through `GET /v1/audit`; that fails the "tamper-evident, off-host" expectation auditors apply. AuditManager now holds a list of `AuditExporter` and fans out each batch immediately after the durable BoltDB write — best-effort delivery, sink failures never roll back the audit record. **WebhookExporter** POSTs each event as JSON (decorated with `_mddb_event_type:audit`) to a single URL with arbitrary headers from `MDDB_AUDIT_EXPORT_WEBHOOK_HEADER` so Splunk HEC, Datadog Logs, ELK Bearer auth all work; retry sequence 0s/1s/5s/15s mirrors the existing webhooks subsystem. **SyslogExporter** writes RFC 5424 framed messages over UDP (default) or TCP to host:port; severity flips to "warning" when result=fail, structured-data block (`mddb@32473`) carries actor/action/result/collection so collectors can index without parsing the JSON body. Both sinks can be enabled simultaneously. Configuration: `MDDB_AUDIT_EXPORT_WEBHOOK_URL`, `MDDB_AUDIT_EXPORT_WEBHOOK_HEADER`, `MDDB_AUDIT_EXPORT_WEBHOOK_INSECURE_TLS`, `MDDB_AUDIT_EXPORT_SYSLOG_ADDR`, `MDDB_AUDIT_EXPORT_SYSLOG_FACILITY`, `MDDB_AUDIT_EXPORT_BUFFER`. New `GET /v1/audit/exporters` surfaces per-sink delivered/failed/dropped counters and last error; the SecurityDashboard panel adds an **Audit log export** card pulling that endpoint with masked webhook URLs.
- **Backup path jail** ([services/mddbd/backup_path.go](services/mddbd/backup_path.go)) — backup/restore endpoints across HTTP, gRPC, and DirectClient now run every user-supplied `to`/`from` parameter through `safeBackupPath()`, which enforces a single jail rooted at `MDDB_BACKUP_DIR` (default `./backups`): rejects empty/NUL/absolute paths, follows symlinks, and verifies the resolved target stays inside the jail. Restore additionally requires the target file to exist and be a regular file. With admin credentials this previously allowed reading or overwriting arbitrary files; the change closes that gap.

### Changed
- **CollectionConfig.encrypted now persists via PUT /v1/collection-config** ([services/mddbd/collection_config.go](services/mddbd/collection_config.go)). The 2.9.15 release shipped the `Encrypted` field on `CollectionConfig` and the panel checkbox, but `SetCollectionConfigRequest` was missing the field — so REST/gRPC/MCP clients flipping the flag through the API silently dropped it. Now plumbed through correctly.

## [2.9.15] - 2026-04-29

### Added
- **Incident events via the existing webhook channel** ([services/mddbd/incident_detector.go](services/mddbd/incident_detector.go)) — ISO 27001 A.8.16 / SOC 2 CC7.3–7.4. Five new event names now deliver through `/v1/webhooks`: `security.auth_failure_burst` (sliding-window detector on repeated auth failures per actor+IP), `security.rate_limit_exceeded` (fired by the HTTP/gRPC limiter on rejection), `ops.replication_lag_high` (follower lag monitor), `ops.panic_recovered` (new panic-recovery middleware wrapping the HTTP handler chain — a crash becomes a 500 + event instead of a process kill), `ops.disk_usage_high` (periodic `syscall.Statfs` on the DB path). `WebhookPayload` gains a `detail map[string]interface{}` so incident payloads carry structured data (lag ms, IP, counts, disk pct) without repurposing the document fields. Each detector has its own threshold / interval / cooldown envs (`MDDB_INCIDENT_*`); all are opt-in by threshold configuration, no breaking change. Register with e.g. `{"events":["security.auth_failure_burst","ops.panic_recovered"]}` on `/v1/webhooks` — retries, backoff, `X-MDDB-Event` and `X-MDDB-Webhook-ID` headers are shared with the document-lifecycle delivery path.
- **At-rest encryption (opt-in per collection)** ([services/mddbd/encryption.go](services/mddbd/internal/encryption/encryption.go)) — ISO 27001 A.8.24 / SOC 2 CC6.7. AES-256-GCM on document and revision values. Activation requires both `MDDB_ENCRYPTION_KEY` (32 bytes, base64) and `CollectionConfig.encrypted=true` per collection; either alone is a no-op. Ciphertext format: `MDDB_ENC_V1\x00` magic (12 B) + nonce (12 B) + AES-GCM ciphertext + auth tag. Transparent backward compat at read time — legacy plaintext documents remain readable after a collection is flipped to encrypted. FTS and vector indexes stay plaintext by design (queryable structures cannot be encrypted without breaking search) — document in your threat model. Startup refuses to continue if any collection is flagged as encrypted but the key is missing. Reusable write path: new `marshalAndEncrypt()` helper replaces `marshalDoc()` at every write call site (HTTP add/update, gRPC Add/Update, batch/final-batch processors, memory RAG, MCP direct client); read path remains untouched — `loadDoc()` detects the magic prefix and decrypts transparently.
- **Rate limiting (HTTP + gRPC)** ([services/mddbd/ratelimit.go](services/mddbd/ratelimit.go)) — ISO 27001 A.5.30 / SOC 2 CC6.6. Single sliding-window limiter shared by the HTTP and gRPC transports so both consume one per-client budget. Opt-in via `MDDB_RATE_LIMIT_ENABLED=true`; tunable by `MDDB_RATE_LIMIT_REQUESTS` (default 100), `MDDB_RATE_LIMIT_WINDOW` (default 60s), `MDDB_RATE_LIMIT_BURST` (default 50), `MDDB_RATE_LIMIT_BY` (`ip` default, or `user` with IP fallback for anonymous traffic). HTTP responses add `X-RateLimit-{Limit,Remaining,Reset}`; rejected requests return 429 with `Retry-After` and a JSON body. gRPC unary + stream interceptors return `codes.ResourceExhausted`. `/health`, `/v1/health`, `/metrics` are exempt so monitoring never trips the limiter. The pre-existing `MDDB_MCP_RATE_LIMIT_*` budget for MCP endpoints is untouched — MCP keeps its own dedicated limiter.
- **Production hardening switch** ([services/mddbd/production_guard.go](services/mddbd/production_guard.go)) — ISO 27001 A.5.15/A.8.9 / SOC 2 CC6.1. Setting `MDDB_PRODUCTION=true` requires every compliance guardrail to be satisfied before the server accepts connections: `MDDB_AUTH_ENABLED=true`, JWT secret ≥32 bytes, `MDDB_TLS_ENABLED=true` (or explicit `MDDB_TLS_INSECURE_OK=true` opt-out for dev), `MDDB_CORS_ORIGIN` not `*`, `MDDB_AUDIT_ENABLED=true`, `MDDB_RATE_LIMIT_ENABLED=true`. Missing items abort startup with a per-variable checklist pointing at the failing control. When `MDDB_PRODUCTION` is unset the guard emits a single WARN and continues with the existing defaults — **no breaking change** for current deployments.
- **Admin panel — compliance surface** ([services/mddb-panel/src/components/AuditLogPanel.jsx](services/mddb-panel/src/components/AuditLogPanel.jsx), [WebhooksPanel.jsx](services/mddb-panel/src/components/WebhooksPanel.jsx), [SecurityDashboard.jsx](services/mddb-panel/src/components/SecurityDashboard.jsx), [ComplianceBanner.jsx](services/mddb-panel/src/components/ComplianceBanner.jsx), [CollectionConfigModal.jsx](services/mddb-panel/src/components/CollectionConfigModal.jsx)). The panel now surfaces every 2.9.15 compliance control as a first-class UI, so admins do not have to curl their way through the new endpoints. **AuditLogPanel** renders the `GET /v1/audit` stream with actor / action / result / time-range filters, newest-first pagination, and a visible `dropped` counter that highlights in red when the in-memory buffer is under pressure. **WebhooksPanel** lists every registered webhook with per-row event-name pills and delivery status; it carries a dedicated "Incident events" section that one-click-subscribes to the five new `security.*` / `ops.*` event names introduced by the incident detector. **SecurityDashboard** is a single-pane status view stitching together the compliance state (`GET /v1/compliance-status`), live auth-failure and rate-limit counters pulled from `/metrics`, recent `ops.panic_recovered` incidents from the audit stream, and replication lag — so an operator can confirm at a glance that the fleet is green. **ComplianceBanner** mounts above the existing sidebar and turns amber when `compliant=false`, with a per-variable link into the docs so a misconfiguration on deploy is visible before the first query. The existing **CollectionConfigModal** grows an "Encrypted" checkbox that flips `CollectionConfig.encrypted=true` via `PUT /v1/collection-config`; the field is disabled (with explanatory tooltip) if `MDDB_ENCRYPTION_KEY` is not configured on the server, so the UI never invites a state that the server would reject at startup. All four new panels slot into the existing sidebar under a new **Security** heading, reuse the shared `mddb-client.js` data layer (so they work identically in REST and GraphQL modes), and inherit the existing WCAG 2.2 contrast tokens.
- **`/v1/compliance-status` endpoint** ([services/mddbd/production_guard.go](services/mddbd/production_guard.go)) — unauthenticated HTTP probe that returns the live state of the production-hardening guard as `{production, compliant, missing:[{envVar,want,reason}], missingCount}`. Exposed for operator liveness/readiness probes, the new **ComplianceBanner** in the panel, and external monitors that must detect configuration drift before traffic lands on a non-compliant server. Documented in [docs/API.md](docs/API.md) and [docs/openapi.yaml](docs/openapi.yaml) with the same voice as `/v1/audit`.
- **Audit log** ([services/mddbd/audit.go](services/mddbd/internal/audit/audit.go)) — ISO 27001 A.8.15 / SOC 2 CC7.2 compliance. `AuditManager` buffers structured JSON events `{ts, actor, action, resource, collection, key, result, ip, userAgent, detail}` and flushes them asynchronously to a dedicated `audit` BoltDB bucket; no hot-path handler blocks on disk I/O. Retention is configurable via `MDDB_AUDIT_RETENTION_DAYS` (default 90) with an hourly background trimmer. Authentication attempts (JWT, API key, login, missing/invalid/disabled user) are recorded from [auth_middleware.go](services/mddbd/auth_middleware.go) and [auth_handlers.go](services/mddbd/auth_handlers.go); every write endpoint is audited automatically via the `guardWrite` wrapper in [main.go](services/mddbd/main.go). Admin-only `GET /v1/audit` query endpoint supports `from`/`to` (RFC3339 or raw nanos), `actor`, `action`, `result`, `limit`. Feature is opt-in via `MDDB_AUDIT_ENABLED=true`; when disabled the manager is a no-op and the endpoint returns 404.

### Changed
- **Timing-safe auth error** ([services/mddbd/auth_middleware.go](services/mddbd/auth_middleware.go)) — "user disabled or not found" now returns the same `invalid token` response as a bad JWT to prevent user-existence enumeration.
- **Consolidated MCP arg helpers** — moved `mcpGetBool` from [services/mddbd/mcp_tools_bulk.go](services/mddbd/mcp_tools_bulk.go) into the shared helper group in [services/mddbd/mcp_tools.go](services/mddbd/mcp_tools.go) alongside `mcpGetString`, `mcpGetInt`, `mcpGetFloat`. No behavior change.

## [2.9.14] - 2026-04-19

### Added
- **Inline facets on search** ([services/mddbd/facets.go](services/mddbd/facets.go), [services/mddbd/fts.go](services/mddbd/internal/fts/fts.go), [services/mddbd/hybrid_search.go](services/mddbd/hybrid_search.go)). `POST /v1/fts` and `POST /v1/hybrid-search` accept a new `facetBy` array of metadata keys; the response grows a `facets` map with per-value counts aggregated over the matched documents. Optional `facetMaxValues` caps per-key bucket count. Counts are computed post-filter / post-boost / post-curation so UIs stay in sync with what the user actually sees; missing keys produce an empty bucket list so UIs can render a stable group layout. gRPC `FTSRequest.facet_by`/`facet_max_values` (fields 9/10) and `HybridSearchRequest.facet_by`/`facet_max_values` (fields 17/18) added backwards-compatibly; responses carry a new `map<string, FacetBucketList> facets`. MCP `full_text_search` and `hybrid_search` tools expose the same parameters.
- **Curation rules — pinned & hidden results** ([services/mddbd/curation.go](services/mddbd/curation.go), [services/mddbd/curation_apply.go](services/mddbd/curation_apply.go), [services/mddbd/curation_handlers.go](services/mddbd/curation_handlers.go), [services/mddbd/grpc_curation.go](services/mddbd/grpc_curation.go), [services/mddbd/mcp_tools_curation.go](services/mddbd/mcp_tools_curation.go)). New REST `/v1/curation` (GET/POST/PUT/DELETE), gRPC RPCs `ListCurationRules` / `CreateCurationRule` / `UpdateCurationRule` / `DeleteCurationRule`, and MCP tools with matching names. Each rule targets a collection + trigger query with `matchMode: "exact"` (default) or `"contains"`. Pinned documents are spliced into fixed 1-based positions (pins with `position<=0` append after organic results); hidden documents are dropped by key. Rules are applied inside the FTS + Hybrid pipelines after scoring, boost, and filtering, but before pagination and facet counting — so facets reflect post-curation visible results. Result items carry `"pinned": true` when injected. Persistence lives in a dedicated `curation` BoltDB bucket with a per-collection in-memory cache, rehydrated on startup; binlog-integrated for follower replication.
- **Per-collection revision retention** ([services/mddbd/revision_retention.go](services/mddbd/revision_retention.go), [services/mddbd/collection_config.go](services/mddbd/collection_config.go), [services/mddbd/main.go](services/mddbd/main.go)). `CollectionConfig.maxRevisions` (REST `PUT /v1/collection-config`, gRPC `SetCollectionConfig.max_revisions`, MCP `set_collection_config.max_revisions`, Admin Panel field) enforces a synchronous cap on revision history: every `addDocument` writes a new revision, then `trimRevisions` deletes the oldest entries inside the same BoltDB transaction so total stays at most `N`. `0` (default) preserves the existing unlimited behavior. Trimmed keys are mirrored into the binlog so followers converge on the same pruned state. Applies to `restoreRevision` too.
- **Admin panel** ([services/mddb-panel/src/components/CurationPanel.jsx](services/mddb-panel/src/components/CurationPanel.jsx), [services/mddb-panel/src/components/CollectionConfigModal.jsx](services/mddb-panel/src/components/CollectionConfigModal.jsx), [services/mddb-panel/src/components/FTSSearchPanel.jsx](services/mddb-panel/src/components/FTSSearchPanel.jsx)). New sidebar entry **Curation Rules** lists/creates/edits/deletes rules with pin-and-hide editors. Collection Settings modal gains a **Revision History Retention** field. FTS Search panel gains a comma-separated `Facets:` input and renders per-key value-count chips above the result list; pinned results are badged `PINNED` for visual distinction.

## [2.9.13] - 2026-04-19

### Added
- **Geo distance sort on hybrid search** ([services/mddbd/hybrid_search.go](services/mddbd/hybrid_search.go)). `POST /v1/hybrid-search` accepts a new `sort` field. With `sort: "distance"` and a `geo` filter attached, the post-merge result set is re-ordered by `distanceMeters` ascending so the nearest matching documents surface first; `sort: "combined"` (the default) keeps the existing score-based ordering. Validation rejects `sort: "distance"` without a `geo` filter and unknown sort values. gRPC `HybridSearch` carries the new field but only accepts `combined` — distance sort is HTTP-only because the gRPC request has no geo payload.
- **GeoJSON polygon and multi-polygon containment** ([services/mddbd/geo_polygon.go](services/mddbd/internal/geo/geo_polygon.go), [services/mddbd/geo_handlers.go](services/mddbd/geo_handlers.go)). New endpoint `POST /v1/geo-polygon` accepts a GeoJSON `Polygon` (outer ring + optional holes) or a `MultiPolygon` (union of polygons) and returns every indexed point inside the shape. Implementation does a bounding-box R-tree prefilter then ray-casts each candidate; response time tracks the polygon's bbox rather than the whole collection. Coordinate order is `[lng, lat]` per RFC 7946; rings may be open or closed. Exposed as read-only MCP tool `geo_polygon`.
- **Query string DSL with nested grouping** ([services/mddbd/fts_query_expr.go](services/mddbd/internal/fts/fts_query_expr.go)). New FTS `mode: "expression"` runs through a proper recursive-descent parser that handles parenthesized grouping, operator precedence (NOT > AND > OR), implicit AND between adjacent atoms, and mixed atom types in one query — terms, fuzzy (`term~2`), phrases, proximity (`"phrase"~5`), wildcards, and NOT. Evaluator reuses the existing per-atom scorers (`SearchBM25`, `SearchPhrase`, `SearchProximity`, `SearchWildcard`, `SearchBM25Fuzzy`), so scores stay consistent with single-mode results. Legacy flat `"boolean"` mode is unchanged.
- **Search-result highlighting with context fragments** ([services/mddbd/fts_highlight.go](services/mddbd/internal/fts/fts_highlight.go)). `POST /v1/fts` with `highlight: true` returns a `highlights[]` array per result — snippets taken from the raw `ContentMD` around matched terms, with each match wrapped in a caller-configurable tag (default `<mark>…</mark>`). Adjacent hits cluster into one fragment, clusters rank by hit count, the top `maxHighlights` (default 3) are kept, then re-sorted by document offset so UIs render in reading flow. Boundaries snap to word edges; ellipsis markers flag truncation. Works uniformly across every FTS mode including the new `expression` mode.

### Changed
- **Proto `HybridSearchRequest.sort` field (16)** added — backwards compatible, pre-2.9.13 clients simply omit it. Regenerated for Go / Python / Node.js / PHP via `buf`.
- **SEARCH.md** gains a dedicated "Expression Search" subsection and a "Highlighting with Fragments" subsection; API.md and openapi.yaml surface the new parameters.
- **Version bump** — `VERSION = "2.9.13"` across `services/mddbd/main.go`, Makefile, docker-compose labels, mddb-panel package.json, CLI manpage, snapcraft, SSG landing page.

## [2.9.12] - 2026-04-18

### Added
- **Per-query boost / demote for FTS and hybrid search** ([services/mddbd/fts_boost.go](services/mddbd/fts_boost.go)). Clients can now supply a `boost` map keyed by `"metaKey:metaValue"` on both `/v1/fts` and `/v1/hybrid-search` (and their gRPC equivalents) to multiply the score of documents that carry the matching metadata pair — positive values boost (`5.0` → 5×), negative values demote (`-2.0` → ½×). Boosts combine multiplicatively when multiple entries match the same document, and the combined factor is floored at `0.001` so a stack of demotions cannot collapse the score. No reindex is required. The panel's FTS and Hybrid search views grow a collapsible "Boost / Demote" section that mirrors the existing field-weights UI, and the MCP `full_text_search` / `hybrid_search` tools accept the new parameter verbatim.
- **Async bulk ingest with job tracking** ([services/mddbd/bulk_ingest_job.go](services/mddbd/bulk_ingest_job.go), [services/mddbd/bulk_ingest_handlers.go](services/mddbd/bulk_ingest_handlers.go)). New endpoints for long-running ingest workloads where the HTTP response should not block:
  - `POST /v1/bulk-ingest-job` — queue a job; returns HTTP 202 with a job ID
  - `GET /v1/bulk-ingest-job/{id}` — poll status (counters, errors, timestamps)
  - `DELETE /v1/bulk-ingest-job/{id}` — cancel a pending job
  - `GET /v1/bulk-ingest-jobs?collection=X` — list jobs newest-first

  Jobs are drained FIFO by a single worker in 500-document chunks; payloads live in an in-memory queue while status records are persisted to the new `bulk_jobs` BoltDB bucket. A startup recovery pass flips any orphan `pending`/`processing` job from a crashed run to `failed` so observers never see stale non-terminal status. Optional `callbackUrl` receives a `POST` with the final job record on completion. MCP tools `bulk_ingest_submit` / `_status` / `_list` / `_cancel` expose the same surface.
- **Prefix autocomplete** ([services/mddbd/fts_autocomplete.go](services/mddbd/internal/fts/fts_autocomplete.go)). New `GET /v1/autocomplete?collection=X&q=mar[&field=title&topN=10]` returns top-N terms starting with the given prefix, ranked by document frequency. The implementation scans the existing FTS inverted index (`fts` bucket for global, `ftsf` for field-scoped) so no additional indexing is required; scan is bounded at 10000 entries to keep pathological prefixes fast. The panel's FTS search input gains a debounced (150ms) dropdown of suggestions with doc-count badges, and the MCP `autocomplete` tool mirrors the HTTP API.

### Changed
- **Proto `FTSRequest` / `HybridSearchRequest` each gain a `map<string, double> boost`** field — field 8 on FTSRequest, field 15 on HybridSearchRequest. All language clients (Go, Python, Node.js, PHP) regenerated via `buf generate`.
- **OpenAPI** ([docs/openapi.yaml](docs/openapi.yaml)) — added `boost` to `FTSSearchRequest` and `HybridSearchRequest`; added new `/v1/bulk-ingest-job` / `/v1/bulk-ingest-job/{id}` / `/v1/bulk-ingest-jobs` / `/v1/autocomplete` paths plus `BulkIngestSubmitRequest` and `BulkIngestJob` schemas.
- **Version bump** — [services/mddbd/main.go](services/mddbd/main.go) `VERSION = "2.9.12"`, Makefile, docker-compose.yml labels, mddb-panel package.json.

### Fixed
- **26 broken documentation links** producing 404s on mddb.tradik.com. Root causes:
  - `docs/GUIDES.md` — absolute links missing `/docs/` prefix (e.g. `/uses-website-chat/` → `/docs/uses-website-chat/`)
  - `docs/EMBEDDING_PROVIDERS.md` — links to non-existent files (`VECTOR_SEARCH.md`, `API_ENDPOINTS.md`, `ADMIN_PANEL.md`, `MCP_CONFIG.md`, `SEARCH.md`) replaced with correct slugs
  - `docs/ARCHITECTURE.md` — `WEBHOOKS.md` link (file doesn't exist) replaced with reference to `AUTOMATIONS.md`; `../CHANGELOG.md` → GitHub URL
  - `docs/COMPARISON.md` — `PERFORMANCE.md` → `BENCHMARK.md`
  - `docs/BULK-IMPORT.md` — `CLI.md` (doesn't exist) replaced with `INSTALLATION.md`
  - `docs/FEATURES.md` — `TEMPORAL_TRACK.md` → `TEMPORAL-TRACK.md` (underscore/dash mismatch)
  - `docs/INSTALLATION.md` — `../services/mddb-mcp/WSL_SETUP.md` → `/docs/mcp/`
  - `docs/PANEL.md` — `../docs/` and `../services/mddbd/README.md` → valid site URLs
  - `docs/README.md` — `../BENCHMARK.md` → `BENCHMARK.md`; `openapi.yaml` / `swagger.html` → `/docs/api/swagger.html`
  - `docs/GRPC.md` — `../proto/mddb.proto` → GitHub blob URL
  - `docs/ROADMAP.md` — `../CONTRIBUTING.md`, `../CHANGELOG.md` → GitHub blob URLs
  - `docs/LLM_CONNECTIONS.md` — `openapi.yaml` → `/docs/api/swagger.html`
  - `docs/examples/sample-with-frontmatter.md` — `../docs/` double-nesting fixed
  - All `../LICENSE` links across docs → GitHub blob URL
- **Footer branding** — added "Made by tradik" link and JSON-LD Organization schema to [services/ssg-template/base.html](services/ssg-template/base.html)

## [2.9.11] - 2026-04-11

### Added
- **Unix Domain Socket (UDS) transport** for HTTP and gRPC servers. `MDDB_HTTP_ADDR` and `MDDB_GRPC_ADDR` (and the equivalent CLI flags / config fields) now accept `unix:/absolute/path.sock` in addition to classic `host:port`. The server creates the socket with `0600` permissions (owner-only), removes any stale socket file left by a prior run, and cleans up the socket on graceful shutdown. TLS is automatically skipped on UDS listeners — filesystem permissions already authenticate the peer.
  - New helper [services/mddbd/listen_addr.go](services/mddbd/listen_addr.go) with `parseListenAddr`, `openListener`, `closeListener`, `isUnixAddr` — used by both the HTTP listener in [services/mddbd/main.go](services/mddbd/main.go) and the gRPC listener in [services/mddbd/grpc_server.go](services/mddbd/grpc_server.go).
  - Unit tests in [services/mddbd/listen_addr_test.go](services/mddbd/listen_addr_test.go) cover TCP / UDS / stale-socket / cleanup paths.
  - **Python client** ([services/python-extension/mddb.py](services/python-extension/mddb.py)) gains `unix:` scheme support via a zero-dependency `_UnixHTTPConnection` / `_UnixHTTPHandler` backed by `socket.AF_UNIX` — stdlib-only, no new deps.
  - **PHP client** ([services/php-extension/mddb.php](services/php-extension/mddb.php)) gains `unix:` scheme support via libcurl's `CURLOPT_UNIX_SOCKET_PATH`.
  - **Rationale**: replaces a previously considered FFI / `libmddb.so` path. UDS delivers the same "zero-network, embedded-ish" UX for PHP/Python/Node sidecars at ~5 % of the cost — no C ABI to maintain, no cgo in the host process, no Go runtime leaking into PHP-FPM workers.

- **Mutual TLS (mTLS) / client certificate authentication** for the HTTP(S) listener. New config fields `tls.clientCAFile` and `tls.clientAuth` (env: `MDDB_TLS_CLIENT_CA`, `MDDB_TLS_CLIENT_AUTH`). When `clientCAFile` points to a PEM bundle of trusted CAs, MDDB will verify client certificates chaining to those CAs. `clientAuth` may be `require` (default, reject unauthenticated clients) or `request` (verify only if client presents a cert).
  - New helper [services/mddbd/tls_config.go](services/mddbd/tls_config.go) (`buildServerTLSConfig`) builds the full `crypto/tls.Config` once at startup, so the HTTP server now uses `ServeTLS(lis, "", "")` with a pre-built config. `MinVersion` pinned to TLS 1.2.
  - mTLS is ignored on UDS listeners (a UDS socket already authenticates the peer by filesystem permissions).
  - [services/mddbd/server_config.go](services/mddbd/server_config.go) adds `ClientCAFile` / `ClientAuth` to `TLSConfig` with YAML and env bindings.

### Fixed
- **Landing page — Mermaid diagram rendering** ([services/ssg-template/index.html](services/ssg-template/index.html)). The Replication section's Mermaid `graph LR` was rendered into a `<pre>` with `mermaid@10` and `startOnLoad: true`, which produced `Syntax error in text` in mermaid 10.9.5. Switched to `<div class="mermaid">` with inline content, upgraded to `mermaid@11` (matching [page.html](services/ssg-template/page.html)), and explicitly call `mermaid.run()` after init.
- **Landing page — stale MCP tool count**. Hero badge, feature card, sr-only feature list, JSON-LD `featureList`, meta description and the "Quick Start" terminal comment all claimed "72 MCP Tools". Audited the real count against the dispatch switch in [services/mddbd/mcp_tools.go](services/mddbd/mcp_tools.go) (67 top-level `case` branches in `mcpCallTool`) vs the declarations in [services/mddbd/mcp_custom_tools.go](services/mddbd/mcp_custom_tools.go)'s `mcpBuiltinTools()` (66). Found one orphan — `aggregate` — that was dispatchable but not declared, so it was invisible to MCP clients (`tools/list` never returned it, so clients could not call it). Added the missing declaration, bringing the authoritative count to **67**. Updated landing page and README accordingly.
- **Landing page — missing geosearch mention**. The landing page made zero reference to geospatial search despite it shipping in 2.9.10. Added a dedicated feature-strip chip, an sr-only bullet, and a JSON-LD `featureList` entry.
- **`/docs/geosearch/` 404** ([docs/GEOSEARCH.md](docs/GEOSEARCH.md)). The file was missing SSG frontmatter (`title`, `slug: "docs/geosearch"`, `description`, `status`), so the static site generator never emitted the corresponding output directory — the sidebar link in [services/ssg-template/base.html](services/ssg-template/base.html#L78) and any inbound link from a prior PR both 404'd. Added frontmatter.
- **Landing page — stale JSON-LD metadata**. `softwareVersion` bumped to `2.9.11`, `datePublished` bumped to `2026-04-11`, `featureList` expanded with geosearch, UDS and mTLS.

### GraphQL — full resurrection (was a stub since v2.7.0)
- **All 11 queries and 21 mutations declared in [services/mddbd/graphql/schema.graphql](services/mddbd/graphql/schema.graphql) are now implemented.** Prior to 2.9.11 the resolvers in `services/mddbd/graphql/schema.resolvers.go` were `panic("not implemented")` stubs for everything except `login` and a partial `deleteDocument`, and `SimpleGraphQLAdapter` returned `"not yet implemented - use REST API"` for every data operation. Both files are fully replaced.
- **New adapter** [services/mddbd/graphql_adapter.go](services/mddbd/graphql_adapter.go) (`GraphQLAdapter`) delegates every operation to the in-process MCP `DirectClient` ([services/mddbd/mcp_direct_client.go](services/mddbd/mcp_direct_client.go)) for documents / search / FTS / vector / schema / webhooks, and to `AuthManager` for users / groups / permissions. Same code path as REST and gRPC — no behavioural drift between protocols.
- **Expanded `gql.ServerInterface`** ([services/mddbd/graphql/resolver.go](services/mddbd/graphql/resolver.go)) from 10 methods (mostly returning `interface{}`) to 38 methods that return concrete `gql.*` types directly. Resolvers in [schema.resolvers.go](services/mddbd/graphql/schema.resolvers.go) are now thin one-liners that just delegate.
- **`@auth` and `@hasRole` directives are intentional no-op pass-throughs** ([services/mddbd/graphql/directives.go](services/mddbd/graphql/directives.go)) — all auth and per-collection permission checks happen inside the adapter so the contract lives in one place and the directive context-key gotcha is sidestepped. Per-method enforcement: `requireAuthenticated` for every operation, `requireAdmin` for user / group / permission / webhook / schema management, `CheckPermission` for read/write on a specific collection. When `AuthManager` is `nil` (auth disabled), the adapter short-circuits to allow-all to mirror a fresh-out-of-the-box deployment.
- **Default-on**: `MDDB_GRAPHQL_ENABLED` flips from `"false"` to `"true"` in [services/mddbd/main.go](services/mddbd/main.go) and [services/mddbd/endpoints_handlers.go](services/mddbd/endpoints_handlers.go). Set `MDDB_GRAPHQL_ENABLED=false` to opt out. The `/graphql` endpoint and the Playground at `/playground` are now part of the standard surface.
- **End-to-end tests** in [services/mddbd/graphql_e2e_test.go](services/mddbd/graphql_e2e_test.go) instantiate a real `Server` against a temp BoltDB and exercise `AddDocument` → `GetDocument`, `SearchDocuments` pagination, `DeleteDocument`, `GetStats` and a panic-guard smoke test that calls every read-only resolver to make sure none of them regress to the old "not implemented" behaviour.
- **Obsolete tests removed**: `services/mddbd/graphql_adapter_test.go` (asserted the old `"not yet implemented - use REST API"` stub returns) is gone. `services/mddbd/graphql/resolver_test.go` is rewritten with a `stubServer` satisfying the new interface and the original Login / DeleteDocument / MapMetaInputToInternal cases retained.
- **Panel transparency**: [services/mddb-panel/src/components/SettingsPanel.jsx](services/mddb-panel/src/components/SettingsPanel.jsx) — the REST/GraphQL toggle's tooltip is rewritten to be honest about the current state: the *server* GraphQL endpoint is fully functional, but the *panel* UI itself still issues every request through the REST client. Wiring `mddb-client.js` to dispatch through the existing [services/mddb-panel/src/lib/graphql.js](services/mddb-panel/src/lib/graphql.js) client is a panel-side refactor scheduled for a follow-up release. Use the GraphQL endpoint directly from your own client (Apollo, urql, curl) until then.
- **`docs/GRAPHQL.md`** updated with the new status, smoke-test recipes against the live endpoint, and accurate error message reference.

### Added (gRPC TLS)
- **TLS / mTLS on the gRPC listener** ([services/mddbd/main.go](services/mddbd/main.go)). The gRPC server now reuses the same `buildServerTLSConfig` (from [services/mddbd/tls_config.go](services/mddbd/tls_config.go)) as the HTTP listener and attaches the resulting `tls.Config` via `grpc.Creds(credentials.NewTLS(...))`. A single `tls.*` config block enables HTTPS *and* TLS-secured gRPC simultaneously, and `MDDB_TLS_CLIENT_CA` enables mTLS on both. Skipped on UDS listeners. New startup log line: `mddb gRPC listening on :11024 (mode=wr, db=mddb.db, tls=on, mtls=on (clientAuth=require))`. Closes the only out-of-scope item from the original 2.9.11 PR description.
- **`docs/TLS.md` extended** with a "TLS on the gRPC listener" section: Go (`google.golang.org/grpc`), Python (`grpcio`), Node (`@grpc/grpc-js`) and `grpcurl` client snippets for both HTTPS-only and full mTLS modes.

### Tests — coverage push for the 2.9.11 surface
- **`services/mddbd/tls_config_test.go`** (new) — 10 cases generating fresh ECDSA self-signed certs in `t.TempDir()` and exercising `buildServerTLSConfig` across every config permutation: disabled, missing fields, bad cert path, plain HTTPS, mTLS default-require, mTLS explicit "require", mTLS "request", bad clientAuth value, missing client CA file, empty client CA bundle. **Coverage: 0% → 100%.**
- **`services/mddbd/graphql_e2e_test.go` extended** with 8 new E2E cases against a real BoltDB plus a real `AuthManager` bootstrap helper (`gqlAdapterWithAuth` synthesizes admin claims into the request context like the HTTP middleware does). New tests cover `UpdateDocument`, `AddBatch`, `SetTTL` + `ImportURL`, `FullTextSearch`, full Schema CRUD (`SetSchema` → `GetSchema` → `ListSchemas` → `ValidateDocument` → `DeleteSchema`), full Webhook CRUD (`RegisterWebhook` → `ListWebhooks` → `DeleteWebhook`), `VectorReindex`, `DeleteCollection`, full Auth flow (`Authenticate` → `GenerateJWT` → `Me` → `Register` → `ListUsers` → `CreateAPIKey` → `SetPermission` → `UserPermissionsList`), and full Group flow (`CreateGroup` → `ListGroups` → `SetGroupPermission` → `GroupPermissionsList` → `UpdateGroup` → `DeleteGroup`). The previous panic-guard smoke test that called every read-only resolver is preserved.
- **Coverage of new 2.9.11 files**: `tls_config.go` 100%, `listen_addr.go` 91% average (parseListenAddr 100%, isUnixAddr 100%, openListener 61% — error paths only, closeListener 71%), `graphql_adapter.go` mostly 60-100% per method (a handful of paths remain at 0%: `IngestDocuments` complex permutations, `VectorSearch` requires an embedding provider, `GetClaimsFromContext` only fires inside the HTTP middleware path, and `derefFloat64` is currently unused). Whole-package coverage moved from 63.9% to 65.4%.

### Documentation — TLS / mTLS / UDS
- **New [docs/TLS.md](docs/TLS.md)** — dedicated TLS + mTLS guide covering quick-start (HTTPS-only and mTLS-required modes), the full env-var reference, `openssl` recipes for generating a demo CA + server cert + client cert, deployment patterns (proxy-fronted, direct HTTPS, service-to-service mTLS, staged rollout), and troubleshooting for the most common handshake failures.
- **[docs/config.md](docs/config.md)** — TLS table extended with `MDDB_TLS_CLIENT_CA` and `MDDB_TLS_CLIENT_AUTH`, plus a brand-new `Unix Domain Socket transport` section explaining the `unix:/path.sock` form for `MDDB_HTTP_ADDR`/`MDDB_GRPC_ADDR` with curl, Python, PHP, and gRPC client examples.
- **Sidebar** in [services/ssg-template/base.html](services/ssg-template/base.html) gains a "TLS & mTLS" link under Security & Ops.
- **README** Security section now links to `docs/TLS.md` from the TLS bullet.

## [2.9.10] - 2026-04-11

### Added
- **Geosearch** ([docs/GEOSEARCH.md](docs/GEOSEARCH.md)) — Point-in-radius and bounding-box queries pulled forward from the v2.11 roadmap. Documents attach coordinates via reserved metadata keys (`geo_lat`/`geo_lng`, `geo_hash`, or `geo_postcode`+`geo_country` with an opt-in CSV lookup), which MDDB indexes into both an in-memory R-tree (default, best overall) and a geohash prefix index (alternative, selectable per-query). Shared `geo` bucket in BoltDB, Binlog-replicated, async startup rebuild identical to the vector index lifecycle.
  - New HTTP endpoints: `POST /v1/geo-search`, `POST /v1/geo-within`, `POST /v1/geo-reindex`, `GET /v1/geo-stats`, `POST /v1/geo-encode`, `POST /v1/geo-decode`.
  - gRPC parity: `GeoSearch`, `GeoWithin`, `GeoReindex`, `GeoStats` RPCs with new proto messages; existing `Document` message untouched.
  - MCP tool surface: `geo_search`, `geo_within`, `geo_stats`, `geo_encode`, `geo_decode` (all read-only).
  - **`/v1/hybrid-search` extended** with an optional `geo: {lat, lng, radiusMeters}` field that spatially pre-filters the FTS+vector candidate pool and attaches `distanceMeters` to each result item.
  - **Panel UI**: new "Geo Search" tab with a Leaflet + OpenStreetMap map, click-to-set query center, radius slider, algorithm switch (R-tree / geohash), metadata filter composition, and result pins that open documents in the shared viewer. Adds `leaflet` as a panel dep.
  - **New files**: [services/mddbd/geo_index.go](services/mddbd/internal/geo/geo_index.go), [geo_store.go](services/mddbd/internal/geo/geo_store.go), [geo_postcodes.go](services/mddbd/internal/geo/geo_postcodes.go), [geo_hash.go](services/mddbd/internal/geo/geo_hash.go), [geohash_index.go](services/mddbd/internal/geo/geohash_index.go), [geo_handlers.go](services/mddbd/geo_handlers.go), [geo_grpc.go](services/mddbd/geo_grpc.go), [mcp_direct_client_geo.go](services/mddbd/mcp_direct_client_geo.go), [mcp_tools_geo.go](services/mddbd/mcp_tools_geo.go), + tests for each.
  - **Dependency**: `github.com/tidwall/rtree v1.10.0` (pure Go, no cgo).
  - Reserved metadata keys: `geo_lat`, `geo_lng`, `geo_hash`, `geo_postcode`, `geo_country`.
  - Out of scope (deferred to a follow-up): anti-meridian crossing bboxes, 3D/altitude, automatic postcode dataset downloads, GeoJSON ingest.
  - **GraphQL not wired up**: geo queries are *not* exposed via `/graphql` in this release. The GraphQL subsystem in this project is a pre-existing stub — every query resolver panics `not implemented` and `SimpleGraphQLAdapter` returns `"not yet implemented - use REST API"` for every method. This is independent of geosearch and will be addressed in a dedicated follow-up PR `graphql: wire up query resolvers` that implements the adapter for all queries, including geo. Until then, use REST (`/v1/geo-*`), gRPC, or MCP.

## [2.9.9] - 2026-04-11

### Security
- **Upgraded `google.golang.org/grpc` to v1.80.0** in `services/mddbd` — fixes GO-2026-4762 (authorization bypass via missing leading `/` in `:path` pseudo-header). Reached by `startGRPCServer` at [grpc_server.go:99](services/mddbd/grpc_server.go#L99).
- **Pinned Go toolchain to 1.26.2** across all modules (`services/mddbd`, `services/mddb-cli`, `tools/bench`, `test`) and `go.work`, plus matching bumps in Dockerfiles (`golang:1.26.2-alpine`) and GitHub Actions workflows (`test.yml`, `release.yml`, `govulncheck.yml`). Fixes 5 stdlib vulnerabilities flagged by `govulncheck`:
  - GO-2026-4947 — `crypto/x509` unexpected work during chain building
  - GO-2026-4946 — `crypto/x509` inefficient policy validation
  - GO-2026-4870 — `crypto/tls` unauthenticated TLS 1.3 KeyUpdate DoS
  - GO-2026-4866 — `crypto/x509` case-sensitive `excludedSubtrees` auth bypass
  - GO-2026-4865 — `html/template` JsBraceDepth XSS
- **Added `govulncheck` GitHub Actions workflow** (`.github/workflows/govulncheck.yml`) — scans all three Go modules on push/PR and nightly (06:00 UTC) with `GOWORK=off` to mirror the isolation of the Tests workflow.

### Fixed
- **Wiki table row separator rendering** ([services/mddbd/wikitext.go](services/mddbd/internal/wikitext/wikitext.go)) — `|-` row separators in wiki tables were previously unreachable: the more-generic `|` data-row branch ran first and swallowed them as empty `| |` rows. Reordered so `|-` is checked first. Surfaced by `staticcheck SA4017` during the Go 1.26.2 upgrade.
- **`TestCharsetReader_UTF8`** ([services/mddbd/wiki_import_test.go](services/mddbd/wiki_import_test.go)) — replaced an empty `if r != nil { /* comment */ }` branch with a real `t.Errorf` so UTF-8 pass-through regressions actually fail the test (`staticcheck SA9003`).
- **`swapParallelConfig`/`MinSize`/`Workers` test helpers** ([services/mddbd/internal/vector/vector_parallel_test.go](services/mddbd/internal/vector/vector_parallel_test.go)) — take `int32` directly (matching the underlying `atomic.Int32`) instead of `int`+conversion, eliminating `gosec G115` overflow warnings.

### Chore
- **License consistency sweep — BSD-3-Clause everywhere** — the canonical `LICENSE` file at the repo root declares BSD-3-Clause, but documentation, packaging metadata, and even distributed artifacts had drifted to claim MIT in several places. Audited and fixed all of them in one pass:
  - **Distributed artifacts (critical — these ship to end users)**:
    - [.github/workflows/release.yml](.github/workflows/release.yml) — RPM spec for both `mddbd` and `mddb-cli` changed from `License: MIT` to `License: BSD-3-Clause`. Affects every `.rpm` package built from a release tag.
    - [scripts/mddb_model.py](scripts/mddb_model.py) — Open WebUI module frontmatter changed from `license: MIT` to `license: BSD-3-Clause`. This file is published to the Open WebUI Community registry and imported as a RAG model by end users.
    - [services/mddb-cli/mddb-cli.1](services/mddb-cli/mddb-cli.1) — manpage copyright line changed from `Copyright (c) 2024 MDDB Project. License MIT.` to `Copyright (c) 2025-2026 Tradik Limited. License BSD-3-Clause.`. Installed by `.deb`, `.rpm`, and Homebrew packages into `/usr/share/man/`.
  - **Documentation (medium — user-visible docs)**:
    - [docs/DOCKER_HUB.md](docs/DOCKER_HUB.md) — badge URL, License section, and "MIT licensed, community driven" tagline — this file is pushed as the Docker Hub repository README.
    - [docs/DOCKER.md](docs/DOCKER.md), [docs/GRPC.md](docs/GRPC.md), [docs/PANEL.md](docs/PANEL.md), [proto/README.md](proto/README.md) — License footers.
    - [services/mddb-panel/README.md](services/mddb-panel/README.md), [services/mddb-cli/README.md](services/mddb-cli/README.md) — License sections.
  - **Package metadata (low — missing fields added)**:
    - [services/mddb-panel/package.json](services/mddb-panel/package.json), [services/mddb-chat-widget/package.json](services/mddb-chat-widget/package.json) — added `"license": "BSD-3-Clause"` (were missing the field entirely).
    - [services/mddb-chat/Cargo.toml](services/mddb-chat/Cargo.toml) — added `license = "BSD-3-Clause"` and `repository` URL (both were missing, `cargo publish` would have failed).
  - **Template cleanup**:
    - [services/mddb-panel/src/lib/markdown-templates.js](services/mddb-panel/src/lib/markdown-templates.js) — the "blog" and "readme" markdown templates offered to panel users now default to BSD-3-Clause instead of MIT, for consistency (these are placeholders users edit, but nudging the default matters).
- **Untracked committed build binaries** — removed `services/mddbd/mddb` (34 MB), `services/mddb-cli/mddb-cli`, and `tools/bench/mddb-bench` from the repo and expanded `.gitignore` so `go build` artifacts cannot slip into history again.
- **`buf breaking` CI guard** ([.github/workflows/test.yml](.github/workflows/test.yml)) — the breaking-change check now skips (with a GitHub Actions warning) when the base branch has no `buf.yaml`. Only applies to the one-shot buf-migration PR, where main's pre-migration layout with its legacy `services/mddbd/proto/mddb.proto` duplicate would otherwise break image building on the target side.

### Added
- **Wikipedia XML Dump Import** (`/v1/import-wiki`) — Stream and import MediaWiki XML dumps (including `.xml.bz2` compressed) directly into MDDB.
  - Streaming XML parser — processes multi-GB dumps without loading into memory
  - Automatic wikitext-to-Markdown conversion (headings, bold/italic, links, lists, tables, templates, references, categories)
  - Namespace filtering (default: ns=0, articles only), redirect skipping, max page limit
  - Batch processing with configurable batch size (default 500) and progress logging every 10K pages
  - Supports multipart file upload or raw octet-stream with query params
  - Metadata extraction: `wiki_id`, `wiki_title`, `wiki_ns`, `wiki_rev_id`, `wiki_timestamp`, `wiki_contributor`
  - `skipFts` option for faster bulk imports (run `/v1/fts-reindex` after)
  - New files: `wiki_import.go`, `wikitext.go`, `wikitext_test.go`, `wiki_import_test.go`
- **Database Path Configuration** — Database file location now configurable via CLI flag, YAML config, or environment variable.
  - CLI: `--db /path/to/mddb.db`, `--mode wr`
  - YAML config: `database.path`, `database.mode`
  - Env var: `MDDB_PATH`, `MDDB_MODE` (unchanged, still supported)
  - Precedence: CLI flags > env vars > config file > defaults

### Removed
- **`services/mddbd/proto/mddb.proto`** — stale duplicate of the source-of-truth `proto/mddb.proto` at the repo root. Nothing actually referenced it: Go imports the generated `mddb/proto` package (the `.pb.go` files, which remain), Docker builds already read the root `proto/mddb.proto` via `COPY proto /proto`, and `buf generate` reads from the root too. The duplicate had drifted 2KB behind source.
- **`services/mddbd/generate.sh`** — dead duplicate of `proto/generate.sh` using hardcoded relative paths to the stale copy above. Three competing code generators was two too many.

### Changed
- **Protobuf code generation now uses `buf`** — Replaces the `protoc`-based `proto/generate.sh` script as the primary code generator.
  - New files: [buf.yaml](buf.yaml) (lint rules + module config), [buf.gen.yaml](buf.gen.yaml) (pinned plugin versions)
  - Pinned plugins for reproducibility: `protocolbuffers/go:v1.36.11`, `grpc/go:v1.6.1`, `protocolbuffers/python:v31.1`, `grpc/python:v1.71.0`, `protocolbuffers/js:v3.21.4`, `grpc/node:v1.13.0`, `protocolbuffers/php:v31.1`, `grpc/php:v1.72.0`
  - Legacy `protoc`-based script preserved as `proto/generate-legacy.sh`; the main `proto/generate.sh` now wraps `buf generate` and falls back to the legacy script if `buf` is not installed.
  - `proto/generate.sh` also syncs `proto/mddb.proto` → `clients/nodejs/proto/mddb.proto` after generation — required because `@grpc/proto-loader` loads the file at runtime.
  - CI now runs `buf lint`, `buf breaking` (on PRs, against base branch), and `git diff --exit-code` after `buf generate` + nodejs sync to catch drift between `.proto` source and committed generated code.
  - Fixes the long-standing quirk documented in the repo memory where `generate.sh` placed files in the wrong directory due to `-I proto` stripping the path prefix.
  - Fixes [docs/GRPC.md](docs/GRPC.md) broken link that pointed at the now-deleted `services/mddbd/proto/mddb.proto`.
- **`services/mddbd` Docker images no longer regenerate proto** — [services/mddbd/Dockerfile](services/mddbd/Dockerfile) and [Dockerfile.dev](services/mddbd/Dockerfile.dev) used to install `protoc-gen-go@latest` + `protoc-gen-go-grpc@latest` at image build time and regenerate `.pb.go` files from scratch, **silently overriding the pinned plugin versions** from buf.gen.yaml and producing Docker images with different proto bindings than local builds.
  - Fix: Dockerfile now just copies the already-committed `services/mddbd/proto/*.pb.go` files (CI enforces these match `buf generate` output via `git diff --exit-code`).
  - Removes `protobuf` + `protobuf-dev` from Alpine apk packages and `go install protoc-gen-go*` steps — smaller image, faster build, reproducible across environments.
  - `services/mddb-chat` Dockerfile is unchanged — Rust's `tonic-build` legitimately needs `protoc` at cargo build time (no committed Rust bindings equivalent to `.pb.go`), and the `tonic-build` crate version is pinned in `Cargo.lock`.
- **`go mod tidy`** across all Go modules (`services/mddbd`, `services/mddb-cli`, `tools/bench`, `test`) — dependency lists now match actual imports after recent feature additions.
- **Go workspace for the monorepo** — [`go.work`](go.work) committed at the repo root, listing `services/mddbd`, `services/mddb-cli`, and `tools/bench`.
  - Enables cross-module refactoring, unified `go build ./...` from the repo root, and gopls "goto definition" across module boundaries.
  - `test/` module intentionally excluded (pre-existing `package main` conflicts in benchmark scripts); its `replace mddb => ../services/mddbd` in `test/go.mod` remains as a standalone fallback.
  - **CI runs with `GOWORK=off`** in both [test.yml](.github/workflows/test.yml) and [release.yml](.github/workflows/release.yml) — each module builds and tests in strict isolation so missing `require` entries (that workspace mode would hide) fail fast.
  - `.gitignore` updated: `go.work` is now committed; `go.work.sum` is ignored (not needed when every module maintains its own `go.sum`).
  - Docker builds are unaffected — `COPY services/mddbd/` never picks up the repo-root `go.work`, so containers naturally run in isolated mode.
  - See README.md "Development with Go Workspace" section for details.

### Fixed
- **Vector search RLock regression from 2.9.8** — `VectorIndex.Search` and `SearchWithFilter` held the read lock for the entire multi-millisecond parallel scoring phase, serializing every writer (`Add`/`Remove`) against searches and partially defeating the 2.5x parallel speedup under write-heavy workloads.
  - Fix: release `RLock` immediately after `snapshotMap()` copies slice headers; parallel scoring now runs lock-free on the owned snapshot.
  - Impact: concurrent `/v1/add` / auto-embedding worker throughput restored during search traffic.
- **`parallelSearchConfig` data race** — Global worker/minSize config was plain `int` fields, mutated directly by tests and read concurrently by searches — fragile if any test ever called `t.Parallel()`.
  - Fix: both fields now `atomic.Int32` with `Workers()` / `MinSize()` accessors; tests use `swapParallelConfig`/`swapParallelWorkers`/`swapParallelMinSize` helpers that atomically swap and return a restore closure.
  - Verified with `go test -race -count=10 ./services/mddbd/` — all parallel tests clean.
- **`batchCosineSim` panic on empty input** — On ARM64 the CGo wrapper indexed `&query[0]` / `&matrix[0]` / `&out[0]` without guarding against empty slices, crashing the server instead of no-oping. Added length guards.
- **`vector_parallel.go` memory model clarity** — Added comment above `partials[workerID]` write explaining why disjoint index writes are race-free per the Go memory model and `wg.Wait()` happens-before — prevents future "fix" attempts that would add unnecessary mutex.

## [2.9.8] - 2026-04-06

### Added
- **Goroutine Parallel Vector Search** — Multi-threaded scoring for Flat and IVF search paths.
  - Flat search: map snapshot + fan-out scoring across goroutines on disjoint index ranges
  - IVF search: parallel cluster probing with per-cluster goroutines
  - Auto worker count: `runtime.NumCPU()` (capped at 16), configurable via `MDDB_VECTOR_PARALLEL_WORKERS`
  - Minimum collection size threshold (default 2048) to avoid goroutine overhead on small collections, configurable via `MDDB_VECTOR_PARALLEL_MIN_SIZE`
  - Zero contention during scoring — each worker writes to its own result slice
  - Deterministic ordering with docID tiebreaker for stable results
  - **~2.5x speedup** on 50K×768 (24ms → 9.7ms), **~2.8x** on 50K×1536 (38ms → 13.5ms)
  - New file: `vector_parallel.go`, tests: `vector_parallel_test.go`
- **OPQ (Optimized Product Quantization)** — New vector index algorithm extending PQ with learned orthogonal rotation matrix.
  - Decorrelates dimensions before subspace splitting for better quantization quality (~1-3% recall improvement over standard PQ)
  - Alternating optimization: jointly learns rotation matrix + PQ codebooks (5 iterations default)
  - Rotation via Procrustes alignment with Gram-Schmidt re-orthogonalization
  - ADC search on rotated query, re-ranking with exact similarity on original vectors
  - API: `"algorithm": "opq"` in vector search requests
  - New file: `vector_opq.go`
- **Configuration documentation update** — Added 15+ missing environment variables to `docs/config.md`:
  - `MDDB_VECTOR_PARALLEL_WORKERS`, `MDDB_VECTOR_PARALLEL_MIN_SIZE` (parallel search)
  - `MDDB_TEMPORAL`, `MDDB_SPELL` (feature toggles)
  - MCP API key authentication, rate limiting, and logging settings

## [2.9.7] - 2026-04-06

### Added
- **ARM NEON/SME Vector Math Acceleration** — Hardware-accelerated similarity functions for vector search using ARM SIMD instructions.
  - **3-tier dispatch**: SME (Apple M4+, Cortex-X925+) → NEON (all ARM64) → scalar Go (x86/other)
  - Accelerated functions: `cosineSimilarity`, `dotProductSimilarity`, `euclideanSimilarity`, `euclideanDistSq`
  - **Batch cosine similarity**: Single CGo call for entire collection search (zero per-vector overhead)
  - NEON: 4-way float32 FMA via `float32x4_t` + `vfmaq_f32` intrinsics
  - SME: Scalable Vector Extension in streaming mode (`__arm_locally_streaming`) for wider SIMD on M4+
  - Runtime hardware detection: macOS (`sysctlbyname`), Linux (`getauxval`)
  - Zero allocations, zero external dependencies (~200 lines of C vendored in-tree)
  - Build tag `nosme` to force pure Go scalar on ARM64
  - Cross-platform: on amd64 compiles to identical pure Go code (no CGo required)
  - New benchmarks: `BenchmarkCosineSim{768,1024,1536,3072}`, `BenchmarkBatchCosineSim_{1K,10K,50K}_{768,1536}`
  - New files: `vector_math_scalar.go`, `vector_math_arm64.go`, `vector_math_arm64_neon.c`, `vector_math_arm64_sme.c`, `vector_math_test.go`, `vector_math_bench_test.go`

## [2.9.6] - 2026-04-05

### Added
- **Temporal Tracking** — Document lifecycle event tracking with analytics API.
  - Records `create`, `update`, and `access` events per document
  - **3 new HTTP endpoints**: `POST /v1/temporal/query` (event history for a doc), `POST /v1/temporal/hot` (top-N most accessed docs), `POST /v1/temporal/histogram` (activity histogram by day/week/month)
  - Per-collection opt-in: `trackAccess` (record GET events), `trackHot` (hot-docs leaderboard) via Collection Settings
  - **Panel**: new "Temporal Analytics" panel with activity histogram and hot-docs leaderboard
  - Async writes via buffered channel + `db.Batch()` — zero overhead on read/write path
  - Configurable via `MDDB_TEMPORAL=false` to disable globally
- **Spell Correction** — SymSpell-style spell checker for FTS queries and document content.
  - Uses Levenshtein distance with frequency-weighted ranking (no new dependencies)
  - **3 new HTTP endpoints**: `POST /v1/spell-suggest` (token suggestions with confidence), `POST /v1/spell-cleanup` (apply corrections), `GET/PUT/DELETE /v1/spell-dictionary` (custom per-collection dictionaries)
  - FTS integration: enable `spellCorrect: true` on a collection to auto-correct queries; `FTSSearchResponse` now includes `spellCorrected` field
  - **Panel**: new "Spell Checker" panel with interactive test UI and custom dictionary management
  - `SpellSuggestionBadge` shown in FTS results when query was auto-corrected
  - Configurable via `MDDB_SPELL=false` to disable globally; async dictionary loading with HTTP 503 guard
- **Memory RAG** — Conversational memory system for RAG applications. Store, retrieve, and semantically search conversation history across sessions.
  - **6 new HTTP endpoints**: `POST /v1/memory/session` (create session), `POST /v1/memory/message` (add message), `POST /v1/memory/recall` (semantic/hybrid/keyword recall), `POST /v1/memory/summarize` (session summary), `POST /v1/memory/sessions` (list sessions), `POST /v1/memory/history` (message history)
  - **6 new MCP tools**: `memory_start_session`, `memory_add_message`, `memory_recall`, `memory_summarize`, `memory_list_sessions`, `memory_session_history`
  - **3 dedicated collections**: `memory_sessions`, `memory_messages`, `memory_summaries`
  - **Hybrid recall**: Combines vector search (semantic) + FTS (keyword) with Reciprocal Rank Fusion (RRF) for optimal context retrieval
  - **Auto-embedding**: Messages are automatically embedded for semantic search when an embedding provider is configured
  - **Session TTL**: Default 30-day auto-expiry, configurable per session
  - **User/session/role filtering**: Filter recall by userId, sessionId, or message role
  - **Session summarization**: Generate and store conversation summaries with embeddings
- **20 new tests** for Memory RAG handlers and helpers

## [2.9.4] - 2026-03-26

### Added
- **MCP API Key Authentication** ([#29](https://github.com/tradik/mddb/issues/29)) — Protect MCP endpoints with API keys. Enable with `MDDB_MCP_API_KEY_ENABLED=true`, define keys in `MDDB_MCP_API_KEYS=key1:name1,key2:name2`. Supports `X-API-Key` header, `Authorization: Bearer`, and `?api_key=` query param (for SSE).
- **MCP Request Logging / Audit Trail** ([#30](https://github.com/tradik/mddb/issues/30)) — Structured JSON audit logs for all MCP requests. Enable with `MDDB_MCP_LOGGING_ENABLED=true`. Logs method, path, status, duration, client IP, API key name, session ID, and user agent.
- **MCP Rate Limiting** ([#31](https://github.com/tradik/mddb/issues/31)) — Per-client rate limiting for MCP endpoints. Enable with `MDDB_MCP_RATE_LIMIT_ENABLED=true`. Configurable via `MDDB_MCP_RATE_LIMIT_REQUESTS` (default: 100), `MDDB_MCP_RATE_LIMIT_WINDOW` (default: 60s), `MDDB_MCP_RATE_LIMIT_BURST` (default: 20), `MDDB_MCP_RATE_LIMIT_BY` (ip/api_key/session). Returns `X-RateLimit-*` headers and `Retry-After` on 429.
- **Dynamic MCP API Key Management** ([#33](https://github.com/tradik/mddb/issues/33)) — REST API for creating, listing, disabling, and deleting MCP API keys stored in BoltDB. Keys persist across restarts. Supports TTL expiry and disable-without-delete. Endpoints: `POST/GET/DELETE /v1/mcp/keys`, `POST /v1/mcp/keys/disable`. Requires admin auth. Cache with configurable TTL (`MDDB_MCP_API_KEY_CACHE_TTL`).
- **Panel: MCP API Keys tab** — New "API Keys" tab in LLM Connections for creating, viewing, and managing MCP API keys from the web panel.
- **Panel: Metadata filter scroll fix** ([#14](https://github.com/tradik/mddb/issues/14)) — Filter panel now uses `max-h-[70vh]` instead of `max-h-60` for proper scrolling with many metadata fields.

## [2.9.3] - 2026-03-25

### Added
- **MCP Protocol 2025-11-25** — Upgraded from 2024-11-05 to the latest MCP specification
- **Streamable HTTP transport** (`/mcp`) — New standard transport supporting POST (JSON-RPC), GET (SSE stream), DELETE (session termination), `MCP-Session-Id` header. Legacy SSE transport (`/sse` + `/message`) preserved for backward compatibility
- **Tool annotations** — All 52+ tools annotated with `readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint` hints per MCP spec. Enables AI clients (Claude, Cursor) to auto-approve safe tools
- **Structured output schemas** — `outputSchema` on 9 key tools: `get_stats`, `search_documents`, `semantic_search`, `full_text_search`, `hybrid_search`, `vector_stats`, `get_checksum`, `classify_document`, `aggregate`
- **5 MCP Prompts** — Built-in prompt templates: `analyze-collection`, `search-help`, `summarize-collection`, `import-guide`, `rag-pipeline`
- **Completion/autocomplete** — `completion/complete` for collection names and prompt arguments (source, model, algorithm)
- **MCP Logging** — `logging/setLevel` method + `notifications/message` support with RFC 5424 severity levels (debug through emergency)
- **Notification handling** — Server accepts `notifications/initialized`, `notifications/cancelled`, `notifications/roots/list_changed` without error
- **Progress token infrastructure** — `notifications/progress` support for long-running tools (vector_reindex, ingest_documents, fts_reindex, create_backup, etc.)
- **Cursor-based pagination** — `tools/list` and `resources/list` support cursor parameter per spec
- **Per-protocol access modes** — Independent read/write control per protocol via `MDDB_API_MODE`, `MDDB_GRPC_MODE`, `MDDB_MCP_MODE`, `MDDB_HTTP3_MODE`. Each overrides the global `MDDB_MODE` for its protocol. Example: `MDDB_MCP_MODE=read` makes MCP read-only while API remains read-write.
- **`MDDB_MCP_BUILTIN_TOOLS=false`** — Disable all 54 built-in MCP tools, exposing only custom YAML tools. Useful for restricting AI agents to domain-specific tools only.
- **39 new tests** — MCP protocol (29), per-protocol mode enforcement (10) including follower/global mode scenarios

### Security
- **MCP now respects global read-only mode** — `MDDB_MODE=read` and follower replication role correctly block MCP write tools. Previously MCP DirectClient bypassed the mode check entirely.
- **globalMode propagation** — Server's access mode (including follower-forced read-only) flows through the full MCP chain: Server → MCPHandler → MCPToolServer → write guard

### Fixed
- **Custom MCP tools work in read-only/follower mode** ([#27](https://github.com/tradik/mddb/issues/27)) — Custom tools with read-only actions (`full_text_search`, `search_documents`, `semantic_search`, `fts_languages`) are now allowed in read-only mode. Previously all custom tools were incorrectly blocked because they had no entries in the annotation map.

### Changed
- Tool call errors now return `isError: true` in result content (per spec) instead of JSON-RPC error codes
- `ping` response is now an empty object `{}` per MCP spec (was `{"result":"pong"}`)
- Resource read errors use code `-32002` (resource not found) instead of generic `-32000`
- Capabilities now advertise `prompts`, `logging`, `completions`, and `listChanged: true` for tools and resources

## [2.9.2] - 2026-03-25

### Added
- **MCP-over-SSE transport** — Spec-compliant MCP transport over Server-Sent Events at `/sse` + `/message` on MCP port (9000). Enables remote MCP connections without stdio for web-based agents and remote servers.
- **Panel SSE integration** — Real-time toast notifications for document changes, auto-refresh document list, Live/Offline connection indicator with auto-reconnect and exponential backoff
- **7 new MCP-over-SSE tests** — endpoint, full message flow, session validation, error handling

### Changed
- Version bump to 2.9.2 across all files

## [2.9.1] - 2026-03-25

### Fixed
- **SSE auth enforcement** — when auth is enabled, SSE now requires JWT/API key (returns 401 without token). Previously SSE was open to unauthenticated users.
- **SSE RBAC filtering** — events are only sent to clients with `PermRead` on the collection. `readOnly` field in each event indicates if client has `PermWrite`.

### Added
- **SSE per-IP rate limiting** — max concurrent SSE connections per IP address (default: 5). Prevents resource exhaustion. Configurable via `MDDB_SSE_MAX_PER_IP`.
- **SSE on MCP port** — `/events` endpoint available on MCP HTTP server (port 9000)
- **SSE connected event** includes `mode` ("read" or "readwrite") and `user` fields

## [2.9.0] - 2026-03-24

### Added
- **Per-Collection Vector Quantization** — Each collection can now configure its own vector quantization level for both storage and in-memory search. Supported formats:
  - `float32` (default) — Full precision, no compression
  - `int8` — 4x compression with ~1% recall drop, recommended for most use cases
  - `int4` — 8x compression with ~2-3% recall drop, ideal for large collections
- **Quantized Vector Index** (`vector_index_quantized.go`) — In-memory flat index that stores and searches vectors directly in int8/int4 format without dequantization
- **Quantized storage format (v2)** — New binary serialization with version byte prefix, backward-compatible with existing float32 records
- **Auto-select quantized searcher** — Vector search automatically uses the quantized index when the collection has quantization configured
- **`quantization` field in Collection Config API** — `PUT /v1/collection-config` now accepts `quantization` field (`"float32"`, `"int8"`, `"int4"`)
- **Quantization info in vector stats** — `GET /v1/vector-stats` now shows `quantization` per collection
- **Panel: Vector Quantization selector** — Collection Settings modal now includes Vector Quantization dropdown
- **New documentation** — `docs/QUANTIZATION.md` with full guide, examples, storage savings table, and technical details
- **17 new tests** — Round-trip quantization, similarity accuracy, storage integration, compression ratio verification

- **Server-Sent Events (SSE)** — Real-time document change notifications via `GET /v1/events`. Broadcasts `doc.added`, `doc.updated`, `doc.deleted` events. Per-collection filtering via `?collection=X`. Default enabled, configurable via `MDDB_SSE_ENABLED=false`. Keep-alive heartbeat every 30s.
- **pprof profiling endpoints** — Runtime CPU/memory profiling at `/debug/pprof/` (heap, goroutine, CPU profile, trace, allocs, block, mutex). Disabled by default, enable via `MDDB_PPROF_ENABLED=true`.
- **HTTP connection pooling** — Shared `http.Transport` with keep-alive for all outbound requests (webhooks, triggers, crons, import-url). Configurable via `MDDB_HTTP_POOL_MAX_IDLE`, `MDDB_HTTP_POOL_MAX_PER_HOST`, `MDDB_HTTP_POOL_IDLE_TIMEOUT`.
- **Built-in TLS/HTTPS** — Native TLS support without reverse proxy. Configure via `MDDB_TLS_ENABLED=true`, `MDDB_TLS_CERT`, `MDDB_TLS_KEY` or YAML config. Works for HTTP API server.

### Fixed
- **quantization.go**: Add bounds validation in `dequantizeInt8`/`dequantizeInt4` — prevents panic on corrupted or truncated quantized vector data
- **vector_store.go**: Return error instead of nil vector when dequantization fails due to data length mismatch
- **lockfree_cache.go**: Fix incorrect `size` tracking when updating an existing cache key — eviction was triggered unnecessarily and size counter drifted
- **lockfree_cache.go**: Fix goroutine leak — `cleanup()` goroutine now stops via `Close()` method; called on server shutdown
- **lockfree_cache.go**: Fix misleading "FIFO eviction" comment — Go map iteration is random, not ordered
- **mvcc.go**: Fix race condition in `Delete` — replaced `Load`+`Store` with `LoadOrStore` to prevent concurrent writes from being silently lost
- **mvcc.go**: Fix memory leak in GC — uncommitted versions from abandoned transactions were never cleaned up; GC now skips only versions belonging to active transactions
- **mvcc.go**: Optimize `Commit`/`Rollback` from O(all keys) to O(affected keys) via `txnKeys` tracking map
- **vector_store.go**: Harden `CountByCollection` chunk suffix stripping with `n >= 0` guard to reduce false deduplication risk

### Changed
- **Use Cases section linked to guides** — `docs/index.html` Use Cases section now links to step-by-step guides (`uses/website-chat.md`, `uses/wordpress-analyzer.md`, `uses/youtube-transcribe.md`); added "Use Cases" nav link
- Updated docs: README, CHANGELOG, openapi.yaml, docs/index.html, man page
- `vector_store.go` — `PutQuantized`, `PutChunksQuantized`, `LoadCollectionQuantized` methods, auto-detect v1/v2 format on read
- `vector_handlers.go` — Quantization-aware reindex, auto-algorithm selection, stats enrichment

## [2.8.0] - 2026-03-15

### Added
- **Per-Collection Storage Backends** — Each collection can now use its own storage backend instead of the server-wide default. Supported backends:
  - `boltdb` (default) — Embedded BoltDB, same as before
  - `memory` — In-memory ephemeral storage, data lost on restart. Ideal for scratch/cache collections
  - `s3` — S3-compatible object storage (AWS S3, MinIO, Cloudflare R2, etc.) with configurable endpoint, bucket, region, credentials, and prefix
- **Storage backend configuration via API** — `PUT /v1/collection-config` now accepts `storageBackend` and `storageConfig` fields
- **Storage backend configuration via Panel** — Collection Settings modal now includes Storage Backend selector with S3 configuration form
- **StorageBackend interface** (`storage_backend.go`) — Pluggable storage abstraction with `BackendRegistry` for per-collection routing
- **MemoryBackend** (`storage_memory.go`) — Thread-safe in-memory document store
- **S3Backend** (`storage_s3.go`) — S3-compatible storage using `minio-go/v7`, with auto bucket creation
- **Aggregations** — New `POST /v1/aggregate` endpoint for metadata facets and date histograms
  - Facets: count distinct values per metadata key with `count` or `value` ordering
  - Histograms: group documents by `addedAt`/`updatedAt` with `day`/`week`/`month`/`year` intervals
  - Optional `filterMeta` pre-filtering (same as `/v1/search`)
  - MCP tool: `aggregate`
- **Advanced Full-Text Search Modes** — The `/v1/fts` endpoint now supports 8 search types:
  - Boolean search: `AND`, `OR`, `NOT` operators and `+`/`-` prefix notation
  - Phrase search: exact consecutive phrase matching via positional index
  - Wildcard search: `*` (any chars) and `?` (single char) pattern matching
  - Proximity search: terms within N words of each other (`"rust performance"~5`)
  - Range search: numeric/date range filtering on metadata and timestamps (`rangeMeta`)
  - Auto-detect mode: automatically selects search type from query syntax
  - New `mode` parameter: `"auto"`, `"simple"`, `"boolean"`, `"phrase"`, `"wildcard"`, `"proximity"`
  - Positional index for phrase/proximity search (new `ftsp` bucket)
  - Panel: search mode selector, proximity distance slider, syntax hints
- **Multi-Language Full-Text Search** — Language-aware stemming and stop word filtering for 18 languages (English, Polish, German, French, Spanish, Italian, Portuguese, Dutch, Russian, Swedish, Norwegian, Danish, Finnish, Hungarian, Romanian, Turkish, Arabic, Tamil). Each document's `lang` field determines the FTS pipeline. New endpoints: `GET /v1/fts-languages`, `POST /v1/fts-reindex`. New config: `MDDB_FTS_DEFAULT_LANG`. New `lang` parameter in FTS search requests.
- **Chat Service Improvements** — Anthropic Claude LLM provider, tool-use support (search, get document), improved widget UI with typing indicators and error handling
- **File Upload Enhancements** — Extended `POST /v1/upload` with support for PDF, DOCX, HTML, ODT, RTF, TeX, YAML, and plain text file formats alongside Markdown

### Changed
- Updated docs: README, openapi.yaml, docs/index.html, API.md, SEARCH.md, man page
- Updated OpenAPI spec with FTS `mode`, `distance`, and `rangeMeta` schema fields

### Fixed
- **golint** — Fixed all 190 warnings across the codebase (added doc comments to all exported types, methods, constants)
- **gosec** — Fixed all 108 security issues in non-generated code:
  - G115: Added `safeInt32()` / `safeUint16()` helpers for overflow-safe integer conversions
  - G706/G704/G703/G705/G117/G404: Added targeted `#nosec` annotations with justification comments

## [2.7.1] - 2026-03-10

### Added
- **`POST /v1/delete-batch` endpoint** — Batch delete documents via REST API (previously only available in MCP/gRPC)
- **MCP tools**: `list_revisions` and `restore_revision` added to builtin tool schemas and validation map
- **MCP endpoint list**: Added missing `ingest_documents` and `upload_file` to `/v1/endpoints` MCP tools list
- **OpenAPI spec**: Added `/v1/delete-batch` endpoint definition with full request/response schema

### Fixed
- **Panel proxy 404 fix** — `server.js` mounted proxy at `/v1` which caused Express to strip the prefix; switched to root mount with `pathFilter: '/v1/**'` ([#17](https://github.com/tradik/mddb/issues/17))
- Panel endpoint counts now correct: HTTP(78), gRPC(54), MCP(53) — previously showed HTTP(76), MCP(51)
- MCP tool count updated from 52 to 53 across README, LLM_CONNECTIONS.md, and all documentation
- API.md expanded with ~35 missing endpoint docs (delete-batch, delete-collection, hybrid-search, cross-search, find-duplicates, collection-config, webhooks, revisions, automation, auth endpoints, system endpoints)

## [2.7.0] - 2026-03-06

### Added
- **Search Stats** — All search endpoints (`/v1/fts`, `/v1/vector-search`, `/v1/hybrid-search`) now return `searchStats` object with `durationMs`, `queryTerms`, `totalTokens`, and `indexSize`. Controlled by `MDDB_SEARCH_STATS` env var (default: enabled)
- **Distance Metrics**: Configurable distance metric for vector and hybrid search (cosine, dot_product, euclidean) via `distanceMetric` parameter
- **Document Revision History** — New `POST /v1/revisions` endpoint lists all revisions of a document. New `POST /v1/revisions/restore` restores a document to a previous revision
- **Collection attributes**: Per-collection type, description, icon, color, and custom metadata (`/v1/collection-config`, `/v1/collection-configs`)
- **Cross-collection search**: Search across multiple collections using a source document's embedding or text query (`/v1/cross-search`)
- **Duplicate detection**: Find exact and similar documents within a collection using content hashes and embedding similarity (`/v1/find-duplicates`, `find_duplicates` MCP tool)
- **4 new MCP tools**: `get_collection_config`, `set_collection_config`, `list_collection_configs`, `cross_search`
- **MCP tools**: `list_revisions`, `restore_revision`
- Panel: Full-page document editor (replaces constrained modal)
- Panel: Edit button moved to document header for easier access
- Panel: Document revision history viewer with restore capability
- Panel: Search stats display (duration, tokens, query terms) in all search panels

### Fixed
- Panel: Document content now refreshes after save, so re-opening editor shows updated content

### Changed
- Version bumped to 2.7.0 across all services and documentation

## [2.6.9] - 2026-03-05

### Added
- **Partial Document Update** — `PATCH /v1/update` for updating metadata and/or content independently
  - Meta only: `{"collection":"blog","key":"p1","lang":"en","meta":{"tag":["go"]}}`
  - Content only: `{"collection":"blog","key":"p1","lang":"en","contentMd":"new content"}`
  - Both: include both fields. Clear meta: `{"meta":{}}`
  - gRPC: `UpdateDocument` RPC. MCP: `update_document` tool
- **Document Metadata Read** — `GET /v1/doc-meta` returns metadata without content (lightweight)
  - gRPC: `GetDocumentMeta` RPC. MCP: `get_document_meta` tool
- **Zero-Shot Classification** — `POST /v1/classify` classifies documents against candidate labels using embedding similarity
  - By reference: provide `collection`, `key`, `lang` (reuses existing embedding if available)
  - By raw text: provide `text` field (embeds on the fly)
  - Labels embedded in a single batch call for efficiency
  - Parameters: `topK`, `multi` (return all above threshold), `threshold`
  - gRPC: `Classify` RPC. MCP: `classify_document` tool
- Panel: `updateDocument`, `getDocumentMeta`, and `classify` client methods

## [2.6.8] - 2026-03-05

### Added
- **Metadata Tag Filtering in Search** — Select metadata tags to filter FTS, vector, and hybrid search results in the panel. Dynamically loads available tags from collection. Multi-select with AND across keys, OR within values. New `MetaFilterBar` component.
- **`GET /v1/meta-keys` Endpoint** — List unique metadata keys and values for a collection. Powers the tag filter UI in the panel.
- **`GET /v1/checksum` Endpoint** — Lightweight CRC32-based collection checksum that changes on any document add/update/delete. Enables cache invalidation without downloading all documents. Also included in `/v1/stats` response per collection.
- **FTS `filterMeta` Support** — Full-text search now accepts `filterMeta` parameter for metadata pre-filtering (already supported in vector and hybrid search).

### Changed
- Version bumped to 2.6.8 across all services and documentation

## [2.6.7] - 2026-03-05

### Added
- **PMISparse Search Algorithm** - Two-phase sparse retrieval with PMI query expansion (invented by Tradik Limited)
  - BM25 scoring for direct term matches + automatic PPMI-based query expansion from corpus co-occurrence statistics
  - Lazy per-collection training with sliding-window co-occurrence matrix, automatic invalidation on document changes
  - Fuzzy variant combining edit-distance tolerance with PMI expansion for maximum recall
  - Works standalone (`algorithm: "pmisparse"`) and as the FTS component in hybrid search
  - Configurable parameters: k1, b, alpha, expansionK, windowSize, minCount, topK
  - Expansion matches marked with `~` prefix in `matchedTerms` for transparency
  - Dedicated documentation: `docs/PMISPARSE.md`
- **Sentiment Analysis for Triggers** - Keyword-based sentiment scoring for automation triggers
  - `AnalyzeSentiment()` returns score from -1.0 (negative) to +1.0 (positive) using built-in lexicon (~100 positive, ~100 negative words)
  - Optional `sentimentEnabled` condition on triggers with configurable min/max range
  - AND/OR logic (`conditionLogic`) when combining sentiment with search conditions
  - Markdown-aware text stripping before analysis
- **Automation Execution Logs** - Track webhook execution history
  - `GET /v1/automation-logs` endpoint with cursor-based pagination
  - Filter by `ruleId` and `status` (success, error, skipped)
  - TTL-based automatic cleanup with configurable retention (`MDDB_AUTOMATION_LOGS_TTL`, default: 7d)
  - Panel Logs tab with auto-refresh toggle and status filter
- **`MDDB_AUTOMATIONS` env var** - Single toggle to enable/disable entire automation system
  - `MDDB_AUTOMATION_LOGS` - Enable/disable automation execution logging
  - `MDDB_AUTOMATION_LOGS_TTL` - Log retention period (default: 7d)
- **Webhook Template Variables** - Dynamic `{{variable}}` substitution in webhook URLs and custom headers
  - Trigger variables: `{{doc.id}}`, `{{doc.key}}`, `{{doc.lang}}`, `{{doc.meta.FIELD}}`, `{{collection}}`, `{{score}}`, `{{sentiment}}`, `{{trigger.id}}`, `{{trigger.name}}`, `{{timestamp}}`, `{{webhook.id}}`, `{{event}}`
  - Cron variables: `{{cron.id}}`, `{{cron.name}}`, `{{timestamp}}`, `{{webhook.id}}`, `{{event}}`
  - Panel: collapsible help section listing available variables in webhook form

### Changed
- Version bumped to 2.6.7 across all services and documentation

## [2.6.6] - 2026-03-04

### Added
- **Automation System** - Triggers, Crons, and Webhook Targets for automated workflows
  - **Triggers**: Fire webhooks when new documents match search criteria (FTS/vector/hybrid) above threshold
  - **Crons**: Schedule periodic trigger execution using cron expressions (`robfig/cron/v3`)
  - **Webhook Targets**: Named HTTP endpoints with custom headers and configurable methods
  - Unified storage in single `automation` BoltDB bucket with `type` field
  - HTTP API: `GET/POST /v1/automation`, `GET/PUT/DELETE /v1/automation/{id}`, `POST /v1/automation/{id}/test`
  - gRPC RPCs: `ListAutomation`, `CreateAutomation`, `UpdateAutomation`, `DeleteAutomation`, `TestAutomation`
  - MCP tools: `list_automation`, `create_automation`, `update_automation`, `delete_automation`, `test_automation`
  - Env vars: `MDDB_TRIGGERS`, `MDDB_CRONS`, `MDDB_WEBHOOKS` (all default: false)
  - Webhook payload with retry backoff (0s, 1s, 5s, 15s) and custom X-MDDB headers
- **Panel: Automation Tab** - Full automation management UI
  - Type filter tabs (All/Webhooks/Triggers/Crons) with icons (Webhook/Zap/Clock)
  - Dynamic forms per type with collection/webhook/trigger dropdowns
  - Enable/disable toggle, test button (dry run with matching docs and scores)
- **Automation Tests** - 15 unit tests covering CRUD, validation, and edge cases

### Changed
- Version bumped to 2.6.6 across all services and documentation

## [2.6.5] - 2026-03-04

### Added
- **Hybrid Search** (`/v1/hybrid-search`) - New endpoint combining BM25/BM25F keyword search with vector semantic search
  - Alpha Blending strategy: weighted linear interpolation `combined = (1-α) * BM25 + α * vector`
  - RRF (Reciprocal Rank Fusion) strategy: rank-based fusion robust to different score distributions
  - Configurable: `strategy`, `alpha`, `rrfK`, `algorithm`, `vectorAlgorithm`
  - gRPC `HybridSearch` RPC and `hybrid_search` MCP tool
- **In-Graph FTS Filtering** - `filterMeta` parameter on full-text search endpoint
  - Pre-filters candidate documents by metadata before BM25 scoring
  - Supports OR within key, AND across keys
  - Added `filter_meta` field to FTS proto message
- **Panel: Hybrid Search Mode** - New search mode with strategy/alpha/algorithm controls
- **Panel: Command Modal** - Copy-ready API examples in curl, PHP, Python, and JavaScript for all search operations
- **Panel: System Info Default** - Default to System Information view after login

### Fixed
- Stale gRPC/MCP entries in endpoint documentation (removed non-existent `Delete`/`DeleteCollection` gRPC methods)

## [2.6.4] - 2026-03-04

### Added
- **BM25F Field-Weighted Search** - New full-text search algorithm
  - Weights matches in different document fields (title, tags, body) independently
  - Default weights: meta.title=3.0, meta.tags=2.0, meta.category=2.0, meta.description=1.5, content=1.0
  - Custom per-query field weights via `fieldWeights` parameter
  - Supports fuzzy matching with field weights
  - Field-level inverted index stored in dedicated BoltDB buckets
  - Algorithm option: `"bm25f"` in FTS API
- **Panel BM25F UI** - Field weights configuration panel with collapsible field weight editor, custom field support
- **Optimized Docker Pipeline** - Pre-built Go binaries instead of compiling in Docker
- **Scalar Quantization (SQ)** - New vector search algorithm
  - Quantizes float32 vectors to uint8 (0-255) using per-dimension min/max scaling
  - ADC-style search with precomputed distance tables + exact cosine re-ranking
  - ~75% memory reduction vs flat index
  - Algorithm option: `"sq"` in vector search API
- **Binary Quantization (BQ)** - New vector search algorithm
  - Reduces each float32 to 1 bit (sign bit), packed into uint64 words
  - Hamming distance for ultra-fast coarse ranking via `math/bits.OnesCount64`
  - Re-ranks top candidates with exact cosine similarity
  - ~97% memory reduction vs flat index
  - Algorithm option: `"bq"` in vector search API
- **Porter Stemming** for Full-Text Search
  - Pure Go Porter Stemmer implementation (no external deps)
  - Stems indexed terms and query terms for better recall
  - Configurable: `MDDB_FTS_STEMMING` (default: true)
  - Per-query disable via `disableStem` request field
- **Synonym Support** for Full-Text Search
  - Per-collection synonym dictionaries stored in BoltDB
  - HTTP endpoints: `POST/GET/DELETE /v1/synonyms`
  - Built-in default synonym groups (10 English groups)
  - Bidirectional query-time expansion
  - Configurable: `MDDB_FTS_SYNONYMS` (default: true)
  - Per-query disable via `disableSynonyms` request field
- **Compression Configuration**
  - `MDDB_COMPRESSION_ENABLED` - enable/disable adaptive compression
  - `MDDB_COMPRESSION_SMALL_THRESHOLD` - Snappy threshold (default: 1024)
  - `MDDB_COMPRESSION_MEDIUM_THRESHOLD` - Zstd threshold (default: 10240)
- **Extended Configuration**
  - New config sections: `fts`, `compression`, `vector` in YAML config file
  - All new features configurable via env vars, YAML, or CLI flags
  - FTS response includes `stemmingActive` and `synonymsActive` status

### Changed
- Panel VectorSearchPanel: added SQ and BQ to algorithm dropdown
- Panel FTSSearchPanel: added stemming/synonyms toggles
- Panel mddb-client: added synonym CRUD methods
- Version bumped to 2.6.4

## [2.6.2] - 2026-03-04

### Added
- **Embedding Chunking** - Auto-split long documents into chunks before embedding
  - Paragraph-based splitting with sentence and hard-split fallbacks
  - Multi-key chunk storage: `vec|collection|docID#0`, `vec|collection|docID#1`, etc.
  - Chunk deduplication in vector search: best-chunk-score per document
  - Oversampling (topK * 3) for accurate top-K after deduplication
  - Configurable via `MDDB_EMBEDDING_CHUNK_SIZE` (default 1500) and `MDDB_EMBEDDING_CHUNK_ENABLED` (default true)
  - Backward-compatible with existing non-chunked embeddings
  - Chunk stats in `/v1/vector-stats` and `/v1/vector-reindex` responses
- **Panel Mode** - `MDDB_PANEL_MODE` environment variable
  - `internal` (default): CORS enabled, browser accesses API directly
  - `external`: CORS disabled, panel reverse-proxies all `/v1/*` requests
  - Express production server (`server.js`) with `http-proxy-middleware`
  - Panel always uses relative `/v1` URLs (works in both modes)

### Changed
- Panel Dockerfile uses Express server instead of Vite preview for production
- Panel `mddb-client.js` simplified to always use relative `/v1` API base
- Vector search handlers (HTTP, gRPC, MCP) use oversampling + chunk deduplication
- All 4 vector searchers (flat, HNSW, IVF, PQ) handle chunk keys in filter matching

## [2.3.3] - 2026-02-28

### Added
- **Custom MCP Tools** - YAML-defined website-specific AI tools for mddb-mcp
  - Define custom tools in `config.yaml` under `custom_tools:` key
  - 3 supported actions: `semantic_search`, `search_documents`, `full_text_search`
  - Preconfigured defaults merged with user arguments at runtime
  - Custom tools appear alongside 23 built-in tools in `tools/list`
  - Startup validation: name conflicts, valid actions, valid param types
  - Works on both transports: stdio (Claude Desktop) and HTTP
  - Deduplicated tool definitions (~420 lines removed, shared `builtinTools()`)
  - Dedicated documentation: `docs/CUSTOM-TOOLS.md`

## [2.3.2] - 2026-02-28

### Added
- **Telemetry** - Prometheus-compatible `/metrics` endpoint
  - HTTP request counters with method, path, and status labels
  - Request duration histograms (12 buckets from 1ms to 10s)
  - Database metrics: file size, documents, revisions, meta indices per collection
  - Vector search metrics: embeddings count, index readiness, queue size
  - Webhook and schema counts
  - Go runtime: goroutines, memory stats, GC metrics
  - Zero external dependencies (pure Go text exposition format)
  - DB stats cached for 15s to minimize scan overhead
  - Configurable via `MDDB_METRICS` env var (enabled by default)
  - Dedicated documentation: `docs/TELEMETRY.md` with Grafana queries and alerting rules

## [2.3.1] - 2026-02-28

### Added
- **Schema Validation** - JSON Schema validation for document metadata
  - Per-collection schemas (opt-in, disabled by default)
  - HTTP endpoints: `/v1/schema/set`, `/v1/schema/get`, `/v1/schema/delete`, `/v1/schema/list`, `/v1/validate`
  - gRPC RPCs: `SetSchema`, `GetSchema`, `DeleteSchema`, `ListSchemas`, `ValidateDocument`
  - Supported rules: `required`, `properties` (types), `enum`, `pattern`, `minItems`/`maxItems`
  - Automatic validation on document add/update when schema is set
  - CLI commands: `schema set/get/delete/list`, `validate`
  - MCP tools: `set_schema`, `get_schema`, `delete_schema`, `list_schemas`, `validate_document`
  - PHP and Python extension support
  - Dedicated documentation: `docs/SCHEMA-VALIDATION.md`
- **SECURITY.md** - Security policy and vulnerability reporting process
- **CONTRIBUTING.md** - Contribution guidelines with setup instructions

## [2.1.0] - 2025-01-09

### Added
- **Health check endpoints** - `/health` and `/v1/health` for monitoring
  - Simple JSON response with status and mode
  - Database connectivity verification
  - HTTP 200 for healthy, 503 for unhealthy
- **OpenAPI/Swagger documentation** - Complete API specification
  - OpenAPI 3.0.3 specification in `docs/openapi.yaml`
  - Interactive Swagger UI in `docs/swagger.html`
  - Machine-readable API documentation
  - Try-it-out functionality for all endpoints
- **Health check documentation** - Comprehensive guide in `docs/HEALTHCHECK.md`
  - Docker and Docker Compose examples
  - Kubernetes liveness and readiness probes
  - Load balancer configuration (Nginx, HAProxy, Traefik)
  - Manual health check methods (curl, wget, httpie)
  - Monitoring integration examples
  - Troubleshooting guide

### Changed
- Updated Docker health checks to use `/health` endpoint
- Updated docker-compose.yml with proper health check configuration
- Updated Dockerfile with health check using wget
- Simplified performance claims in documentation (more pragmatic, less boastful)
- Removed Polish documentation files (English only)
- Fixed license badge and references (BSD-3-Clause)

## [2.0.3] - 2025-11-07

### Added
- **Document deletion functionality** - Delete documents with confirmation dialog
  - Delete button in document list items
  - Delete button in document viewer header
  - Confirmation modal with document details
  - Automatic list refresh after deletion
- **Error handling improvements** - Better error boundaries and fallbacks
  - ReactMarkdown error boundary with raw content fallback
  - Progressive document loading (immediate display + background fetch)
  - User-friendly error messages with recovery options
- **Docker image for mddb-panel** - Complete containerization
  - Multi-stage Docker build for production
  - Development Docker configuration
  - Docker Compose integration
  - Makefile targets for panel Docker operations

### Fixed
- **Blank document viewer issue** - Documents now display immediately with content
- **Document content loading** - Fixed API integration for full document fetching
- **ReactMarkdown compatibility** - Removed deprecated className prop
- **Content overflow issues** - Fixed margin and layout problems in document viewer
- **UI responsiveness** - Better loading states and user feedback
- **golangci-lint errors** - Fixed unchecked error returns in JSON encoding

### Improved
- **Document viewer layout** - Better container constraints and overflow handling
- **User experience** - Immediate feedback when clicking documents
- **Error recovery** - Users can always access document content in some form
- **Development workflow** - Added panel to development Docker setup
- **Web Admin Panel (mddb-panel)** - Modern React-based admin interface
  - Server statistics dashboard
  - Collection browser with document count
  - Document list with metadata preview
  - Document viewer with full content and metadata
  - **Document editor** - Edit markdown content and metadata
  - **New document creation** - Create documents directly from UI
  - **Markdown editor with live preview** - Split view with real-time rendering
  - **Markdown toolbar** - Quick formatting buttons (bold, italic, headings, lists, etc.)
  - **Syntax highlighting** - Code blocks with language-specific highlighting
  - **Markdown templates** - Pre-built templates (blog, docs, README, API, changelog)
  - Advanced filtering by metadata fields
  - Sort by date, key, or custom fields
  - Copy document content to clipboard
  - Modern UI with TailwindCSS and Lucide icons
  - Built with React 19, Vite 6, and Zustand 5
  - Docker support with multi-stage builds
  - Proxy configuration for API requests
- Bulk import script for loading markdown files from folders
- `load-md-folder.sh` script with features:
  - Automatic key generation from filenames
  - YAML frontmatter metadata extraction
  - Recursive folder scanning
  - Progress tracking with statistics
  - Dry run mode for preview
  - Custom metadata support
  - Multi-language support
- Makefile targets for folder import:
  - `import-folder` - Import markdown files
  - `import-folder-dry` - Preview import without executing
  - `import-folder-recursive` - Import recursively
- Makefile targets for panel:
  - `panel-install` - Install panel dependencies
  - `panel-dev` - Run panel in development mode
  - `panel-build` - Build panel for production
  - `panel-preview` - Preview production build
- Comprehensive bulk import documentation (BULK-IMPORT.md)
- README section for bulk import usage
- README section for web admin panel
- Docker Compose configuration for panel service

## [2.0.2] - 2025-11-07

### Changed
- Updated quic-go to v0.55.0 (HTTP/3 improvements)
- Updated Alpine base image to 3.22 (security updates)
- Updated Go dependencies (crypto, net, sys, mod, text, tools)
- Disabled automatic workflow triggers (manual only)
- Removed Docker buildcache (not needed for users)
- Removed dev Docker images (production only)

### Fixed
- Docker build context issues
- Docker Hub description update (now manual)

## [2.0.1] - 2025-11-07

### Fixed
- Fixed all golangci-lint issues (18 total)
  - errcheck: Added error checking for binary.Write, file.Close, res.Body.Close
  - staticcheck: Removed redundant nil check, optimized fmt usage, fixed pointer usage
  - unused: Removed unused struct fields (mu, workerPool, current)
- Fixed proto definitions for UpdateDocument, DeleteDocument, and batch responses
- Added missing SaveRevision and NotFound fields to proto messages

### Added
- Docker Hub integration with automated builds
- Multi-platform Docker images (AMD64 + ARM64)
- Comprehensive Docker Hub documentation
- GitHub Actions workflow for Docker builds and pushes
- Docker Compose configuration for production deployment

### Changed
- Updated golangci-lint to v2.6.1 with Go 1.26 support
- Improved error handling across codebase
- Optimized buffer pool usage to avoid allocations

## [2.0.0] - 2025-11-07

### Major Performance Release

**Significant performance improvements through multiple optimization strategies**

#### Performance Enhancements
- Protobuf serialization for smaller payloads
- BoltDB tuning (NoFreelistSync, FreelistMapType, optimized mmap)
- Conditional metadata reindexing
- Batch processing with single transactions
- Parallel processing with worker pools
- Connection pooling for gRPC
- Bucket caching
- Optional revision history
- Single transaction search
- Lazy indexing with async queue
- Read-through document cache
- Batch delete and update operations

#### Advanced Features (Extreme Mode)
Enable with `MDDB_EXTREME=true` environment variable:
- Write-Ahead Log (WAL) with periodic sync
- Lock-free cache with 16 shards
- MVCC snapshot isolation
- Bloom filters for fast lookups
- Delta encoding for smaller revisions
- Adaptive compression (Snappy/Zstd)
- HTTP/3 + QUIC support
- Adaptive indexing
- Async I/O operations
- Zero-copy I/O
- Vectorized operations (SIMD)
- Distributed sharding

### Benchmark Results

Tested with 3000 documents:
- MDDB (Batch API): 29,810 docs/s, 34µs avg latency
- MongoDB: 5,176 docs/s, 192µs avg latency
- PostgreSQL: 4,324 docs/s, 231µs avg latency
- MySQL: 1,214 docs/s, 822µs avg latency
- CouchDB: 312 docs/s, 3,185µs avg latency

### Added
- HTTP/3 server on port 11443 (Extreme Mode)
- Comprehensive performance benchmarking suite
- Comparison tests with MongoDB, PostgreSQL, MySQL, CouchDB

## [1.0.0] - Initial Release

### Added
- Initial release of MDDB
- RESTful API for markdown document management
- **gRPC API** - High-performance binary protocol (70% smaller payload than JSON)
- **Dual protocol support** - HTTP (port 11023) and gRPC (port 11024) run simultaneously
- **Docker images** - Optimized Alpine Linux images (~15 MB)
- **Docker Compose** - Production and development configurations
- **Shared Protobuf** - Monorepo structure with centralized proto definitions
- **Multi-language clients** - Generated code for Go, Python, Node.js, PHP
- BoltDB-based storage engine
- Document versioning and revision history
- Metadata indexing and search
- Multi-language support
- Template variable substitution
- Export functionality (NDJSON and ZIP formats)
- Backup and restore capabilities
- Revision truncation for database maintenance
- Access mode control (read, write, read-write)
- **Statistics endpoint** - `/v1/stats` for server and database monitoring
- **Command-line client (mddb-cli)** - Full-featured CLI similar to mysql-client
- **Unix man page** - Complete manual page for CLI
- Comprehensive documentation
- Makefile with development and build targets
- Systemd service configuration

### Features

#### Core Functionality
- Add/update markdown documents with metadata
- Retrieve documents by key and language
- Search with metadata filtering
- Sort by addedAt, updatedAt, or key
- Pagination support
- Template variable substitution (%%var%% syntax)

#### Storage
- BoltDB embedded database
- Automatic metadata indexing
- Revision history tracking
- Efficient prefix-based indices
- ACID transactions

#### API Endpoints
- `POST /v1/add` - Add or update documents
- `POST /v1/get` - Retrieve documents
- `POST /v1/search` - Search with filters
- `POST /v1/export` - Export as NDJSON or ZIP
- `GET /v1/backup` - Create backup
- `POST /v1/restore` - Restore from backup
- `POST /v1/truncate` - Truncate revision history
- `GET /v1/stats` - Server and database statistics

#### Developer Experience
- Comprehensive Makefile with colored output
- Hot reload support with Air
- Cross-platform builds (Linux, Windows, macOS)
- Test coverage reporting
- Code formatting and linting targets
- Development and production modes

#### Command-Line Client
- `mddb-cli` - Full-featured CLI client
- Unix-style commands (add, get, search, export, backup, restore, truncate, stats)
- Man page documentation (`man mddb-cli`)
- JSON and human-readable output formats
- Pipe-friendly content-only mode
- Metadata filtering and search
- Template variable support
- Batch operation support
- Server statistics display

#### Documentation
- Quick start guide
- Complete API documentation
- Usage examples with multiple languages
- Architecture overview with diagrams
- Production deployment guide
- Docker and systemd configurations

### Technical Details
- Go 1.26+ required
- BoltDB for storage
- HTTP/JSON API
- Single binary deployment
- No external dependencies

## [0.1.0] - 2024-11-06

### Added
- Initial project structure
- Basic MDDB server implementation
- Core API endpoints
- Documentation suite
- Build system with Makefile
- Docker support

---

## Release Notes

### Version 0.1.0 (Initial Release)

This is the first release of MDDB - a lightweight markdown database with a RESTful API.

**Key Features:**
- Store and manage markdown documents with metadata
- Full revision history
- Fast metadata-based search
- Multi-language support
- Export capabilities
- Easy backup and restore

**Getting Started:**
```bash
make build
make run
```

See the [Quick Start Guide](docs/QUICKSTART.md) for detailed instructions.

**Requirements:**
- Go 1.26 or later
- 512 MB RAM minimum
- Linux, macOS, or Windows

**Known Limitations:**
- Single-writer (BoltDB limitation)
- No built-in authentication
- No full-text search (planned for future release)

**Future Plans:**
- Full-text search integration
- Built-in authentication
- GraphQL API
- Replication support
- Plugin system

---

## Contributing

When contributing, please:
1. Update this CHANGELOG with your changes
2. Follow [Keep a Changelog](https://keepachangelog.com/) format
3. Add entries under `[Unreleased]` section
4. Use these categories: Added, Changed, Deprecated, Removed, Fixed, Security

## Links

- [Repository](https://github.com/tradik/mddb)
- [Documentation](docs/)
- [Issues](https://github.com/tradik/mddb/issues)
