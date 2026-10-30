/**
 * Vector index choices shared by the vector, hybrid and cross-collection
 * search views. `auto` (SRCH-013, 2.18.9+) lets the server's planner pick per
 * query from collection size, memory, built indexes and filter selectivity;
 * "flat" stays the default, as on the server.
 */
export const VECTOR_ALGORITHMS = [
  { value: 'flat', label: 'Flat (Exact)' },
  { value: 'auto', label: 'Auto (server chooses)' },
  { value: 'hnsw', label: 'HNSW (Approximate)' },
  { value: 'ivf', label: 'IVF (Clustered)' },
  { value: 'pq', label: 'PQ (Compressed)' },
  { value: 'opq', label: 'OPQ (Optimized PQ)' },
  { value: 'sq', label: 'SQ (int8, 4x smaller)' },
  { value: 'sq4', label: 'SQ4 (int4, 8x smaller)' },
  { value: 'bq', label: 'BQ (Binary Quantized)' },
];

/**
 * The message for a vector search refused while its index loads or trains
 * (503). GO-047: flat, hnsw and bq answer as soon as vectors are loaded; ivf,
 * pq, opq, sq and sq4 train in the background and answer 503 with
 * `training: true` until then. Returns `{ message, training }`, or null for
 * any other error. A training index is not retried: another algorithm answers
 * now, and the server's message says which.
 */
export function indexUnavailable(error) {
  if (error?.status !== 503) return null;
  return {
    message: error.body?.error || 'The vector index is still loading. Please retry in a moment.',
    training: error.body?.training === true,
  };
}
