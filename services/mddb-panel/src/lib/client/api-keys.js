/**
 * The signed-in user's own account: who they are (/v1/auth/me) and their
 * personal API keys. A key's secret is returned once, by create; the list
 * names keys by their SHA-256 hash, which is also what revoke takes.
 */
import { postJson } from './http';

export const apiKeyMethods = {
  /** `{ username, admin, createdAt }` of the caller. */
  getMe() {
    return this.request('/auth/me', { method: 'GET' });
  },

  /** `{ keys: [{ keyHash, description, createdAt, expiresAt }] }`. */
  listApiKeys() {
    return this.request('/auth/api-keys', { method: 'GET' });
  },

  /** Creates a key; `expiresAt` is a Unix time, 0 for never. Answers with the secret `key`. */
  createApiKey({ description, expiresAt = 0 }) {
    return postJson(this, '/auth/api-key', { description, expiresAt });
  },

  /** Revokes one of the caller's keys by hash. */
  deleteApiKey(keyHash) {
    return this.request(`/auth/api-keys/${encodeURIComponent(keyHash)}`, { method: 'DELETE' });
  },
};
