/**
 * A line diff of two texts: what "Update from source" would change in a chat
 * skill (#404), what a document revision changed and how two documents
 * differ (FE-013).
 *
 * The lines both texts start and end with are kept as they are; between them
 * the longest common subsequence of the lines decides which lines are `same`,
 * and the rest are `removed` (old only) or `added` (new only). The LCS table
 * is quadratic, so a middle part above `maxCells` (two documents of about
 * 2,000 differing lines each) is shown as removed then added instead of
 * stalling the page.
 */

/** The largest LCS table (rows x columns) the diff builds. */
export const maxCells = 4_000_000;

/** Splits a text into lines; an empty text has none. */
function lines(text) {
  return text === '' ? [] : text.replace(/\r\n/g, '\n').split('\n');
}

/** A line both texts share: index `i` in the old text and `j` in the new. */
function same(a, i, j) {
  return { type: 'same', text: a[i], oldLine: i + 1, newLine: j + 1 };
}

function removed(a, i) {
  return { type: 'removed', text: a[i], oldLine: i + 1, newLine: null };
}

function added(b, j) {
  return { type: 'added', text: b[j], oldLine: null, newLine: j + 1 };
}

/**
 * Diffs a[start:endA] against b[start:endB] into `out`, by the longest
 * common subsequence, or as a plain replacement when the table would be too big.
 */
function diffMiddle(a, b, start, endA, endB, out) {
  const rows = endA - start;
  const cols = endB - start;
  if (rows * cols > maxCells) {
    for (let i = start; i < endA; i += 1) out.push(removed(a, i));
    for (let j = start; j < endB; j += 1) out.push(added(b, j));
    return;
  }
  // common[i][j]: the length of the longest common subsequence of the
  // middle parts from line i (old) and j (new) on, counted from `start`.
  const common = Array.from({ length: rows + 1 }, () => new Array(cols + 1).fill(0));
  for (let i = rows - 1; i >= 0; i -= 1) {
    for (let j = cols - 1; j >= 0; j -= 1) {
      common[i][j] = a[start + i] === b[start + j] ? common[i + 1][j + 1] + 1 : Math.max(common[i + 1][j], common[i][j + 1]);
    }
  }
  let i = 0;
  let j = 0;
  while (i < rows || j < cols) {
    if (i < rows && j < cols && a[start + i] === b[start + j]) {
      out.push(same(a, start + i, start + j));
      i += 1;
      j += 1;
    } else if (j >= cols || (i < rows && common[i + 1][j] >= common[i][j + 1])) {
      out.push(removed(a, start + i));
      i += 1;
    } else {
      out.push(added(b, start + j));
      j += 1;
    }
  }
}

/**
 * The diff of `before` and `after`: `[{ type, text, oldLine, newLine }]`,
 * where `type` is `same`, `removed` or `added`, and the line numbers count
 * from 1 in the text the line belongs to (null in the other).
 */
export function lineDiff(before, after) {
  const a = lines(before ?? '');
  const b = lines(after ?? '');
  let start = 0;
  while (start < a.length && start < b.length && a[start] === b[start]) start += 1;
  let endA = a.length;
  let endB = b.length;
  while (endA > start && endB > start && a[endA - 1] === b[endB - 1]) {
    endA -= 1;
    endB -= 1;
  }
  const out = [];
  for (let k = 0; k < start; k += 1) out.push(same(a, k, k));
  diffMiddle(a, b, start, endA, endB, out);
  for (let k = 0; endA + k < a.length; k += 1) out.push(same(a, endA + k, endB + k));
  return out;
}

/** How many lines a diff adds and removes: `{ added, removed }`. */
export function diffStats(diff) {
  return {
    added: diff.filter((d) => d.type === 'added').length,
    removed: diff.filter((d) => d.type === 'removed').length,
  };
}
