/**
 * Bulk ingest jobs: queue a batch of documents for the server's background
 * worker (202 with the job), poll one job, list jobs, and cancel a job that
 * is still pending. Jobs live in server memory, newest first.
 */
import { postJson, getWithQuery } from './http';

export const bulkIngestMethods = {
  /** Queues `documents` (the /v1/add-batch shape) into `collection`. */
  startBulkIngestJob({ collection, documents }) {
    return postJson(this, '/bulk-ingest-job', { collection, documents });
  },

  /** One job's counters, timestamps and up to 50 error messages. */
  getBulkIngestJob(id) {
    return this.request(`/bulk-ingest-job/${encodeURIComponent(id)}`, { method: 'GET' });
  },

  /** Jobs, optionally of one collection: `{ jobs, total }` (`jobs` is null when empty). */
  listBulkIngestJobs({ collection } = {}) {
    return getWithQuery(this, '/bulk-ingest-jobs', { collection });
  },

  /** Cancels a pending job; a started one answers 400. */
  cancelBulkIngestJob(id) {
    return this.request(`/bulk-ingest-job/${encodeURIComponent(id)}`, { method: 'DELETE' });
  },
};
