/**
 * The scope of an MCP API key (#439): the collections it may reach, whether
 * it may only read, and the chat scenario it is bound to; since 2.19.11 the
 * collections among them it may only read (AGT-007) and the memory
 * namespace it shares with other keys (AGT-008).
 */

/** An empty scope: the key reaches every collection and may write. */
export const emptyScope = { collections: [], readOnly: false, scenario: '', readOnlyCollections: [], memoryNamespace: '' };

/**
 * isLimited reports whether a scope limits the collections a key reaches:
 * only such a key has a memory namespace.
 */
export function isLimited({ collections = [], scenario = '' } = {}) {
  return collections.length > 0 || scenario !== '';
}

/**
 * The scope fields of a new key as the server takes them, empty ones left
 * out. Read-only collections no longer among the collections are dropped,
 * and are not sent for a key read-only as a whole; a memory namespace is
 * sent only for a limited key, which the server requires.
 */
export function scopeBody(scope = {}) {
  const { collections = [], readOnly = false, scenario = '', readOnlyCollections = [], memoryNamespace = '' } = scope;
  const body = {};
  if (collections.length > 0) body.collections = collections;
  if (readOnly) body.readOnly = true;
  if (scenario) body.scenario = scenario;
  const readOnlyKept = readOnlyCollections.filter((c) => collections.includes(c));
  if (!readOnly && readOnlyKept.length > 0) body.readOnlyCollections = readOnlyKept;
  const namespace = memoryNamespace.trim();
  if (namespace && isLimited(scope)) body.memoryNamespace = namespace;
  return body;
}

/** A listed key's scope in words. A scenario-bound key is always read-only. */
export function scopeLabel(key) {
  const parts = [];
  if (key.scenario) parts.push(`scenario ${key.scenario}`);
  if (key.collections?.length) parts.push(key.collections.join(', '));
  if (key.readOnly || key.scenario) parts.push('read-only');
  else if (key.readOnlyCollections?.length) parts.push(`read-only ${key.readOnlyCollections.join(', ')}`);
  if (key.memoryNamespace) parts.push(`memory ${key.memoryNamespace}`);
  return parts.length > 0 ? parts.join(' · ') : 'all collections';
}
