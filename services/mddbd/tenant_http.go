package main

import (
	"errors"
	"net/http"
	"strings"

	json "mddb/internal/jsonx"
)

// The tenant endpoints (GO-054):
//
//	GET    /v1/tenants              every known tenant with its usage (global admin)
//	GET    /v1/tenants/{id}         its quota, limits and effective limits
//	PUT    /v1/tenants/{id}         set them (global admin)
//	DELETE /v1/tenants/{id}         remove them (global admin)
//	GET    /v1/tenants/{id}/usage   its usage report
//
// A tenant's own admin — a user of the tenant holding read on every
// collection of it ("*") — may read its configuration and usage, never
// change them: a tenant able to raise its own quota would not have one.

// tenantConfigView is the configuration as the API shows it.
type tenantConfigView struct {
	Tenant          string        `json:"tenant"`
	Quota           *TenantQuota  `json:"quota,omitempty"`
	Limits          *TenantLimits `json:"limits,omitempty"`
	EffectiveLimits TenantLimits  `json:"effectiveLimits"`
}

// handleTenants routes /v1/tenants and /v1/tenants/{id}[/usage].
func (s *Server) handleTenants(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/tenants"), "/")
	if rest == "" {
		s.listTenants(w, r)
		return
	}
	tenant, sub, _ := strings.Cut(rest, "/")
	if !ValidTenantName(tenant) {
		bad(w, ErrInvalidTenant)
		return
	}
	switch {
	case sub == "usage" && r.Method == http.MethodGet:
		if s.tenantReader(w, r, tenant) {
			ok(w, s.tenantUsageReport(tenant))
		}
	case sub != "":
		writeJSONStatus(w, http.StatusNotFound, map[string]string{"error": "no such tenant endpoint"})
	case r.Method == http.MethodGet:
		if s.tenantReader(w, r, tenant) {
			ok(w, s.tenantConfigView(tenant))
		}
	case r.Method == http.MethodPut:
		s.guardWrite(func(w http.ResponseWriter, r *http.Request) { s.putTenant(w, r, tenant) })(w, r)
	case r.Method == http.MethodDelete:
		s.guardWrite(func(w http.ResponseWriter, r *http.Request) { s.deleteTenant(w, r, tenant) })(w, r)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// listTenants answers GET /v1/tenants: every configured tenant and every
// tenant a user is assigned to, with its usage.
func (s *Server) listTenants(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAdmin(w, r) {
		return
	}
	out := []TenantUsageReport{}
	for _, t := range s.knownTenants() {
		out = append(out, s.tenantUsageReport(t))
	}
	ok(w, map[string]any{"tenants": out, "total": len(out)})
}

// tenantConfigView is the tenant's stored and effective configuration.
func (s *Server) tenantConfigView(tenant string) tenantConfigView {
	cfg := s.tenantConfig(tenant)
	return tenantConfigView{Tenant: tenant, Quota: cfg.Quota, Limits: cfg.Limits, EffectiveLimits: s.effectiveLimits(tenant)}
}

// tenantReader lets a global admin read any tenant, and a tenant's own admin
// read its own; it answers the refusal.
func (s *Server) tenantReader(w http.ResponseWriter, r *http.Request, tenant string) bool {
	if !s.authOn() {
		return true
	}
	claims, found := GetClaimsFromContext(r.Context())
	switch {
	case !found:
		writeAccessDenied(w, ErrUnauthorized)
		return false
	case claims.Admin:
		return true
	case claims.Tenant == tenant && s.AuthManager.CheckPermission(r.Context(), "*", PermRead) == nil:
		return true
	}
	writeAccessDenied(w, ErrForbidden)
	return false
}

// putTenant answers PUT /v1/tenants/{id} with {"quota":{…},"limits":{…}}. A
// part left out is kept; sent as null or {} it is removed.
func (s *Server) putTenant(w http.ResponseWriter, r *http.Request, tenant string) {
	if !s.requireAdmin(w, r) {
		return
	}
	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		bad(w, errors.New(`body must be {"quota":{...},"limits":{...}}`))
		return
	}
	cfg := s.tenantConfig(tenant)
	if err := mergeTenantConfig(&cfg, body); err != nil {
		bad(w, err)
		return
	}
	if err := s.Tenants.Set(tenant, cfg); err != nil {
		bad(w, err)
		return
	}
	ok(w, s.tenantConfigView(tenant))
}

// mergeTenantConfig applies the parts a PUT body sent and validates the
// result. An empty part is removed.
func mergeTenantConfig(cfg *TenantConfig, body map[string]json.RawMessage) error {
	if raw, sent := body["quota"]; sent {
		var q TenantQuota
		if err := decodePart(raw, &q); err != nil {
			return errors.New("invalid quota: " + err.Error())
		}
		cfg.Quota = nil
		if q != (TenantQuota{}) {
			cfg.Quota = &q
		}
	}
	if raw, sent := body["limits"]; sent {
		var l TenantLimits
		if err := decodePart(raw, &l); err != nil {
			return errors.New("invalid limits: " + err.Error())
		}
		cfg.Limits = nil
		if l != (TenantLimits{}) {
			cfg.Limits = &l
		}
	}
	return cfg.Validate()
}

// decodePart decodes one part of the body; null leaves v zero.
func decodePart(raw json.RawMessage, v any) error {
	if string(raw) == "null" {
		return nil
	}
	return json.Unmarshal(raw, v)
}

// deleteTenant answers DELETE /v1/tenants/{id}: the tenant has no limits of
// its own any more.
func (s *Server) deleteTenant(w http.ResponseWriter, r *http.Request, tenant string) {
	if !s.requireAdmin(w, r) {
		return
	}
	if err := s.Tenants.Delete(tenant); err != nil {
		bad(w, err)
		return
	}
	ok(w, map[string]string{"status": "deleted", "tenant": tenant})
}
