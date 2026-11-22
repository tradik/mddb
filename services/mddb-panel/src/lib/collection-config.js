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

import { needsForge } from './automation-sync-form';

/** Retrieval keys the dialog edits; every other stored key is kept. */
export const EDITED_RETRIEVAL_KEYS = [
  'defaultSearchType', 'topK', 'retrievalMode', 'hybridStrategy', 'hybridAlpha', 'hybridAlphaSet',
  'contextTokenBudget', 'vectorAlgorithm', 'summaryMode', 'summaryWeight', 'maxTopK', 'rerank',
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
    ...rerankFromProfile(r.rerank),
    excludeMetaKeys: (cfg.fts?.excludeMetaKeys || []).join(', '),
    quota: Object.fromEntries(QUOTA_FIELDS.map(({ key }) => [key, q[key] ? String(q[key]) : ''])),
  };
}

/**
 * RAG-011: the dialog's state for the profile's model reranking. `rerankEnabled`
 * is '' (not set: off), 'on' or 'off'.
 */
export function rerankFromProfile(rerank) {
  const r = rerank || {};
  let enabled = '';
  if (r.enabled === true) enabled = 'on';
  if (r.enabled === false) enabled = 'off';
  return {
    rerankEnabled: enabled,
    rerankCandidates: r.candidates ? String(r.candidates) : '',
    rerankTopN: r.topN ? String(r.topN) : '',
  };
}

/** The profile's rerank block the dialog's fields describe, or null when nothing is set. */
export function buildRerank(advanced) {
  const out = {};
  if (advanced.rerankEnabled) out.enabled = advanced.rerankEnabled === 'on';
  if (Number(advanced.rerankCandidates) > 0) out.candidates = Number(advanced.rerankCandidates);
  if (Number(advanced.rerankTopN) > 0) out.topN = Number(advanced.rerankTopN);
  return Object.keys(out).length > 0 ? out : null;
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
 * vector index, summary mode and weight, result cap and model reranking
 * (2.19.8), full-text excluded
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
  const rerank = buildRerank(advanced);
  if (rerank) retrieval.rerank = rerank;
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

/** The dialog's state for a stored repository block (INT-023). */
export function repositoryFromConfig(cfg = {}) {
  const r = cfg.repository || {};
  return { url: r.url || '', forge: r.forge || '', tokenSecretRef: r.tokenSecretRef || '' };
}

/**
 * The repository block to send (INT-023): the URL, the forge only for a host
 * the server does not know, and the token's secret name — never a value.
 * An empty URL removes a stored block (`{}`) and sends nothing otherwise
 * (undefined: the server keeps what it has, which is nothing).
 */
export function buildRepository(form, stored) {
  const url = form.url.trim();
  if (!url) return stored?.url ? {} : undefined;
  const out = { url };
  if (needsForge(url) && form.forge) out.forge = form.forge;
  if (form.tokenSecretRef) out.tokenSecretRef = form.tokenSecretRef;
  return out;
}

/**
 * The cron rule "Create issues sync" adds (INT-023): every 15 minutes, all
 * issues and pull requests with comments of the collection's repository,
 * mirrored into `<collection>-issues`.
 */
export function issuesSyncRule(collection) {
  return {
    type: 'cron',
    name: `${collection} issues`,
    schedule: '*/15 * * * *',
    enabled: true,
    sync: {
      source: 'issues',
      collection: `${collection}-issues`,
      lang: 'en',
      repositoryFrom: collection,
      state: 'all',
      include: ['issues', 'pulls'],
      comments: true,
    },
  };
}
