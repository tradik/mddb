package metrics

import (
	"strings"
	"testing"
)

// GO-043: the quota gauges render only when there is something to report.
func TestWriteQuotaGauges(t *testing.T) {
	var empty strings.Builder
	writeQuotaGauges(&empty, DBStats{})
	if empty.Len() != 0 {
		t.Fatalf("gauges written without quotas: %q", empty.String())
	}

	var buf strings.Builder
	writeQuotaGauges(&buf, DBStats{
		Fullness: []FullnessGauge{
			{Collection: "acme/notes", Resource: "documents", Value: 0.5},
			{Collection: "acme/notes", Resource: "bytes", Value: 1.25},
		},
		EmbeddingBudgetUsed: map[string]float64{"b": 0.75, "a": 0},
	})
	out := buf.String()
	for _, want := range []string{
		"# TYPE mddb_collection_fullness gauge\n",
		`mddb_collection_fullness{collection="acme/notes",resource="documents"} 0.5` + "\n",
		`mddb_collection_fullness{collection="acme/notes",resource="bytes"} 1.25` + "\n",
		"# TYPE mddb_embedding_budget_used gauge\n",
		`mddb_embedding_budget_used{collection="a"} 0` + "\n" + `mddb_embedding_budget_used{collection="b"} 0.75` + "\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}
