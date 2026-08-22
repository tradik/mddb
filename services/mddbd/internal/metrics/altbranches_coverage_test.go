package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// altStats returns the opposite of stubStats so HandleMetrics renders the
// "0"/"follower"/"default"/absent branches.
type altStats struct{ role string }

func (altStats) Mode() string                    { return "ro" }
func (altStats) VectorIndexReady() bool          { return false }
func (altStats) EmbeddingConfigured() bool       { return false }
func (altStats) EmbeddingQueueSize() (int, bool) { return 0, false }

// Caching disabled: the metric must be absent, not a zero that reads as
// "the cache never hits".
func (altStats) EmbeddingCacheStats() (uint64, uint64, int, bool) { return 0, 0, 0, false }
func (s altStats) ReplicationRole() string                        { return s.role }
func (altStats) BinlogStats() (BinlogStatsView, bool)             { return BinlogStatsView{}, false }
func (altStats) DBStats() DBStats                                 { return DBStats{} }

func TestHandleMetricsAltBranches(t *testing.T) {
	for _, role := range []string{"follower", "standalone"} { // follower -> 2, other -> default 0
		m := NewMetrics(true, altStats{role: role})
		m.IncOp("solo") // single label -> the no-label operations branch
		rec := httptest.NewRecorder()
		m.HandleMetrics(rec, httptest.NewRequest("GET", "/metrics", nil))
		body := rec.Body.String()
		if !strings.Contains(body, "mddb_vector_index_ready 0") {
			t.Error("want mddb_vector_index_ready 0")
		}
		if !strings.Contains(body, "mddb_embedding_provider_configured 0") {
			t.Error("want mddb_embedding_provider_configured 0")
		}
		if !strings.Contains(body, `mddb_operations_total{operation="solo"}`) {
			t.Error("want single-label operations line")
		}
	}
}
