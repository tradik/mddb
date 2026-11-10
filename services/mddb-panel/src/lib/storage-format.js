// Pure helpers for the storage breakdown (GET /v1/storage, 2.18.2) and the
// indexing status (GET /v1/indexing-status, 2.18.8): no DOM, so they are
// tested in Node.

/** The categories a collection's bytes are split into, in display order. */
export const STORAGE_CATEGORIES = ['content', 'metadata', 'fullText', 'vectors', 'other'];

/** Human labels for the categories. */
export const CATEGORY_LABELS = {
  content: 'Content',
  metadata: 'Metadata',
  fullText: 'Full text',
  vectors: 'Vectors',
  other: 'Other',
};

const UNITS = ['B', 'KB', 'MB', 'GB', 'TB'];

/** Bytes as "1.5 MB" (1024-based); anything not a positive number is "0 B". */
export function formatBytes(bytes) {
  const n = Number(bytes);
  if (!Number.isFinite(n) || n <= 0) return '0 B';
  const i = Math.min(Math.floor(Math.log(n) / Math.log(1024)), UNITS.length - 1);
  const value = n / 1024 ** i;
  return `${i === 0 ? value : value.toFixed(value >= 100 ? 0 : 1)} ${UNITS[i]}`;
}

/** `part` as a percentage of `total`, one decimal; 0 when total is 0. */
export function percentOf(part, total) {
  if (!total || total <= 0) return 0;
  return Math.round((Number(part || 0) / total) * 1000) / 10;
}

/**
 * The category rows of one collection: label, bytes and share of its total,
 * skipping categories with no bytes.
 */
export function categoryRows(collection = {}) {
  const by = collection.byCategory || {};
  const total = collection.totalBytes || 0;
  return STORAGE_CATEGORIES
    .filter((key) => (by[key] || 0) > 0)
    .map((key) => ({ key, label: CATEGORY_LABELS[key], bytes: by[key], percent: percentOf(by[key], total) }));
}

/** The text alternative of a category bar: "Content 1.0 MB (10%), …". */
export function categorySummary(rows) {
  if (!rows.length) return 'No bytes recorded';
  return rows.map((r) => `${r.label} ${formatBytes(r.bytes)} (${r.percent}%)`).join(', ');
}

/** Collections sorted by size, largest first: [name, entry]. */
export function sortedCollections(collections = {}) {
  return Object.entries(collections || {}).sort((a, b) => (b[1]?.totalBytes || 0) - (a[1]?.totalBytes || 0));
}

/** Buckets sorted by bytes in use, largest first: [name, entry]. */
export function sortedBuckets(buckets = {}) {
  return Object.entries(buckets || {}).sort((a, b) => (b[1]?.inUseBytes || 0) - (a[1]?.inUseBytes || 0));
}

/** Milliseconds as "850 ms" or "41.2 s". */
export function formatDuration(ms) {
  const n = Number(ms) || 0;
  return n < 1000 ? `${Math.round(n)} ms` : `${(n / 1000).toFixed(1)} s`;
}

/** Seconds left as "45 s", "12 min" or "2 h 5 min"; empty for none. */
export function formatEta(seconds) {
  const n = Math.round(Number(seconds) || 0);
  if (n <= 0) return '';
  if (n < 60) return `${n} s`;
  if (n < 3600) return `${Math.round(n / 60)} min`;
  const h = Math.floor(n / 3600);
  return `${h} h ${Math.round((n % 3600) / 60)} min`;
}

/** The indexing progress (0..1) as a whole-number percentage, clamped. */
export function progressPercent(progress) {
  const n = Number(progress);
  if (!Number.isFinite(n)) return 0;
  return Math.max(0, Math.min(100, Math.round(n * 100)));
}
