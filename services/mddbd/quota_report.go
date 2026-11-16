package main

import (
	"log/slog"
	"sort"
	"time"

	"mddb/internal/metrics"
	"mddb/internal/webhooks"
)

// Reporting quota refusals and quota use (GO-043): the log line, the
// quota.exceeded webhook and the /metrics gauges.

// quotaReportInterval is how often one collection's refusals of one kind are
// logged and sent as a webhook. An agent writing in a loop into a full
// collection is refused on every call; its operator needs one message, not
// thousands. A variable so tests need not wait.
var quotaReportInterval = time.Minute

// reportQuota counts a refusal, and logs it and fires quota.exceeded at most
// once per quotaReportInterval for each collection and resource. It returns
// qe, so a caller can refuse and report in one statement.
func (s *Server) reportQuota(qe *QuotaError) error {
	s.Metrics.IncOp("quota_exceeded", qe.Resource)
	if tenant := quotaTenant(qe); tenant != "" { // GO-054
		s.countTenantRefusal(tenant, "quota."+qe.Resource)
	}
	qs := s.quotas()
	key := qe.Tenant + "|" + qe.Collection + "|" + qe.Resource
	now := time.Now().UnixNano()
	qs.mu.Lock()
	last, seen := qs.reported[key]
	due := !seen || time.Duration(now-last) >= quotaReportInterval
	if due {
		qs.reported[key] = now
	}
	qs.mu.Unlock()
	if !due {
		return qe
	}
	slog.Warn("quota exceeded", "tenant", qe.Tenant, "collection", qe.Collection, "resource", qe.Resource,
		"used", qe.Used, "limit", qe.Limit, "retryAfter", qe.RetryAfter)
	if s.WebhookManager != nil {
		detail := qe.detail()
		delete(detail, "status")
		s.WebhookManager.FireCollectionEvent(webhooks.EventQuotaExceeded, qe.Collection, detail)
	}
	return qe
}

// quotaTenant is the tenant a refusal counts against: the tenant whose limit
// it is, or the tenant of the refused collection.
func quotaTenant(qe *QuotaError) string {
	if qe.Tenant != "" {
		return qe.Tenant
	}
	return TenantOfCollection(qe.Collection)
}

// quotaGauges is every quota's use for /metrics: the fullness of each
// storage limit, and the share of today's embedding budget spent. Values are
// above 1 when a limit was lowered below what the collection already holds.
func (s *Server) quotaGauges() ([]metrics.FullnessGauge, map[string]float64) {
	if s.CollectionManager == nil {
		return nil, nil
	}
	configs := s.CollectionManager.ListAll()
	names := make([]string, 0, len(configs))
	for name := range configs {
		names = append(names, name)
	}
	sort.Strings(names)

	var fullness []metrics.FullnessGauge
	budget := map[string]float64{}
	for _, name := range names {
		q := configs[name].Quota
		if q.limitsStorage() {
			u := s.collectionUsage(name)
			add := func(resource string, used, limit int64) {
				if limit > 0 {
					fullness = append(fullness, metrics.FullnessGauge{Collection: name, Resource: resource,
						Value: float64(used) / float64(limit)})
				}
			}
			add(quotaDocuments, u.docs, q.MaxDocuments)
			add(quotaBytes, u.bytes, q.MaxBytes)
			add(quotaVectors, s.collectionVectorCount(name), q.MaxVectors)
		}
		if q.limitsEmbedding() {
			budget[name] = s.quotas().budget.usedFraction(name, q)
		}
	}
	return fullness, budget
}
