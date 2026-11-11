/**
 * Metadata facets (/v1/aggregate): how many documents of a collection carry
 * each value of the chosen metadata keys, optionally pre-filtered.
 */
import { postJson } from './http';

export const aggregateMethods = {
  /**
   * Counts per value of each field in `fields`:
   * `{ totalDocs, facets: { field: [{ value, count }] }, durationMs }`.
   */
  aggregate({ collection, fields, filterMeta, orderBy = 'count', maxFacetSize = 50 }) {
    const body = { collection, facets: fields.map((field) => ({ field, orderBy })), maxFacetSize };
    if (filterMeta && Object.keys(filterMeta).length > 0) body.filterMeta = filterMeta;
    return postJson(this, '/aggregate', body);
  },
};
