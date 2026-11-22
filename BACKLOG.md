# Backlog

One line is one session: a ready prompt, its size (S/M/L) and the model to run
it on. No status here: state lives in issues and CHANGELOG.md. A session with no
other instruction takes the first line, does only that line, commits, and
deletes the line.

| Prompt | Size | Model |
|---|---|---|
| SEO: add a `VideoObject` JSON-LD block for the homepage video (name, description, thumbnailUrl, uploadDate from the YouTube page, embedUrl, contentUrl); make `check_schema: strict` pass | S | Sonnet |
| SEO: give the 50 generated API reference pages (`docs/api/reference/*`) meta descriptions of 70–160 characters, via the OpenAPI tag descriptions in `docs/openapi.yaml` | M | Sonnet |
| Perf: cut render-blocking web fonts (three Google Fonts families on every page): self-host Geist and Geist Mono as WOFF2 with `font-display: swap`, drop Instrument Serif if unused, and recompress `images/hero.webp` (345 KB) | M | Sonnet |
| #439 follow-up: offer a scenario's external MCP servers' tools to an MCP key bound to that scenario, under their prefixed names (the optional part of #439 left out of 2.19.9) | L | Opus |
| Chat: an empty `allowedCollections` means the chat service's `default_collection` in mddb-chat, "no collection" in the panel hint and for a scenario-bound MCP key; pick one meaning, apply it in all three and document it | M | Opus |
| MCP: `completion/complete` and the `rag-pipeline` prompt read collection names and configs without an RBAC check for main-auth users; `/events` on the MCP listener streams every collection to an unscoped key when main auth is off; `s.mcpAuth` is written in the `serveMCP` goroutine and read by handlers without synchronisation | M | Opus |
| Auth: `IsAdmin` ignores admin on `*` granted through a group (decide and document); `UpdateGroup` changes the cache before the bolt write; `handleAuthGroups` / `handleAuthGroupDetail` build JSON errors with `fmt.Sprintf` and no escaping; `wordpress_publish` is classed `ruleRead` but writes to an external site | M | Opus |
| Panel: a screenshot script for `docs/panel-*.png`, then `docs/panel-llm-connections-claude.png` (#440); check the Claude tab's contrast in light and dark theme | M | Sonnet |
| Panel: remote command / project-file variants for the other MCP tabs of LLM Connections (the `variants` mechanism of #440); a free-text field for `role:` and alias names in the MCP key form; replace the local `DeleteConfirmModal` of UsersPanel and GroupsPanel with `ConfirmDialog` | M | Sonnet |
| Docs: one form of the `claude mcp add` example (`--transport http mddb <url> --header …`) in docs/AGENTS.md, docs/USES-*.md and docs/LLM_CONNECTIONS.md "API Key Authentication" | S | Sonnet |
| Docs: docs/GRAPHQL.md is stale — the footer says Version 2.7.1 / March 2026 and "Contributing" calls the resolvers in progress; check what GraphQL serves today (`services/mddbd/graphql`) and rewrite those parts from the code | S | Sonnet |
| `/v1/rerank` without a reranking model answers 503 (#450): decide whether a lexical fallback (the `lexical` provider's scoring) is worth having, and if so add it behind the same `rerankApi` module, documented as not semantic | S | Sonnet |
| Verify the 2.19.10 blog post's commands on a fresh local container (docker run with `MDDB_MCP_API_KEY_ENABLED=true`, autoindex, scoped key, `.mcp.json` with `${MDDB_MCP_API_KEY}`) and the VS Code tab's `.vscode/mcp.json` / `code --add-mcp` in a real VS Code; fix anything that differs | S | Sonnet |
