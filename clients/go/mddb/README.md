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
| `WithFollowLeader(on)` | Follow the cluster leader for writes (default off; see below). |

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

## Clusters: following the leader

In a cluster with automatic leader election (mddbd 2.19.8+) every node serves
reads, but only the leader accepts writes. A follower answers a write with
**421 Misdirected Request**, the leader's HTTP base URL in the
`X-MDDB-Leader` header and a body of
`{"error":"not the leader","leaderAddr":"...","leaderId":"n2","term":5}`.
While an election runs and no leader is known, it answers **503** with
`Retry-After: 1` and `{"error":"no leader: an election is in progress"}`.

Off by default, the client returns both as an `*APIError`: a 421 carries
`LeaderAddr` (header first, body as fallback), `LeaderID` and `Term`; a 503
during an election has `NoLeader` set.

```go
_, err := c.Add(ctx, req)
var apiErr *mddb.APIError
if errors.As(err, &apiErr) && apiErr.LeaderAddr != "" {
	c = mddb.New(apiErr.LeaderAddr, mddb.WithAPIKey(key)) // or follow automatically
}
```

With `WithFollowLeader(true)`:

- a write answered with 421 is sent **once** more, to the leader it names. A
  second 421 is returned as the error; there is no redirect loop. Only an
  absolute `http`/`https` address is followed.
- the client remembers the leader (`c.Leader()`). A request (method and path)
  that was redirected once is a write, so later requests of that kind go to
  the leader directly; every other request, reads included, keeps going to
  the configured base URL. When the remembered leader cannot be reached, it
  is forgotten and the next write asks the configured node again.
- a 503 "no leader" is retried up to three times, waiting as `Retry-After`
  says (capped at three seconds), within the call's context deadline.
- JSON bodies are buffered, so they can be sent again. `DoStream` follows a
  421 only when its body is `nil` or an `io.Seeker` (an `*os.File` is); with
  any other reader it remembers the leader and returns the 421, so the next
  call goes to the leader. With the option on, `DoStream` does not close a
  seekable body; close it yourself.

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
- **Bulk:** `Ingest` (`IngestRequest` with `IngestOptions{SkipEmbeddings, SkipFTS, SkipWebhooks, SkipDuplicates, AutoConfigureCollection, SaveRevision, Profile, TextOnly}`) → `IngestResponse`; an `IngestDocument` may carry a file in `Content` with its `Format` (pdf, docx, html, ...) for the server to convert (v2.18.9+), and `IngestResponse.Conversions` says how well each converted; `IngestOptions.ExtractImages` / `FetchRemoteImages` and `IngestDocument.Images` store a document's images as image documents (v2.18.9+); `DeleteBatch` → `DeleteBatchResponse`; `Aggregate` (facets, date histograms) → `AggregateResponse`.
- **Search (typed):** `SearchFTS` → `FTSResponse`, `SearchVectors` → `VectorSearchResponse`, `SearchHybrid` → `HybridSearchResponse`, `CodeGraph` → `CodeGraph`; `IncludeImages` on the three search requests returns each hit's `Images` and, with an image-capable provider, `ImageHits`; `VectorSearchRequest.QueryImage` searches by an image (v2.18.9+).
- **Vectors (typed):** `GetVectorStats` → `VectorStats`, `ReindexVectors` (with `Force`) → `VectorReindexResponse`.
- **Search (raw JSON, kept for compatibility):** `FTS`, `VectorSearch`, `VectorStats`, `VectorReindex`.
- **Collections:** `GetCollectionConfig` → `CollectionConfigResponse` (including `code.includePaths`, `embedding.{chunkSize,chunkMode}`, `fts.enabled`); `SetCollectionConfig` replaces the whole config, so read it, change it and send all of it back.
- **Storage:** `Storage(ctx, collection)` → `StorageBreakdown` (`GET /v1/storage`; `""` reads the whole database, which takes minutes on a large file).
- **Health:** `Status` → `Health` (see above).
- **Stats:** `Stats` → typed `Stats` (+ per-collection `CollectionStats`).
- **Webhooks:** `RegisterWebhook`, `ListWebhooks`, `DeleteWebhook` → typed `Webhook`.
- **Admin:** schema (`SetSchema`/`GetSchema`/`ListSchemas`/`DeleteSchema`), `ValidateDocument`, `Export`/`Restore`/`Truncate`, auth (`Login`/`CreateAPIKey`/`ListAPIKeys`/`DeleteAPIKey`), `GraphQL`, `Diagnose(ctx, DiagnoseOptions{Profiles, Integrity, KeepNames})` → the diagnose bundle's tar.gz bytes (`GET /v1/admin/diagnose`, v2.18.10+; give `Integrity` a long context deadline).
- **Escape hatch:** `Do(ctx, method, path, body)` for any endpoint not yet wrapped.

Field names and JSON tags follow the server's own request and response
structs. A few response tags are snake_case (`index_ready`,
`total_documents`) because the server writes them that way.
`DeleteBatchResponse.NotFound` reads the server's `notFound` (2.19.0+) and falls
back to the deprecated `not_found` of an older server.

Any HTTP status ≥ 400 is returned as an `*APIError{StatusCode, Body}`, except a
503 warm-up answer to `Status`.
