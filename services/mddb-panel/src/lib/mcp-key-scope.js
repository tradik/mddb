/**
 * The scope of an MCP API key (#439): the collections it may reach, whether
 * it may only read, and the chat scenario it is bound to.
 */

/** An empty scope: the key reaches every collection and may write. */
export const emptyScope = { collections: [], readOnly: false, scenario: '' };

/** The scope fields of a new key as the server takes them, empty ones left out. */
export function scopeBody({ collections = [], readOnly = false, scenario = '' } = {}) {
  const body = {};
  if (collections.length > 0) body.collections = collections;
  if (readOnly) body.readOnly = true;
  if (scenario) body.scenario = scenario;
  return body;
}

/** A listed key's scope in words. A scenario-bound key is always read-only. */
export function scopeLabel(key) {
  const parts = [];
  if (key.scenario) parts.push(`scenario ${key.scenario}`);
  if (key.collections?.length) parts.push(key.collections.join(', '));
  if (key.readOnly || key.scenario) parts.push('read-only');
  return parts.length > 0 ? parts.join(' · ') : 'all collections';
}
