// SRCH-019 (2.18.8): pure helpers for the retrieval evaluation view —
// presets for POST /v1/eval, parsing the stored-query form and picking the
// best configuration of a run.

/** Search configurations offered for a run; the server accepts up to 8. */
export const EVAL_PRESETS = [
  { id: 'fts-bm25', label: 'Full text (BM25)', config: { searchType: 'fts', algorithm: 'bm25', name: 'fts/bm25' } },
  { id: 'hybrid-rrf', label: 'Hybrid (RRF)', config: { searchType: 'hybrid', strategy: 'rrf', name: 'hybrid/rrf' } },
  { id: 'hybrid-alpha', label: 'Hybrid (alpha 0.5)', config: { searchType: 'hybrid', strategy: 'alpha', alpha: 0.5, name: 'hybrid/alpha' } },
  { id: 'vector', label: 'Vector', config: { searchType: 'vector', name: 'vector' } },
];

/** The server's cap on configurations per run and on k. */
export const MAX_EVAL_CONFIGS = 8;
export const MAX_EVAL_K = 100;

/** "a.md, b.md" → ["a.md", "b.md"], blanks dropped. */
export function parseKeys(text) {
  return text.split(',').map((k) => k.trim()).filter(Boolean);
}

/**
 * "a.md=3, b.md=1" → { "a.md": 3, "b.md": 1 }. Throws on a pair that is not
 * key=0..3, so the form can say which one is wrong.
 */
export function parseGrades(text) {
  const grades = {};
  for (const pair of parseKeys(text)) {
    const at = pair.lastIndexOf('=');
    const key = pair.slice(0, at).trim();
    const grade = Number(pair.slice(at + 1).trim());
    if (at <= 0 || !key || !Number.isInteger(grade) || grade < 0 || grade > 3) {
      throw new Error(`Grade "${pair}" must be key=0..3`);
    }
    grades[key] = grade;
  }
  return grades;
}

/** { "a.md": 3 } → "a.md=3", for showing a stored query. */
export function formatGrades(grades) {
  return Object.entries(grades || {}).map(([k, v]) => `${k}=${v}`).join(', ');
}

/** k clamped to 1..100; anything else is the default 10. */
export function clampK(value) {
  const k = Math.trunc(Number(value));
  if (!Number.isFinite(k) || k < 1) return 10;
  return Math.min(k, MAX_EVAL_K);
}

/** The config name of the result with the highest nDCG@k, ignoring failures. */
export function bestConfig(results) {
  let best = null;
  for (const r of results || []) {
    if (r.error) continue;
    if (!best || r.ndcgAtK > best.ndcgAtK) best = r;
  }
  return best ? best.config : null;
}

/** A 0..1 score with three decimals; a dash when missing. */
export function formatScore(value) {
  return typeof value === 'number' ? value.toFixed(3) : '—';
}
