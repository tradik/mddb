package main

import (
	"fmt"
	"sort"
	"time"

	"mddb/internal/metrics"
)

// What the server reports per tenant (GO-054): refusals and queue waits as
// they happen (mddb_tenant_refusals_total, mddb_tenant_queue_wait_seconds_total,
// mddb_tenant_queue_jobs_total), and use and queue depth at scrape time
// (mddb_tenant_usage, mddb_tenant_fullness, mddb_tenant_queue_depth).
// Collections outside any namespace report under tenant "".

// Background queues, as named in metrics and the usage report.
const (
	queueIndex     = "index"
	queueEmbedding = "embedding"
	queueParked    = "embeddingParked"
)

// queueStat is a tenant's jobs taken off one queue and their total wait.
type queueStat struct {
	Jobs        uint64  `json:"jobs"`
	WaitSeconds float64 `json:"waitSecondsTotal"`
	MaxWait     float64 `json:"maxWaitSeconds"`
}

// tenantLabels renders the labels of a per-tenant series.
func tenantLabels(tenant, name, value string) string {
	return fmt.Sprintf("tenant=%q,%s=%q", tenant, name, value)
}

// countTenantRefusal counts one refusal of the tenant's requests or writes.
func (s *Server) countTenantRefusal(tenant, reason string) {
	rt := s.tenantLimiter()
	rt.mu.Lock()
	if rt.refusals[tenant] == nil {
		rt.refusals[tenant] = map[string]uint64{}
	}
	rt.refusals[tenant][reason]++
	rt.mu.Unlock()
	s.Metrics.AddCounter("mddb_tenant_refusals_total",
		"Requests and writes of a tenant refused by a tenant quota or limit, per reason.",
		tenantLabels(tenant, "reason", reason), 1)
}

// observeQueueWait records a job a worker took off a queue, and its wait.
func (s *Server) observeQueueWait(queue, tenant string, waited time.Duration) {
	rt := s.tenantLimiter()
	rt.mu.Lock()
	st := rt.queues[tenant+"|"+queue]
	if st == nil {
		st = &queueStat{}
		rt.queues[tenant+"|"+queue] = st
	}
	st.Jobs++
	st.WaitSeconds += waited.Seconds()
	st.MaxWait = max(st.MaxWait, waited.Seconds())
	rt.mu.Unlock()
	labels := tenantLabels(tenant, "queue", queue)
	s.Metrics.AddCounter("mddb_tenant_queue_wait_seconds_total",
		"Time a tenant's background jobs waited in a queue before a worker took them.", labels, waited.Seconds())
	s.Metrics.AddCounter("mddb_tenant_queue_jobs_total",
		"Background jobs of a tenant taken off a queue by a worker.", labels, 1)
}

// queueObserver is what a queue's workers call for each job they take.
func (s *Server) queueObserver(queue string) func(tenant string, waited time.Duration) {
	return func(tenant string, waited time.Duration) { s.observeQueueWait(queue, tenant, waited) }
}

// tenantQueueStats is the tenant's wait totals per queue.
func (s *Server) tenantQueueStats(tenant string) map[string]queueStat {
	rt := s.tenantLimiter()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	out := map[string]queueStat{}
	for _, q := range []string{queueIndex, queueEmbedding} {
		if st := rt.queues[tenant+"|"+q]; st != nil {
			out[q] = *st
		}
	}
	return out
}

// tenantRefusals is the tenant's refusals per reason.
func (s *Server) tenantRefusals(tenant string) map[string]uint64 {
	rt := s.tenantLimiter()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	out := map[string]uint64{}
	for k, v := range rt.refusals[tenant] {
		out[k] = v
	}
	return out
}

// tenantQueueDepths is each tenant's waiting jobs per queue.
func (s *Server) tenantQueueDepths() map[string]map[string]int {
	out := map[string]map[string]int{}
	add := func(queue string, depths map[string]int) {
		for tenant, n := range depths {
			if out[tenant] == nil {
				out[tenant] = map[string]int{}
			}
			out[tenant][queue] = n
		}
	}
	if s.IndexQueue != nil {
		add(queueIndex, s.IndexQueue.TenantDepths())
	}
	if s.EmbeddingWorker != nil {
		add(queueEmbedding, s.EmbeddingWorker.TenantDepths())
		add(queueParked, s.EmbeddingWorker.ParkedDepths())
	}
	return out
}

// knownTenants is every tenant the server knows: configured, or assigned to
// a user.
func (s *Server) knownTenants() []string {
	seen := map[string]bool{}
	for _, t := range s.Tenants.Names() {
		seen[t] = true
	}
	if s.AuthManager != nil {
		for _, t := range s.AuthManager.TenantNames() {
			seen[t] = true
		}
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// tenantGauges is every known tenant's use and queue depth for /metrics.
func (s *Server) tenantGauges() metrics.TenantGauges {
	var g metrics.TenantGauges
	depths := s.tenantQueueDepths()
	for _, tenant := range s.knownTenants() {
		rep := s.tenantUsageReport(tenant)
		for _, r := range rep.Usage.resources() {
			g.Usage = append(g.Usage, metrics.TenantGauge{Tenant: tenant, Label: r.name, Value: float64(r.used)})
			if r.limit > 0 {
				g.Fullness = append(g.Fullness, metrics.TenantGauge{Tenant: tenant, Label: r.name, Value: float64(r.used) / float64(r.limit)})
			}
		}
	}
	tenants := make([]string, 0, len(depths))
	for t := range depths {
		tenants = append(tenants, t)
	}
	sort.Strings(tenants)
	for _, t := range tenants {
		for _, q := range []string{queueIndex, queueEmbedding, queueParked} {
			if n, ok := depths[t][q]; ok {
				g.QueueDepth = append(g.QueueDepth, metrics.TenantGauge{Tenant: t, Label: q, Value: float64(n)})
			}
		}
	}
	return g
}
