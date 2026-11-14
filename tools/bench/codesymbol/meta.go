package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"mddb-bench/internal/benchkit"
)

// metaStats is what the semantic facts cost per code document: the bytes of
// every sym* value it stores.
type metaStats struct {
	Documents   int `json:"documents"`   // code documents with symIndex
	MedianBytes int `json:"medianBytes"` // sym* bytes per document
	P99Bytes    int `json:"p99Bytes"`
	MaxBytes    int `json:"maxBytes"`
	TotalBytes  int `json:"totalBytes"`
	Over64KiB   int `json:"over64KiB"` // documents above 64 KiB of facts
	Truncated   int `json:"truncated"` // documents the server cut at MDDB_CODE_MAX_FACTS
}

// factBytes exports a collection and measures its facts.
func factBytes(client *http.Client, base, collection string) (metaStats, error) {
	raw, err := benchkit.Post(client, base+"/v1/export", map[string]any{"collection": collection, "format": "ndjson"})
	if err != nil {
		return metaStats{}, err
	}
	return factBytesOf(string(raw))
}

// factBytesOf measures the facts of an ndjson export.
func factBytesOf(ndjson string) (metaStats, error) {
	var st metaStats
	var sizes []int
	sc := bufio.NewScanner(strings.NewReader(ndjson))
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var doc struct {
			Meta map[string][]string `json:"meta"`
		}
		if err := json.Unmarshal(sc.Bytes(), &doc); err != nil {
			return st, err
		}
		idx := doc.Meta["symIndex"]
		if len(idx) == 0 {
			continue
		}
		if strings.HasSuffix(idx[0], " truncated") {
			st.Truncated++
		}
		n := 0
		for k, vs := range doc.Meta {
			if strings.HasPrefix(k, "sym") {
				for _, v := range vs {
					n += len(v)
				}
			}
		}
		sizes = append(sizes, n)
		st.TotalBytes += n
		if n > 64<<10 {
			st.Over64KiB++
		}
	}
	if len(sizes) == 0 {
		return st, sc.Err()
	}
	sort.Ints(sizes)
	st.Documents = len(sizes)
	st.MedianBytes = sizes[len(sizes)/2]
	st.P99Bytes = sizes[min(len(sizes)-1, len(sizes)*99/100)]
	st.MaxBytes = sizes[len(sizes)-1]
	return st, sc.Err()
}
