/**
 * Presenting a search recommendation (SRCH-010).
 *
 * The numbers the advisor returns are raw: a ratio, a byte count, a term
 * average. Rendering them is the panel's job, and it is the part with edge
 * cases — an empty collection divides by zero, a missing field is not the same
 * as a zero, and a percentage of nothing is not 0%.
 *
 * Kept out of the component so those cases can be tested without a browser.
 */

/** Formats the share of a collection that carries vectors. */
export function embeddedCoverage(profile) {
  if (!profile || !profile.documents) return null;
  const embedded = profile.embeddedDocuments || 0;
  return Math.round((embedded / profile.documents) * 100);
}

/**
 * Says whether the recommendation came from a sample rather than the whole
 * collection — a figure from a sample should say so.
 */
export function isSampled(profile) {
  if (!profile || !profile.sampled || !profile.documents) return false;
  return profile.sampled < profile.documents;
}

/** Formats a byte count for people. */
export function formatBytes(bytes) {
  if (!bytes || bytes < 0) return '—';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value < 10 && unit > 0 ? value.toFixed(1) : Math.round(value)} ${units[unit]}`;
}

/**
 * Describes what the vocabulary measurement means, in words.
 *
 * The number alone is meaningless to anyone who has not read the docs: 0.18 is
 * not obviously bad and 8 is not obviously fine.
 */
export function vocabularyVerdict(termsPerDocument) {
  if (termsPerDocument === undefined || termsPerDocument === null) return null;
  if (termsPerDocument < 2) {
    return 'Documents look alike to a keyword ranker; query expansion helps';
  }
  if (termsPerDocument < 6) {
    return 'Moderately varied vocabulary';
  }
  return 'Varied vocabulary; exact term matching discriminates well';
}
