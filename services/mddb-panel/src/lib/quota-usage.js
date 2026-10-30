/**
 * A collection's use of its quota (GO-043), as GET /v1/collection-config
 * reports it next to the quota: `usage` holds the documents, stored bytes and
 * vectors the write gate counts and the embedding calls and tokens spent
 * today (UTC). The server sends `usage` only for a collection with a quota.
 */
import { formatBytes } from './advisor-format';

/** Each limit, the usage field it is measured against, and how to name it. */
export const USAGE_FIELDS = [
  { limit: 'maxDocuments', used: 'documents', label: 'Documents', unit: 'documents' },
  { limit: 'maxBytes', used: 'bytes', label: 'Stored bytes', unit: 'stored', bytes: true },
  { limit: 'maxVectors', used: 'vectors', label: 'Vectors', unit: 'vectors' },
  { limit: 'embeddingCallsPerDay', used: 'embeddingCalls', label: 'Embedding calls today', unit: 'embedding calls today' },
  { limit: 'embeddingTokensPerDay', used: 'embeddingTokens', label: 'Embedding tokens today', unit: 'embedding tokens today' },
];

/** The share of a limit at which a bar turns amber. */
export const NEAR_LIMIT = 80;

/** Formats a count, or a byte size, for people: "1,234", "4.0 MB". */
export function formatAmount(n, bytes = false) {
  if (bytes) return n > 0 ? formatBytes(n) : '0 B';
  return n.toLocaleString('en-US');
}

/**
 * One row per limit the quota sets: what is used, the limit, the share in
 * percent (above 100 when a limit was lowered below what the collection
 * holds) and the text that names both, e.g. "1,234 / 5,000 documents".
 * Empty without a quota or without usage.
 */
export function usageRows(quota, usage) {
  if (!quota || !usage) return [];
  return USAGE_FIELDS.filter((f) => quota[f.limit] > 0).map((f) => {
    const used = usage[f.used] || 0;
    const limit = quota[f.limit];
    const percent = Math.round((used / limit) * 100);
    const level = percent >= 100 ? 'over' : percent >= NEAR_LIMIT ? 'near' : 'ok';
    return {
      key: f.limit,
      label: f.label,
      used,
      limit,
      percent,
      level,
      text: `${formatAmount(used, f.bytes)} / ${formatAmount(limit, f.bytes)} ${f.unit}`,
    };
  });
}
