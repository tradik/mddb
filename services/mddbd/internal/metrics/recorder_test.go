package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// nonFlusher is a ResponseWriter that cannot flush.
type nonFlusher struct{ http.ResponseWriter }

// The recorder the middleware wraps every response in: Flush on a writer that
// cannot is a no-op, and a ResponseController looks through it (Unwrap).
func TestStatusRecorderWithoutFlusher(t *testing.T) {
	m := NewMetrics(true, nil)
	h := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.(http.Flusher).Flush()
		if http.NewResponseController(w).SetReadDeadline(time.Now()) == nil {
			t.Error("a deadline on a writer that has none")
		}
	}))
	h.ServeHTTP(nonFlusher{httptest.NewRecorder()}, httptest.NewRequest(http.MethodGet, "/x", nil))
}
