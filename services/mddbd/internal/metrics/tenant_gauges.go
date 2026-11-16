package metrics

import "strings"

// Per-tenant gauges (GO-054), read from the server at scrape time like the
// quota gauges. Each family is absent on a server without tenants rather
// than a page of zeros.

// TenantGauge is one series: a tenant and the value of one label.
type TenantGauge struct {
	Tenant string
	Label  string // the resource, or the queue
	Value  float64
}

// TenantGauges is every per-tenant gauge of a scrape.
type TenantGauges struct {
	// Usage is what each tenant's collections hold (documents, bytes,
	// vectors, collections, vectorRamBytes, embeddingCalls, embeddingTokens).
	Usage []TenantGauge
	// Fullness is the share of each tenant quota in use (0-1; above 1 when a
	// limit was lowered below current use).
	Fullness []TenantGauge
	// QueueDepth is the jobs each tenant has waiting per background queue
	// (index, embedding, embeddingParked).
	QueueDepth []TenantGauge
}

// writeTenantGauges writes the per-tenant gauge families.
func writeTenantGauges(buf *strings.Builder, ds DBStats) {
	writeTenantFamily(buf, "mddb_tenant_usage", "What a tenant's collections hold, per resource.", "resource", ds.Tenants.Usage)
	writeTenantFamily(buf, "mddb_tenant_fullness", "Share of a tenant quota in use (0-1; above 1 when a limit was lowered below current use).", "resource", ds.Tenants.Fullness)
	writeTenantFamily(buf, "mddb_tenant_queue_depth", "Jobs a tenant has waiting in a background queue.", "queue", ds.Tenants.QueueDepth)
}

// writeTenantFamily writes one family, nothing when it has no series.
func writeTenantFamily(buf *strings.Builder, name, help, label string, series []TenantGauge) {
	if len(series) == 0 {
		return
	}
	writef(buf, "# HELP %s %s\n", name, help)
	writef(buf, "# TYPE %s gauge\n", name)
	for _, g := range series {
		writef(buf, "%s{tenant=%q,%s=%q} %g\n", name, g.Tenant, label, g.Label, g.Value)
	}
	buf.WriteString("\n")
}
