/**
 * A tenant's quota, request limits and usage (GO-054), as GET
 * /v1/tenants/{id}/usage reports them, and the edit form's fields.
 */
import { USAGE_FIELDS, formatAmount } from './quota-usage';

/**
 * The tenant's limits and the usage each is measured against: the
 * collection quota's, plus the number of collections and the tenant's share
 * of the server's vector RAM limit (`vectorRamLimitBytes` in the report).
 */
export const TENANT_USAGE_FIELDS = [
  ...USAGE_FIELDS.slice(0, 3),
  { limit: 'maxCollections', used: 'collections', label: 'Collections', unit: 'collections' },
  { limit: 'vectorRamLimitBytes', used: 'vectorRamBytes', label: 'Vector RAM (estimate)', unit: 'of vector RAM', bytes: true },
  ...USAGE_FIELDS.slice(3),
];

/** The quota the bars compare usage with: the stored quota plus the RAM share in bytes. */
export function barLimits(report) {
  return { ...report.quota, vectorRamLimitBytes: report.vectorRamLimitBytes || 0 };
}

/** The quota fields of the edit form; `share` is a fraction 0–1, the rest whole numbers >= 0. */
export const QUOTA_INPUTS = [
  { name: 'maxDocuments', label: 'Documents' },
  { name: 'maxBytes', label: 'Stored bytes' },
  { name: 'maxVectors', label: 'Vectors' },
  { name: 'maxCollections', label: 'Collections holding documents' },
  { name: 'embeddingCallsPerDay', label: 'Embedding calls per day (UTC)' },
  { name: 'embeddingTokensPerDay', label: 'Embedding tokens per day (UTC)' },
  { name: 'vectorRamShare', label: 'Share of the vector RAM limit (0–1)', share: true },
];

/** The request-limit fields of the edit form: 0 takes the server default, -1 lifts it. */
export const LIMIT_INPUTS = [
  { name: 'readPerSecond', label: 'Reads and searches per second (tenant)' },
  { name: 'writePerSecond', label: 'Writes per second (tenant)' },
  { name: 'keyReadPerSecond', label: 'Reads per second (each API key or user)' },
  { name: 'keyWritePerSecond', label: 'Writes per second (each API key or user)' },
  { name: 'maxConcurrentSearches', label: 'Searches at once' },
  { name: 'maxConcurrentWrites', label: 'Writes at once' },
  { name: 'maxConcurrentReindexes', label: 'Vector reindexes at once' },
];

/** The form's text values for a stored part: "" where nothing is set. */
export function toForm(inputs, part) {
  const out = {};
  for (const f of inputs) out[f.name] = part?.[f.name] ? String(part[f.name]) : '';
  return out;
}

/** Whether a value is acceptable for a field; `min` is the lowest whole number allowed. */
function invalid(field, value, min) {
  if (field.share) return !(value >= 0 && value <= 1);
  return !Number.isInteger(value) || value < min;
}

/**
 * The part of a PUT body for the form's values: null when nothing is set
 * (the server then removes the part), or `{ error }` naming the first bad field.
 */
export function fromForm(inputs, values, min) {
  const part = {};
  for (const f of inputs) {
    const text = String(values[f.name] ?? '').trim();
    if (text === '') continue;
    const n = Number(text);
    if (invalid(f, n, min)) {
      const rule = f.share ? 'a number from 0 to 1' : `a whole number from ${min}`;
      return { error: `${f.label}: must be ${rule}.` };
    }
    if (n !== 0) part[f.name] = n;
  }
  return { part: Object.keys(part).length > 0 ? part : null };
}

/** The tenant's refusals, most frequent first: `[{ reason, count }]`. */
export function refusalList(refusals) {
  return Object.entries(refusals || {})
    .map(([reason, count]) => ({ reason, count }))
    .sort((a, b) => b.count - a.count || a.reason.localeCompare(b.reason));
}

/** The total of the refusals. */
export function refusalTotal(refusals) {
  return Object.values(refusals || {}).reduce((sum, n) => sum + n, 0);
}

/** "12 jobs, average wait 0.4 s, longest 2.1 s" for one queue's history. */
export function queueSummary(queue) {
  const taken = queue?.taken;
  if (!taken?.jobs) return 'no jobs taken yet';
  const avg = taken.waitSecondsTotal / taken.jobs;
  return `${formatAmount(taken.jobs)} jobs taken, average wait ${avg.toFixed(1)} s, longest ${taken.maxWaitSeconds.toFixed(1)} s`;
}

/** A limit as people read it: "none" for 0. */
export function limitText(n) {
  return n > 0 ? formatAmount(n) : 'none';
}
