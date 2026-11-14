// Helpers for the code symbol lookup (2.18.8, `GET /v1/symbol`).

/**
 * The relations `GET /v1/symbol` answers, in the order they are shown.
 * `implementations` (2.19.3) is off by default: it needs a collection indexed
 * with `mddb-cli autoindex --semantic`, and asking for it without facts only
 * returns an empty list.
 */
export const RELATIONS = [
  { id: 'definitions', label: 'Definitions' },
  { id: 'references', label: 'References' },
  { id: 'callers', label: 'Callers' },
  { id: 'callees', label: 'Callees' },
  { id: 'tests', label: 'Tests' },
  { id: 'siblings', label: 'Siblings' },
  { id: 'implementations', label: 'Implementations', optIn: true },
];

/** The relations asked for until the user changes them. */
export const DEFAULT_RELATIONS = RELATIONS.filter((r) => !r.optIn).map((r) => r.id);

/** The largest per-relation limit the server accepts. */
export const MAX_SYMBOL_LIMIT = 200;

/** "path:12-18" (or "path:12" for one line) for a passage. */
export function passageLocation(p) {
  const { start, end } = p.lines || {};
  if (!start) return p.path;
  return end && end !== start ? `${p.path}:${start}-${end}` : `${p.path}:${start}`;
}

/**
 * How an answer's precision is shown (2.19.3). The server leaves precision
 * out of a heuristic answer to a plain request, so absent reads as heuristic.
 * Each style pairs a word with its colour — never colour alone — and every
 * pair keeps text contrast above 7:1 (WCAG 2.2 AAA).
 */
const PRECISION_STYLES = {
  semantic: { label: 'semantic', className: 'bg-green-100 text-green-900 border-green-700' },
  mixed: { label: 'mixed', className: 'bg-amber-100 text-amber-900 border-amber-700' },
  heuristic: { label: 'heuristic', className: 'bg-gray-100 text-gray-900 border-gray-600' },
};

export function precisionStyle(precision) {
  return PRECISION_STYLES[precision] || PRECISION_STYLES.heuristic;
}
