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
| Panel: an `mcp_` key can be created under *LLM Connections → API Keys* while the server runs without `MDDB_MCP_API_KEY_ENABLED=true`, where the MCP listener never checks it (401 from the main auth gate). Show that state from `protocols.mcp.apiKeyEnabled` in `/v1/config` next to the key form, and say it in docs/MCP.md | S | Sonnet |
| #439 follow-up: offer a scenario's external MCP servers' tools to an MCP key bound to that scenario, under their prefixed names (the optional part of #439 left out of 2.19.9) | L | Opus |
| Chat: an empty `allowedCollections` means the chat service's `default_collection` in mddb-chat, "no collection" in the panel hint and for a scenario-bound MCP key; pick one meaning, apply it in all three and document it | M | Opus |
| MCP: `completion/complete` and the `rag-pipeline` prompt read collection names and configs without an RBAC check for main-auth users; `/events` on the MCP listener streams every collection to an unscoped key when main auth is off; `s.mcpAuth` is written in the `serveMCP` goroutine and read by handlers without synchronisation | M | Opus |
| Auth: `IsAdmin` ignores admin on `*` granted through a group (decide and document); `UpdateGroup` changes the cache before the bolt write; `handleAuthGroups` / `handleAuthGroupDetail` build JSON errors with `fmt.Sprintf` and no escaping; `wordpress_publish` is classed `ruleRead` but writes to an external site | M | Opus |
| Panel: a screenshot script for `docs/panel-*.png`, then `docs/panel-llm-connections-claude.png` (#440); check the Claude tab's contrast in light and dark theme | M | Sonnet |
| Panel: remote command / project-file variants for the other MCP tabs of LLM Connections (the `variants` mechanism of #440); a free-text field for `role:` and alias names in the MCP key form; replace the local `DeleteConfirmModal` of UsersPanel and GroupsPanel with `ConfirmDialog` | M | Sonnet |
| Docs: one form of the `claude mcp add` example (`--transport http mddb <url> --header …`) in docs/AGENTS.md, docs/USES-*.md and docs/LLM_CONNECTIONS.md "API Key Authentication" | S | Sonnet |
| #444: deflake the cluster e2e tests on Windows — `TestClusterFailoverWhenTheLeaderIsKilled` writes right after `tc.leader()` and loses to a re-election (`testElection` 150 ms); wait for a stable leader+term or follow `notLeaderError.LeaderHTTP`, apply to all tests in `services/mddbd/cluster_e2e_test.go`, run the suite 20× with `-count` | S | Sonnet |
| Open WebUI pgvector provider: decide, then (if yes) a narrow PostgreSQL wire listener (opt-in env, own port) that answers only Open WebUI's `_retrieve_pgvector` — startup/SSL refusal, SCRAM auth mapped to mddb API keys, the `pg_type` lookup of `register_vector`, and the one parametrised `SELECT … <=> $1 … WHERE collection = $2 … LIMIT $3` over a collection's vectors; prove it with real psycopg+pgvector; open an issue first with the scope and the security review | L | Opus |
