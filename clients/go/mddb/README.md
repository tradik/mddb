# mddb — official Go client for MDDB

[![Go](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![Go Reference](https://pkg.go.dev/badge/github.com/tradik/mddb/clients/go/mddb/v2.svg)](https://pkg.go.dev/github.com/tradik/mddb/clients/go/mddb/v2)
[![License](https://img.shields.io/badge/license-see%20repo-lightgrey)](../../../LICENSE)

The shared HTTP/JSON client for the [MDDB](../../../README.md) document server. It
is the single client used by [`mddb-cli`](../../../services/mddb-cli/) and by
external Go integrations, so neither has to re-implement request building,
authentication or response parsing.

## Install

```bash
go get github.com/tradik/mddb/clients/go/mddb/v2@latest
```

The module path is `github.com/tradik/mddb/clients/go/mddb/v2` (the `/v2`
suffix is what Go requires for v2+ versions) and the package name is `mddb`.
It is tagged `clients/go/mddb/vX.Y.Z` with each server release, so a client
version matches the server version it was written against. Inside the
monorepo it is wired via `go.work` and a relative `replace`.

## Usage

```go
package main

import (
	"context"
	"fmt"
	"os"

	mddb "github.com/tradik/mddb/clients/go/mddb/v2"
)

func main() {
	c := mddb.New("http://localhost:11023",
		mddb.WithAPIKey(os.Getenv("MDDB_API_KEY")),
	)

	doc, err := c.Add(context.Background(), mddb.AddRequest{
		Collection: "blog",
		Key:        "hello",
		Lang:       "en",
		ContentMD:  "# Hello",
	})
	if err != nil {
		panic(err)
	}
	fmt.Println("stored", doc.ID)

	stats, _ := c.Stats(context.Background())
	fmt.Printf("%d documents across %d collections\n", stats.TotalDocuments, len(stats.Collections))
}
```

## Options

| Option | Effect |
|---|---|
| `WithAPIKey(key)` | Send `X-API-Key` (takes precedence over a token). |
| `WithToken(jwt)` | Send `Authorization: Bearer <jwt>`. |
| `WithHTTPClient(h)` | Use a custom `*http.Client`. Its own `Timeout`, if set, is a hard cap. |
| `WithTimeout(d)` | Timeout for calls whose context has no deadline (default 30s, `0` = none). |
| `WithVerbose(w)` | Log each request body to `w` (e.g. `os.Stderr`). |

## Timeouts

A call whose context has no deadline gets the client's default timeout
(`DefaultTimeout`, 30s, or whatever `WithTimeout` set). A call whose context
already has a deadline uses that deadline instead, longer or shorter. Bulk
ingest and vector reindexing on a large collection take longer than 30s, so
give those calls their own deadline:

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
defer cancel()
res, err := c.ReindexVectors(ctx, mddb.VectorReindexRequest{Collection: "docs", Force: true})
```

The default is applied through the context, not `http.Client.Timeout`, which
would cap every call regardless of its context. If you pass your own client
with `WithHTTPClient`, leave its `Timeout` at zero to keep this behaviour.

## Health and warm-up

`Status(ctx)` returns a `Health`. While the server starts it answers `/health`
with 503 and `{"status":"warming_up","phase":...}`; `Status` returns that as
`Ready=false` with `Phase`, `ElapsedSeconds` and, for phases that can be
measured, `Progress` (`BytesDone`, `BytesTotal`, `Percent`, `EtaSeconds`),
not as an error. Any other status of 400 or more, including 503 `unhealthy`,
is an `*APIError`, so a starting server can be told apart from a broken one.

```go
for {
	h, err := c.Status(ctx)
	if err != nil {
		return err // down or broken
	}
	if h.Ready {
		break
	}
	log.Printf("mddb starting: %s, %ds", h.Phase, h.ElapsedSeconds)
	time.Sleep(2 * time.Second)
}
```

## API surface

- **Documents:** `Add`, `Get`, `Search`, `SetTTL`, `ImportURL` → typed `Document`/`[]Document`.
- **Bulk:** `Ingest` (`IngestRequest` with `IngestOptions{SkipEmbeddings, SkipFTS, SkipWebhooks, SkipDuplicates, AutoConfigureCollection, SaveRevision, Profile, TextOnly}`) → `IngestResponse`; an `IngestDocument` may carry a file in `Content` with its `Format` (pdf, docx, html, ...) for the server to convert (v2.18.9+), and `IngestResponse.Conversions` says how well each converted; `DeleteBatch` → `DeleteBatchResponse`; `Aggregate` (facets, date histograms) → `AggregateResponse`.
- **Search (typed):** `SearchFTS` → `FTSResponse`, `SearchVectors` → `VectorSearchResponse`, `SearchHybrid` → `HybridSearchResponse`, `CodeGraph` → `CodeGraph`.
- **Vectors (typed):** `GetVectorStats` → `VectorStats`, `ReindexVectors` (with `Force`) → `VectorReindexResponse`.
- **Search (raw JSON, kept for compatibility):** `FTS`, `VectorSearch`, `VectorStats`, `VectorReindex`.
- **Collections:** `GetCollectionConfig` → `CollectionConfigResponse` (including `code.includePaths`, `embedding.{chunkSize,chunkMode}`, `fts.enabled`); `SetCollectionConfig` replaces the whole config, so read it, change it and send all of it back.
- **Storage:** `Storage(ctx, collection)` → `StorageBreakdown` (`GET /v1/storage`; `""` reads the whole database, which takes minutes on a large file).
- **Health:** `Status` → `Health` (see above).
- **Stats:** `Stats` → typed `Stats` (+ per-collection `CollectionStats`).
- **Webhooks:** `RegisterWebhook`, `ListWebhooks`, `DeleteWebhook` → typed `Webhook`.
- **Admin:** schema (`SetSchema`/`GetSchema`/`ListSchemas`/`DeleteSchema`), `ValidateDocument`, `Export`/`Restore`/`Truncate`, auth (`Login`/`CreateAPIKey`/`ListAPIKeys`/`DeleteAPIKey`), `GraphQL`.
- **Escape hatch:** `Do(ctx, method, path, body)` for any endpoint not yet wrapped.

Field names and JSON tags follow the server's own request and response
structs. A few response tags are snake_case (`not_found`, `index_ready`,
`total_documents`) because the server writes them that way.

Any HTTP status ≥ 400 is returned as an `*APIError{StatusCode, Body}`, except a
503 warm-up answer to `Status`.
