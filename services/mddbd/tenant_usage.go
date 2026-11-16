package main

// A tenant's usage report (GO-054): GET /v1/tenants/{id}/usage, the panel's
// tenant view and the mddb_tenant_* gauges read it.

// TenantUsage is what a tenant's collections hold and spent today.
type TenantUsage struct {
	Documents   int64 `json:"documents"`
	Bytes       int64 `json:"bytes"`
	Vectors     int64 `json:"vectors"`
	Collections int64 `json:"collections"`
	// VectorRAMBytes estimates the in-memory vector index the tenant takes:
	// its share of the vectors times the index's measured size.
	VectorRAMBytes int64 `json:"vectorRamBytes"`
	// EmbeddingCalls and EmbeddingTokens are today's (UTC) spend, counted
	// only while the tenant has an embedding budget.
	EmbeddingCalls  int64 `json:"embeddingCalls"`
	EmbeddingTokens int64 `json:"embeddingTokens"`

	limits usageLimits // the limit of each resource, 0 for none
}

// usageLimits is the limit each usage figure is compared with.
type usageLimits struct {
	docs, bytes, vectors, collections, ram, calls, tokens int64
}

// usageResource is one resource, its use and its limit (0 = none).
type usageResource struct {
	name        string
	used, limit int64
}

// resources lists every resource with its limit, in a fixed order.
func (u TenantUsage) resources() []usageResource {
	return []usageResource{
		{quotaDocuments, u.Documents, u.limits.docs},
		{quotaBytes, u.Bytes, u.limits.bytes},
		{quotaVectors, u.Vectors, u.limits.vectors},
		{quotaCollections, u.Collections, u.limits.collections},
		{quotaVectorRAM, u.VectorRAMBytes, u.limits.ram},
		{quotaEmbeddingCalls, u.EmbeddingCalls, u.limits.calls},
		{quotaEmbeddingTokens, u.EmbeddingTokens, u.limits.tokens},
	}
}

// TenantQueueReport is a tenant's place in one background queue.
type TenantQueueReport struct {
	Depth int `json:"depth"`
	// Taken is the jobs workers took off the queue since start, with their
	// total and longest wait.
	Taken queueStat `json:"taken"`
}

// TenantUsageReport is everything the server knows about one tenant.
type TenantUsageReport struct {
	Tenant string `json:"tenant"`
	// Quota and Limits are the stored configuration, absent when not set.
	Quota  *TenantQuota  `json:"quota,omitempty"`
	Limits *TenantLimits `json:"limits,omitempty"`
	// EffectiveLimits are the limits that apply: the stored ones over the
	// server defaults, 0 meaning none.
	EffectiveLimits TenantLimits `json:"effectiveLimits"`
	// VectorRAMLimitBytes is the tenant's share of the server's vector RAM
	// limit, 0 for none.
	VectorRAMLimitBytes int64                        `json:"vectorRamLimitBytes"`
	Usage               TenantUsage                  `json:"usage"`
	CollectionNames     []string                     `json:"collectionNames"`
	Queues              map[string]TenantQueueReport `json:"queues"`
	// InFlight is the tenant's searches, writes and reindexes holding a slot
	// now (counted only while the matching limit is set).
	InFlight map[string]int `json:"inFlight"`
	// Refusals counts the tenant's refused requests and writes since start,
	// per reason: tenant.readRate, key.writeRate, tenant.searchConcurrency,
	// quota.documents, ...
	Refusals map[string]uint64 `json:"refusals"`
}

// tenantUsageReport reads one tenant's configuration, use and queues.
func (s *Server) tenantUsageReport(tenant string) TenantUsageReport {
	cfg := s.tenantConfig(tenant)
	rep := TenantUsageReport{
		Tenant: tenant, Quota: cfg.Quota, Limits: cfg.Limits,
		EffectiveLimits: s.effectiveLimits(tenant),
		CollectionNames: s.tenantCollections(nil, tenant),
		Queues:          map[string]TenantQueueReport{},
		Refusals:        s.tenantRefusals(tenant),
	}
	rep.Usage = s.measureTenant(tenant, rep.CollectionNames)
	if limit := s.quotas().vectorRAMLimit; limit > 0 {
		if share := s.tenantDefaults().ramShareFor(cfg.Quota); share > 0 {
			rep.VectorRAMLimitBytes = int64(share * float64(limit))
		}
	}
	rep.Usage.limits = quotaLimits(cfg.Quota, rep.VectorRAMLimitBytes)
	depths := s.tenantQueueDepths()[tenant]
	stats := s.tenantQueueStats(tenant)
	for _, q := range []string{queueIndex, queueEmbedding} {
		rep.Queues[q] = TenantQueueReport{Depth: depths[q], Taken: stats[q]}
	}
	rep.Queues[queueParked] = TenantQueueReport{Depth: depths[queueParked]}
	rt := s.tenantLimiter()
	rep.InFlight = map[string]int{
		"searches": rt.inUse("s|" + tenant), "writes": rt.inUse("w|" + tenant), "reindexes": rt.inUse("r|" + tenant),
	}
	return rep
}

// measureTenant counts what the tenant's collections hold and spent today.
func (s *Server) measureTenant(tenant string, collections []string) TenantUsage {
	var u TenantUsage
	for _, c := range collections {
		cu := s.collectionUsage(c)
		u.Documents += cu.docs
		u.Bytes += cu.bytes
		u.Vectors += s.collectionVectorCount(c)
		if cu.docs > 0 || s.collectionConfigured(c) {
			u.Collections++
		}
	}
	if s.VectorIndex != nil {
		u.VectorRAMBytes = s.tenantVectorRAM(tenant, s.VectorIndex.MemoryBytes())
	}
	spent := s.quotas().budget.today(tenantBudgetKey(tenant))
	u.EmbeddingCalls, u.EmbeddingTokens = spent.calls, spent.tokens
	return u
}

// quotaLimits is the limit of each resource under quota q.
func quotaLimits(q *TenantQuota, ramLimit int64) usageLimits {
	l := usageLimits{ram: ramLimit}
	if q != nil {
		l.docs, l.bytes, l.vectors, l.collections = q.MaxDocuments, q.MaxBytes, q.MaxVectors, q.MaxCollections
		l.calls, l.tokens = q.EmbeddingCallsPerDay, q.EmbeddingTokensPerDay
	}
	return l
}
