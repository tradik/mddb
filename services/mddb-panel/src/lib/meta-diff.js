/**
 * The metadata half of a document diff (FE-013): which metadata keys a
 * revision or another document adds, removes or changes.
 *
 * Metadata arrives as `{ key: [values] }` (a value may also be a bare
 * string); values are compared as the comma-joined text the viewer shows.
 */

/** A metadata value as the viewer shows it. */
export function metaText(value) {
  return Array.isArray(value) ? value.join(', ') : String(value ?? '');
}

/**
 * The difference of two metadata maps, sorted by key:
 * `[{ key, type, before, after }]`, where `type` is `added` (only in
 * `after`), `removed` (only in `before`) or `changed`; unchanged keys are
 * left out. `before`/`after` are display texts, null on the missing side.
 */
export function metaDiff(before, after) {
  const a = before || {};
  const b = after || {};
  const keys = [...new Set([...Object.keys(a), ...Object.keys(b)])].sort((x, y) => x.localeCompare(y));
  const out = [];
  for (const key of keys) {
    const inA = Object.hasOwn(a, key);
    const inB = Object.hasOwn(b, key);
    if (!inB) {
      out.push({ key, type: 'removed', before: metaText(a[key]), after: null });
    } else if (!inA) {
      out.push({ key, type: 'added', before: null, after: metaText(b[key]) });
    } else if (metaText(a[key]) !== metaText(b[key])) {
      out.push({ key, type: 'changed', before: metaText(a[key]), after: metaText(b[key]) });
    }
  }
  return out;
}
