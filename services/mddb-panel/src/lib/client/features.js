/**
 * GO-049 (2.19.0): module on/off switches. These methods are attached to the
 * MDDBClient prototype at the end of mddb-client.js, so they run with
 * `this` = the client and reuse its `request` helper (paths are relative to
 * /v1).
 */
export const featureMethods = {
  /** Every switchable module with its state: `{ features: [...], version }`. */
  async listFeatures() {
    return this.request('/features', { method: 'GET' });
  },

  /**
   * Turns one module on or off (global admin). Answers
   * `{ feature, version }`; a module forced off by MDDB_FEATURES_FORCE_OFF
   * answers 409 with `lockedBy` in the error's body.
   */
  async setFeature(name, enabled) {
    return this.request(`/features/${encodeURIComponent(name)}`, {
      method: 'PUT',
      body: JSON.stringify({ enabled: Boolean(enabled) }),
    });
  },
};
