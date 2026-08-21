# mddb — official Go client for MDDB

[![Go](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![Module](https://img.shields.io/badge/module-mddb--client-blue)](.)
[![License](https://img.shields.io/badge/license-see%20repo-lightgrey)](../../../LICENSE)

The shared HTTP/JSON client for the [MDDB](../../../README.md) document server. It
is the single client used by [`mddb-cli`](../../../services/mddb-cli/) and by
external Go integrations, so neither has to re-implement request building,
authentication or response parsing.

## Install

In the monorepo this module (`mddb-client`) is wired via `go.work` and a relative
`replace`. Standalone:

```bash
go get mddb-client
```

## Usage

```go
package main

import (
	"context"
	"fmt"
	"os"

	mddb "mddb-client"
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
| `WithHTTPClient(h)` | Use a custom `*http.Client`. |
| `WithTimeout(d)` | Override the default 30s per-request timeout. |
| `WithVerbose(w)` | Log each request body to `w` (e.g. `os.Stderr`). |

## API surface

- **Documents:** `Add`, `Get`, `Search`, `SetTTL`, `ImportURL` → typed `Document`/`[]Document`.
- **Stats:** `Stats` → typed `Stats` (+ per-collection `CollectionStats`).
- **Webhooks:** `RegisterWebhook`, `ListWebhooks`, `DeleteWebhook` → typed `Webhook`.
- **Search/index:** `FTS`, `VectorSearch`, `VectorStats`, `VectorReindex` (raw JSON for variable-shape responses).
- **Admin:** schema (`SetSchema`/`GetSchema`/`ListSchemas`/`DeleteSchema`), `ValidateDocument`, `Export`/`Restore`/`Truncate`, auth (`Login`/`CreateAPIKey`/`ListAPIKeys`/`DeleteAPIKey`), `GraphQL`.
- **Escape hatch:** `Do(ctx, method, path, body)` for any endpoint not yet wrapped.

Any HTTP status ≥ 400 is returned as an `*APIError{StatusCode, Body}`.
