/**
 * Duplicate detection (/v1/find-duplicates): groups of identical (exact),
 * close-by-embedding (similar) or overlapping-text (minhash) documents in one
 * collection. Each mode answers in its own field: exactGroups, similarGroups,
 * minhashGroups — a field is absent when it has no groups.
 */
import { postJson } from './http';

export const duplicateMethods = {
  /** `mode`: exact, similar, minhash or both (exact + similar). */
  findDuplicates({ collection, mode = 'both', threshold = 0.9, maxDocs = 5000, distanceMetric = 'cosine' }) {
    return postJson(this, '/find-duplicates', { collection, mode, threshold, maxDocs, distanceMetric });
  },
};
