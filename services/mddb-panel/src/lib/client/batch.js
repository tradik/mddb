/**
 * Batch writes in one request: add several documents (/v1/add-batch) and
 * delete several by key and language (/v1/delete-batch).
 */
import { postJson } from './http';

export const batchMethods = {
  /** `documents`: [{ key, lang, contentMd, meta? }] → `{ added, updated, failed, errors }`. */
  addBatch({ collection, documents }) {
    return postJson(this, '/add-batch', { collection, documents });
  },

  /**
   * `documents`: [{ key, lang }] → `{ deleted, not_found, failed, errors }`
   * (the server spells `not_found` in snake case).
   */
  deleteBatch({ collection, documents }) {
    const keys = documents.map(({ key, lang }) => ({ key, lang }));
    return postJson(this, '/delete-batch', { collection, documents: keys });
  },
};
