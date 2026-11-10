// Helpers for the code symbol lookup (2.18.8, `GET /v1/symbol`).

/** The relations `GET /v1/symbol` answers, in the order they are shown. */
export const RELATIONS = [
  { id: 'definitions', label: 'Definitions' },
  { id: 'references', label: 'References' },
  { id: 'callers', label: 'Callers' },
  { id: 'callees', label: 'Callees' },
  { id: 'tests', label: 'Tests' },
  { id: 'siblings', label: 'Siblings' },
];

/** The largest per-relation limit the server accepts. */
export const MAX_SYMBOL_LIMIT = 200;

/** "path:12-18" (or "path:12" for one line) for a passage. */
export function passageLocation(p) {
  const { start, end } = p.lines || {};
  if (!start) return p.path;
  return end && end !== start ? `${p.path}:${start}-${end}` : `${p.path}:${start}`;
}
