---
title: "Catalogue Listing Text"
slug: "docs/listing"
description: "The one description of MDDB for the MCP Registry, Docker Hub, GitHub and the MCP directories: one-liner, long text, categories, proof and the submissions log."
status: publish
---

# Catalogue Listing Text

Every catalogue asks for a description in its own form: a JSON field, a web
form, a pull request, a YAML file. Written separately, they drift: two releases
later there are five different tool counts and three binary sizes. This page is
the only source. `server.json` (the MCP Registry entry), `docs/DOCKER_HUB_SHORT.txt`
and the opening paragraph of [DOCKER_HUB.md](DOCKER_HUB.md) copy from here, and
`services/mddbd/mcp_registry_doc_test.go`, `mcp_tool_count_doc_test.go` and
`site_claims_doc_test.go` fail when a copy differs or when the tool count or the
binary size stops matching `.ssg.yaml` (`mcpTools`, `mddbBinarySize`), which is
where those two numbers are kept.

When a number changes, change it in `.ssg.yaml`, then here, then in the copies;
the tests list what is left. The GitHub repository description has no guard, so
it is on the release checklist instead.

## One-liner

Up to 100 characters, the MCP Registry schema's hard limit for `description`.

> Embedded knowledge base with 107 MCP tools, hybrid BM25+vector search and RAG. One Go binary.

Used for: `server.json` `description`, the Docker Hub short description
(`docs/DOCKER_HUB_SHORT.txt`), the GitHub repository description.

## Long description

> AI-native embedded knowledge base with a built-in MCP server (107 tools), hybrid BM25+vector search and RAG. Single ~32 MB Go binary, zero config. In production behind daemonports.org (about 120,000 pages).

Used for: the Docker Hub overview (first paragraph of
[DOCKER_HUB.md](DOCKER_HUB.md)), mcp.so, PulseMCP, the Docker MCP Catalog
`about` field, and any directory with room for three sentences.

## Without numbers

For lists nobody refreshes (awesome lists, blog round-ups), so that the entry
is not wrong a release later:

> Embedded knowledge base with a built-in MCP server, hybrid BM25+vector search and RAG. One Go binary, Docker image, zero config.

## Categories

- **Knowledge & Memory** (primary)
- Databases
- Search

## Production proof

[daemonports.org](https://daemonports.org/), in the words of the README's
"Used by" section: about 120,000 pages from the ports trees of five BSD systems
in one collection, its public full-text search and autocomplete served by MDDB.
Use that wording; do not write a second one.

## Install snippet

The one in the README, section
[Connect to Claude / Cursor / Windsurf (MCP)](https://github.com/tradik/mddb#connect-to-claude--cursor--windsurf-mcp):
`docker run` with `--network host`, the `mddb-data` volume and
`MDDB_MCP_STDIO=true`. Link to it or paste it from there; this page does not
carry a copy, so there is one snippet in the repository and
`TestMCPToolCountDocsInSync` counts one.

## Submissions

Where the text has been pasted, when, and by whom, so that a changed number can
be chased to every entry. The MCP Registry row is written by the release
workflow (`publish-mcp-registry` in `.github/workflows/release.yml`) at every
tag once the `mcp-registry` environment holds `MCP_DNS_PRIVATE_KEY`; the others
are by hand.

| Catalogue | Text used | Date | Entry | By |
|---|---|---|---|---|
