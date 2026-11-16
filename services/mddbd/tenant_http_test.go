package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	json "mddb/internal/jsonx"
	"mddb/internal/metrics"
)

// GO-054: the tenant endpoints and what the server reports per tenant.

func TestTenantEndpoints(t *testing.T) {
	s := limitServer(t)
	if err := s.AuthManager.SetPermission(&Permission{Username: "tina", Collection: "*", Read: true, Write: true}); err != nil {
		t.Fatal(err)
	}
	addTestDoc(t, s, "t1/notes", "k", "en", "body", nil)

	// Only a global admin sets a tenant's quota and limits.
	put := `{"quota":{"maxDocuments":10},"limits":{"readPerSecond":5}}`
	if rec := restRec(t, s, tina, http.MethodPut, "/v1/tenants/t1", put); rec.Code != http.StatusForbidden {
		t.Fatalf("tenant user set its own quota: %d", rec.Code)
	}
	rec := restRec(t, s, root, http.MethodPut, "/v1/tenants/t1", put)
	var view tenantConfigView
	_ = json.Unmarshal(rec.Body.Bytes(), &view)
	if rec.Code != http.StatusOK || view.Quota.MaxDocuments != 10 || view.EffectiveLimits.ReadPerSecond != 5 {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body.String())
	}
	// A part left out is kept; null removes it.
	rec = restRec(t, s, root, http.MethodPut, "/v1/tenants/t1", `{"limits":null}`)
	var partial tenantConfigView
	_ = json.Unmarshal(rec.Body.Bytes(), &partial)
	if partial.Quota == nil || partial.Limits != nil {
		t.Fatalf("partial PUT: %s", rec.Body.String())
	}
	for _, body := range []string{`nope`, `{"quota":{"maxDocuments":-1}}`, `{"quota":"x"}`, `{"limits":"x"}`} {
		if rec := restRec(t, s, root, http.MethodPut, "/v1/tenants/t1", body); rec.Code != http.StatusBadRequest {
			t.Errorf("PUT %s: %d", body, rec.Code)
		}
	}

	// The tenant's own admin reads its configuration and usage; another tenant does not.
	if rec := restRec(t, s, tina, http.MethodGet, "/v1/tenants/t1", ""); rec.Code != http.StatusOK {
		t.Fatalf("own config: %d", rec.Code)
	}
	rec = restRec(t, s, tina, http.MethodGet, "/v1/tenants/t1/usage", "")
	var rep TenantUsageReport
	_ = json.Unmarshal(rec.Body.Bytes(), &rep)
	if rec.Code != http.StatusOK || rep.Usage.Documents != 1 || rep.Quota.MaxDocuments != 10 || len(rep.CollectionNames) != 1 {
		t.Fatalf("usage: %d %s", rec.Code, rec.Body.String())
	}
	if rec := restRec(t, s, nobby, http.MethodGet, "/v1/tenants/t1/usage", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("another tenant read t1's usage: %d", rec.Code)
	}
	noCreds := httptest.NewRecorder()
	s.handleTenants(noCreds, httptest.NewRequest(http.MethodGet, "/v1/tenants/t1/usage", nil))
	if noCreds.Code != http.StatusUnauthorized {
		t.Fatalf("no credentials: %d", noCreds.Code)
	}

	// The list is a global admin's.
	if rec := restRec(t, s, tina, http.MethodGet, "/v1/tenants", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("tenant listed every tenant: %d", rec.Code)
	}
	rec = restRec(t, s, root, http.MethodGet, "/v1/tenants", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"tenant":"t2"`) || !strings.Contains(rec.Body.String(), `"total":2`) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}

	// Odd requests.
	for _, c := range []struct {
		method, path string
		want         int
	}{
		{http.MethodPost, "/v1/tenants", http.StatusMethodNotAllowed},
		{http.MethodGet, "/v1/tenants/bad.name", http.StatusBadRequest},
		{http.MethodGet, "/v1/tenants/t1/other", http.StatusNotFound},
		{http.MethodPost, "/v1/tenants/t1", http.StatusMethodNotAllowed},
		{http.MethodDelete, "/v1/tenants/t1", http.StatusForbidden}, // as tina below
	} {
		who := root
		if c.method == http.MethodDelete {
			who = tina
		}
		if rec := restRec(t, s, who, c.method, c.path, ""); rec.Code != c.want {
			t.Errorf("%s %s: %d, want %d", c.method, c.path, rec.Code, c.want)
		}
	}
	if rec := restRec(t, s, root, http.MethodDelete, "/v1/tenants/t1", ""); rec.Code != http.StatusOK {
		t.Fatalf("DELETE: %d", rec.Code)
	}
	if _, ok := s.Tenants.Get("t1"); ok {
		t.Fatal("still configured after DELETE")
	}
}

func TestTenantEndpointsWithAuthOff(t *testing.T) {
	_, s := tenantQuotaServer(t)
	rec := httptest.NewRecorder()
	s.handleTenants(rec, httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/usage", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"documents":4`) {
		t.Fatalf("usage with auth off: %d %s", rec.Code, rec.Body.String())
	}
	// A store that cannot be written answers the error.
	_ = s.DB.Close()
	for _, m := range []string{http.MethodPut, http.MethodDelete} {
		rec := httptest.NewRecorder()
		s.handleTenants(rec, httptest.NewRequest(m, "/v1/tenants/acme", strings.NewReader(`{"quota":{"maxDocuments":1}}`)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s on a closed store: %d", m, rec.Code)
		}
	}
}

func TestTenantMetricsAndReport(t *testing.T) {
	t.Setenv("MDDB_VECTOR_RAM_LIMIT_BYTES", "1000")
	_, s := tenantQuotaServer(t)
	s.Metrics = metrics.NewMetrics(true, &serverMetricsStats{s: s})
	setTenantQuota(t, s, "acme", TenantQuota{MaxDocuments: 8, VectorRAMShare: 0.5, EmbeddingCallsPerDay: 10})
	_ = s.VectorIndex.Add("acme/a", "one#0", []float32{1, 0, 0})
	s.observeQueueWait(queueIndex, "acme", 2*time.Second)
	s.observeQueueWait(queueIndex, "acme", time.Second)
	s.countTenantRefusal("acme", "tenant.readRate")
	t.Setenv("MDDB_EMBEDDING_QUEUE_WAIT", "0")
	s.EmbeddingWorker = NewEmbeddingWorker(&mockEmbedding{dims: 3, model: "m"}, s.VectorStore, s.VectorIndex, 1)
	s.EmbeddingWorker.Enqueue(EmbeddingJob{Collection: "acme/a", DocID: "q"})
	s.EmbeddingWorker.Enqueue(EmbeddingJob{Collection: "acme/a", DocID: "p"}) // parked: no room
	defer s.EmbeddingWorker.Stop()

	rep := s.tenantUsageReport("acme")
	idx := rep.Queues[queueIndex].Taken
	if idx.Jobs != 2 || idx.WaitSeconds != 3 || idx.MaxWait != 2 {
		t.Fatalf("index queue = %+v", idx)
	}
	if rep.Queues[queueEmbedding].Depth != 1 || rep.Queues[queueParked].Depth != 1 {
		t.Fatalf("queues = %+v", rep.Queues)
	}
	if rep.VectorRAMLimitBytes != 500 || rep.Usage.Vectors != 1 || rep.Refusals["tenant.readRate"] != 1 {
		t.Fatalf("report = %+v", rep)
	}
	if s.tenantRefusals("nobody") == nil || len(s.tenantQueueStats("nobody")) != 0 {
		t.Fatal("a tenant without history")
	}

	g := s.tenantGauges()
	if len(g.Usage) != 7 || len(g.Fullness) != 3 || len(g.QueueDepth) != 2 {
		t.Fatalf("gauges = %+v", g)
	}
	rec := httptest.NewRecorder()
	s.Metrics.HandleMetrics(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, want := range []string{
		`mddb_tenant_usage{tenant="acme",resource="documents"} 4`,
		`mddb_tenant_fullness{tenant="acme",resource="documents"} 0.5`,
		`mddb_tenant_queue_depth{tenant="acme",queue="embedding"} 1`,
		`mddb_tenant_refusals_total{tenant="acme",reason="tenant.readRate"} 1`,
		`mddb_tenant_queue_wait_seconds_total{tenant="acme",queue="index"} 3`,
		`mddb_tenant_queue_jobs_total{tenant="acme",queue="index"} 2`,
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("/metrics lacks %s", want)
		}
	}
}

func TestKnownTenantsIncludesUsers(t *testing.T) {
	s := limitServer(t)
	_ = s.Tenants.Set("configured", TenantConfig{Quota: &TenantQuota{MaxDocuments: 1}})
	got := strings.Join(s.knownTenants(), ",")
	if got != "configured,t1,t2" {
		t.Fatalf("tenants = %s", got)
	}
	s.AuthManager = nil
	if strings.Join(s.knownTenants(), ",") != "configured" {
		t.Fatal("without auth only configured tenants")
	}
}
