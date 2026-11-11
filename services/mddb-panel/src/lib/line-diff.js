/**
 * A line diff of two texts (#404), for "Update from source": what an update
 * would change in a chat skill, shown before it is confirmed.
 *
 * The longest common subsequence of the lines, then a walk that emits the
 * lines both texts share as `same`, and the rest as `removed` (old only) or
 * `added` (new only). Skills are capped at 32 KiB on the server, so the
 * quadratic table stays small.
 */

/** Splits a text into lines; an empty text has none. */
function lines(text) {
  return text === '' ? [] : text.replace(/\r\n/g, '\n').split('\n');
}

/**
 * The diff of `before` and `after`: `[{ type, text, oldLine, newLine }]`,
 * where `type` is `same`, `removed` or `added`, and the line numbers count
 * from 1 in the text the line belongs to (null in the other).
 */
export function lineDiff(before, after) {
  const a = lines(before ?? '');
  const b = lines(after ?? '');
  // common[i][j]: the length of the longest common subsequence of a[i:] and b[j:].
  const common = Array.from({ length: a.length + 1 }, () => new Array(b.length + 1).fill(0));
  for (let i = a.length - 1; i >= 0; i -= 1) {
    for (let j = b.length - 1; j >= 0; j -= 1) {
      common[i][j] = a[i] === b[j] ? common[i + 1][j + 1] + 1 : Math.max(common[i + 1][j], common[i][j + 1]);
    }
  }
  const out = [];
  let i = 0;
  let j = 0;
  while (i < a.length || j < b.length) {
    if (i < a.length && j < b.length && a[i] === b[j]) {
      out.push({ type: 'same', text: a[i], oldLine: i + 1, newLine: j + 1 });
      i += 1;
      j += 1;
    } else if (j >= b.length || (i < a.length && common[i + 1][j] >= common[i][j + 1])) {
      out.push({ type: 'removed', text: a[i], oldLine: i + 1, newLine: null });
      i += 1;
    } else {
      out.push({ type: 'added', text: b[j], oldLine: null, newLine: j + 1 });
      j += 1;
    }
  }
  return out;
}

/** How many lines a diff adds and removes: `{ added, removed }`. */
export function diffStats(diff) {
  return {
    added: diff.filter((d) => d.type === 'added').length,
    removed: diff.filter((d) => d.type === 'removed').length,
  };
}
