/**
 * Collection configuration: what the settings dialog sends back.
 *
 * PUT /v1/collection-config replaces the stored config with the request (only
 * `quota` is kept when omitted), so a dialog that sends only the fields it
 * shows erases the rest — `fts`, `code`, `embedding`, `wordpress`,
 * `diskOnlyVectors`, and retrieval settings such as `oversample` or `groupBy`.
 * The dialog therefore starts from the stored config and overrides what it
 * edits. Output-only fields (`readOnly`, `corpus`, `secretKeySet`) and the
 * evaluation set (kept by the server) are not sent.
 */

/** Retrieval keys the dialog edits; every other stored key is kept. */
export const EDITED_RETRIEVAL_KEYS = [
  'defaultSearchType', 'topK', 'retrievalMode', 'hybridStrategy', 'hybridAlpha', 'hybridAlphaSet',
  'contextTokenBudget', 'vectorAlgorithm', 'summaryMode', 'summaryWeight', 'maxTopK',
];

/** GO-043 quota limits, in the order the dialog shows them. */
export const QUOTA_FIELDS = [
  { key: 'maxDocuments', label: 'Max documents' },
  { key: 'maxBytes', label: 'Max bytes' },
  { key: 'maxVectors', label: 'Max vectors' },
  { key: 'embeddingCallsPerDay', label: 'Embedding calls / day' },
  { key: 'embeddingTokensPerDay', label: 'Embedding tokens / day' },
];

/** Fields of a stored config the server owns or never takes back. */
const SERVER_OWNED_KEYS = ['readOnly', 'corpus', 'evalSet', 'quota'];

/** A shallow copy of `obj` without `keys`; `obj` itself is left as it is. */
export function omit(obj, keys) {
  const copy = { ...obj };
  for (const key of keys) delete copy[key];
  return copy;
}

/** The stored config minus what the server owns or never takes back. */
export function preservedConfig(cfg = {}) {
  const rest = omit(cfg, SERVER_OWNED_KEYS);
  if (rest.storageConfig) rest.storageConfig = omit(rest.storageConfig, ['secretKeySet']);
  if (rest.retrieval) {
    rest.retrieval = Object.fromEntries(
      Object.entries(rest.retrieval).filter(([key]) => !EDITED_RETRIEVAL_KEYS.includes(key)),
    );
  }
  return rest;
}

/** The dialog's state for the 2.18.9/2.18.10 settings of a stored config. */
export function advancedFromConfig(cfg = {}) {
  const r = cfg.retrieval || {};
  const q = cfg.quota || {};
  return {
    vectorAlgorithm: r.vectorAlgorithm || '',
    summaryMode: r.summaryMode || '',
    summaryWeight: r.summaryWeight != null ? String(r.summaryWeight) : '',
    maxTopK: r.maxTopK ? String(r.maxTopK) : '',
    excludeMetaKeys: (cfg.fts?.excludeMetaKeys || []).join(', '),
    quota: Object.fromEntries(QUOTA_FIELDS.map(({ key }) => [key, q[key] ? String(q[key]) : ''])),
  };
}

/** The quota the dialog's fields describe: only the limits that are set. */
export function buildQuota(fields) {
  const out = {};
  for (const { key } of QUOTA_FIELDS) {
    const n = Number(fields[key]);
    if (Number.isInteger(n) && n > 0) out[key] = n;
  }
  return out;
}

/** True when two quotas set the same limits. */
function sameQuota(a, b) {
  return QUOTA_FIELDS.every(({ key }) => (a?.[key] || 0) === (b?.[key] || 0));
}

/**
 * Adds the 2.18.9/2.18.10 settings to a payload: the retrieval profile's
 * vector index, summary mode and weight and result cap, full-text excluded
 * meta keys, and the quota. A quota is sent only when it changed — changing
 * one needs admin on "*", and an omitted quota keeps the stored one; clearing
 * every limit sends {} to remove it.
 */
export function applyAdvanced(payload, advanced, stored = {}) {
  const retrieval = { ...payload.retrieval };
  if (advanced.vectorAlgorithm) retrieval.vectorAlgorithm = advanced.vectorAlgorithm;
  if (advanced.summaryMode) retrieval.summaryMode = advanced.summaryMode;
  if (advanced.summaryWeight !== '' && Number(advanced.summaryWeight) > 0) {
    retrieval.summaryWeight = Number(advanced.summaryWeight);
  }
  if (Number(advanced.maxTopK) > 0) retrieval.maxTopK = Number(advanced.maxTopK);
  if (Object.keys(retrieval).length > 0) payload.retrieval = retrieval;
  else delete payload.retrieval;

  const keys = advanced.excludeMetaKeys.split(',').map((k) => k.trim()).filter(Boolean);
  const fts = { ...payload.fts };
  if (keys.length) fts.excludeMetaKeys = keys;
  else delete fts.excludeMetaKeys;
  if (Object.keys(fts).length > 0) payload.fts = fts;
  else delete payload.fts;

  const quota = buildQuota(advanced.quota);
  if (!sameQuota(quota, stored.quota)) payload.quota = quota;
  return payload;
}
