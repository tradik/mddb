/**
 * GO-054 (2.19.8): tenant quotas, request limits and usage. Mixed into the
 * MDDBClient prototype through client/index.js, so `this` is the client and
 * paths are relative to /v1.
 */
const tenantPath = (tenant) => `/tenants/${encodeURIComponent(tenant)}`;

export const tenantMethods = {
  /** Every known tenant with its usage report (global admin): `{ tenants, total }`. */
  async listTenants() {
    return this.request('/tenants', { method: 'GET' });
  },

  /** One tenant's usage report: a global admin, or the tenant's own admin. */
  async getTenantUsage(tenant) {
    return this.request(`${tenantPath(tenant)}/usage`, { method: 'GET' });
  },

  /**
   * Sets a tenant's quota and limits (global admin). A part left out of
   * `config` is kept; a part sent as null is removed.
   */
  async setTenant(tenant, config) {
    return this.request(tenantPath(tenant), { method: 'PUT', body: JSON.stringify(config) });
  },

  /** Removes a tenant's quota and limits: it has none of its own any more. */
  async deleteTenant(tenant) {
    return this.request(tenantPath(tenant), { method: 'DELETE' });
  },
};
