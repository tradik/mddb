package metrics

import (
	"strings"
	"testing"
)

func TestTenantGaugesAndCounters(t *testing.T) {
	var buf strings.Builder
	writeTenantGauges(&buf, DBStats{})
	if buf.Len() != 0 {
		t.Fatalf("a server without tenants wrote %q", buf.String())
	}
	writeTenantGauges(&buf, DBStats{Tenants: TenantGauges{
		Usage:      []TenantGauge{{Tenant: "acme", Label: "documents", Value: 3}},
		Fullness:   []TenantGauge{{Tenant: "acme", Label: "documents", Value: 0.5}},
		QueueDepth: []TenantGauge{{Tenant: "acme", Label: "index", Value: 2}},
	}})
	out := buf.String()
	for _, want := range []string{
		`mddb_tenant_usage{tenant="acme",resource="documents"} 3`,
		`mddb_tenant_fullness{tenant="acme",resource="documents"} 0.5`,
		`mddb_tenant_queue_depth{tenant="acme",queue="index"} 2`,
		"# TYPE mddb_tenant_usage gauge",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}

	var nilM *Metrics
	nilM.AddCounter("x", "h", "", 1) // a nil receiver adds nothing
	m := &Metrics{}
	m.AddCounter("mddb_tenant_refusals_total", "Refusals.", `tenant="acme",reason="tenant.readRate"`, 1)
	m.AddCounter("mddb_tenant_refusals_total", "Refusals.", `tenant="acme",reason="tenant.readRate"`, 2)
	var cb strings.Builder
	m.writeGauges(&cb)
	if !strings.Contains(cb.String(), `mddb_tenant_refusals_total{tenant="acme",reason="tenant.readRate"} 3`) ||
		!strings.Contains(cb.String(), "# TYPE mddb_tenant_refusals_total counter") {
		t.Fatalf("counters = %q", cb.String())
	}
}
