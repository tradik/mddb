// AGT-004. Turns the /v1/gain report into what the panel's card shows:
// short token counts and the largest shares of a breakdown.

// compactNumber renders 160700000 as "160.7M", 14922 as "14.9k".
export function compactNumber(n) {
  const v = Number(n) || 0;
  const units = [
    [1e9, 'G'],
    [1e6, 'M'],
    [1e3, 'k'],
  ];
  for (const [size, suffix] of units) {
    if (Math.abs(v) >= size) return `${(v / size).toFixed(1)}${suffix}`;
  }
  return String(v);
}

// topShares returns the largest entries of a counts map as
// [{ name, percent }], most frequent first.
export function topShares(counts, limit = 3) {
  const entries = Object.entries(counts || {});
  const total = entries.reduce((sum, [, n]) => sum + n, 0);
  if (total === 0) return [];
  return entries
    .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
    .slice(0, limit)
    .map(([name, n]) => ({ name, percent: Math.round((100 * n) / total) }));
}

// formatShares renders topShares as "hnsw 65% · flat 12%".
export function formatShares(counts, limit = 3) {
  return topShares(counts, limit)
    .map((s) => `${s.name || 'other'} ${s.percent}%`)
    .join(' · ');
}
