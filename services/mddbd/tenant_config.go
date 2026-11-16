package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"mddb/internal/envconf"
	"mddb/internal/fairq"
)

// Tenant configuration (GO-054): what a tenant as a whole may use, and how
// many requests it may make. Per-collection quotas (GO-043) cap one
// collection; a tenant quota caps the sum over every collection of the
// tenant's namespace, so a tenant cannot pass a limit by creating another
// collection. A tenant without a configuration has no limits of its own and
// behaves exactly as before; only a global admin may set one.

// TenantQuota caps what a tenant's collections may use together. A zero field
// is no limit.
type TenantQuota struct {
	// MaxDocuments is the documents of all the tenant's collections together.
	MaxDocuments int64 `json:"maxDocuments,omitempty"`
	// MaxBytes is their stored size, counted as CollectionQuota.MaxBytes is.
	MaxBytes int64 `json:"maxBytes,omitempty"`
	// MaxVectors is their chunk vectors.
	MaxVectors int64 `json:"maxVectors,omitempty"`
	// MaxCollections is how many collections may hold documents. A document
	// for a further, empty collection is refused.
	MaxCollections int64 `json:"maxCollections,omitempty"`
	// EmbeddingCallsPerDay and EmbeddingTokensPerDay are the tenant's
	// embedding budget per UTC day, spent by all its collections.
	EmbeddingCallsPerDay  int64 `json:"embeddingCallsPerDay,omitempty"`
	EmbeddingTokensPerDay int64 `json:"embeddingTokensPerDay,omitempty"`
	// VectorRAMShare is the share (0-1) of MDDB_VECTOR_RAM_LIMIT_BYTES the
	// tenant's in-memory vectors may take; 0 uses
	// MDDB_TENANT_VECTOR_RAM_SHARE. It does nothing without the server limit.
	VectorRAMShare float64 `json:"vectorRamShare,omitempty"`
}

// TenantLimits caps the rate and concurrency of a tenant's requests. A zero
// field takes the server default from the environment; -1 lifts the default
// for this tenant.
type TenantLimits struct {
	// ReadPerSecond and WritePerSecond are the tenant's requests per second
	// over every credential and surface, reads (and searches) and writes
	// counted apart.
	ReadPerSecond  int `json:"readPerSecond,omitempty"`
	WritePerSecond int `json:"writePerSecond,omitempty"`
	// KeyReadPerSecond and KeyWritePerSecond are the same for each credential
	// of the tenant: an API key, or a user signed in with a password.
	KeyReadPerSecond  int `json:"keyReadPerSecond,omitempty"`
	KeyWritePerSecond int `json:"keyWritePerSecond,omitempty"`
	// MaxConcurrentSearches is how many of the server's search slots
	// (MDDB_SEARCH_MAX_CONCURRENT) the tenant may hold at once.
	MaxConcurrentSearches int `json:"maxConcurrentSearches,omitempty"`
	// MaxConcurrentWrites is how many write requests may run at once.
	MaxConcurrentWrites int `json:"maxConcurrentWrites,omitempty"`
	// MaxConcurrentReindexes is how many vector reindexes may run at once.
	MaxConcurrentReindexes int `json:"maxConcurrentReindexes,omitempty"`
}

// TenantConfig is what is stored for one tenant.
type TenantConfig struct {
	Quota  *TenantQuota  `json:"quota,omitempty"`
	Limits *TenantLimits `json:"limits,omitempty"`
}

// errTenantAdminOnly refuses a tenant configuration change by anyone but a
// global admin.
var errTenantAdminOnly = errors.New("forbidden: only a global admin may change a tenant's quota or limits")

// TenantOfCollection is the tenant whose namespace holds collection, or "".
func TenantOfCollection(collection string) string { return fairq.TenantOf(collection) }

// Validate refuses negative quota fields and a share outside 0-1.
func (q *TenantQuota) Validate() error {
	if q == nil {
		return nil
	}
	for name, v := range map[string]int64{
		"maxDocuments": q.MaxDocuments, "maxBytes": q.MaxBytes, "maxVectors": q.MaxVectors,
		"maxCollections": q.MaxCollections, "embeddingCallsPerDay": q.EmbeddingCallsPerDay,
		"embeddingTokensPerDay": q.EmbeddingTokensPerDay,
	} {
		if v < 0 {
			return fmt.Errorf("invalid quota.%s: must be >= 0 (0 = unlimited)", name)
		}
	}
	if q.VectorRAMShare < 0 || q.VectorRAMShare > 1 {
		return errors.New("invalid quota.vectorRamShare: must be between 0 and 1")
	}
	return nil
}

// limitsStorage reports whether a document write is checked against q.
func (q *TenantQuota) limitsStorage() bool {
	return q != nil && (q.MaxDocuments > 0 || q.MaxBytes > 0 || q.MaxVectors > 0 || q.MaxCollections > 0)
}

// limitsEmbedding reports whether the tenant has an embedding budget.
func (q *TenantQuota) limitsEmbedding() bool {
	return q != nil && (q.EmbeddingCallsPerDay > 0 || q.EmbeddingTokensPerDay > 0)
}

// Validate refuses limits below -1.
func (l *TenantLimits) Validate() error {
	if l == nil {
		return nil
	}
	for name, v := range map[string]int{
		"readPerSecond": l.ReadPerSecond, "writePerSecond": l.WritePerSecond,
		"keyReadPerSecond": l.KeyReadPerSecond, "keyWritePerSecond": l.KeyWritePerSecond,
		"maxConcurrentSearches": l.MaxConcurrentSearches, "maxConcurrentWrites": l.MaxConcurrentWrites,
		"maxConcurrentReindexes": l.MaxConcurrentReindexes,
	} {
		if v < -1 {
			return fmt.Errorf("invalid limits.%s: must be >= -1 (0 = server default, -1 = unlimited)", name)
		}
	}
	return nil
}

// Validate checks both parts.
func (c *TenantConfig) Validate() error {
	if err := c.Quota.Validate(); err != nil {
		return err
	}
	return c.Limits.Validate()
}

// tenantDefaults are the limits a tenant without an override gets, read from
// the environment once. Zero everywhere — the default — limits nothing.
type tenantDefaults struct {
	limits   TenantLimits
	ramShare float64
}

// readTenantDefaults reads MDDB_TENANT_* and MDDB_KEY_*.
func readTenantDefaults() tenantDefaults {
	share, err := strconv.ParseFloat(strings.TrimSpace(envconf.String("MDDB_TENANT_VECTOR_RAM_SHARE", "0")), 64)
	if err != nil || share < 0 || share > 1 {
		share = 0
	}
	return tenantDefaults{
		ramShare: share,
		limits: TenantLimits{
			ReadPerSecond:          envconf.Int("MDDB_TENANT_READ_RPS", 0),
			WritePerSecond:         envconf.Int("MDDB_TENANT_WRITE_RPS", 0),
			KeyReadPerSecond:       envconf.Int("MDDB_KEY_READ_RPS", 0),
			KeyWritePerSecond:      envconf.Int("MDDB_KEY_WRITE_RPS", 0),
			MaxConcurrentSearches:  envconf.Int("MDDB_TENANT_MAX_CONCURRENT_SEARCHES", 0),
			MaxConcurrentWrites:    envconf.Int("MDDB_TENANT_MAX_CONCURRENT_WRITES", 0),
			MaxConcurrentReindexes: envconf.Int("MDDB_TENANT_MAX_CONCURRENT_REINDEXES", 0),
		},
	}
}

// pick is a tenant's own value when it set one, else the default; -1 and
// anything not positive is no limit (0).
func pick(own, def int) int {
	v := def
	if own != 0 {
		v = own
	}
	return max(v, 0)
}

// effective merges a tenant's override over the defaults: the limits that
// apply to it, 0 meaning none.
func (d tenantDefaults) effective(own *TenantLimits) TenantLimits {
	if own == nil {
		own = &TenantLimits{}
	}
	return TenantLimits{
		ReadPerSecond:          pick(own.ReadPerSecond, d.limits.ReadPerSecond),
		WritePerSecond:         pick(own.WritePerSecond, d.limits.WritePerSecond),
		KeyReadPerSecond:       pick(own.KeyReadPerSecond, d.limits.KeyReadPerSecond),
		KeyWritePerSecond:      pick(own.KeyWritePerSecond, d.limits.KeyWritePerSecond),
		MaxConcurrentSearches:  pick(own.MaxConcurrentSearches, d.limits.MaxConcurrentSearches),
		MaxConcurrentWrites:    pick(own.MaxConcurrentWrites, d.limits.MaxConcurrentWrites),
		MaxConcurrentReindexes: pick(own.MaxConcurrentReindexes, d.limits.MaxConcurrentReindexes),
	}
}

// ramShare is the tenant's share of the vector RAM limit, 0 for none.
func (d tenantDefaults) ramShareFor(q *TenantQuota) float64 {
	if q != nil && q.VectorRAMShare > 0 {
		return q.VectorRAMShare
	}
	return d.ramShare
}

// tenantConfig is the stored configuration of a tenant, zero when it has none.
func (s *Server) tenantConfig(tenant string) TenantConfig {
	if tenant == "" || s.Tenants == nil {
		return TenantConfig{}
	}
	cfg, _ := s.Tenants.Get(tenant)
	return cfg
}

// tenantQuotaOf is the tenant's quota and the collection's tenant, nil when
// the collection is outside a namespace or its tenant has no quota.
func (s *Server) tenantQuotaOf(collection string) (string, *TenantQuota) {
	tenant := TenantOfCollection(collection)
	return tenant, s.tenantConfig(tenant).Quota
}
