import { useCallback, useEffect, useState } from 'react';
import mddbClient from './mddb-client';
import { useJobPolling } from './use-job-polling';

// Bulk ingest jobs (/v1/bulk-ingest-job*): the server queues a batch for one
// FIFO worker and answers 202 with a pending job; the job moves through
// processing to completed, failed or cancelled. Jobs live in server memory,
// newest first, so a restart forgets them.

/** True once a job has stopped. */
export const isBulkDone = (job) => ['completed', 'failed', 'cancelled'].includes(job?.status);

/** One line describing a job's progress or outcome. */
export function describeBulkJob(job) {
  if (job.status === 'cancelled') return 'Cancelled before it started.';
  if (job.status === 'pending') return `Queued: ${job.total} documents.`;
  const counts = `${job.added || 0} added, ${job.updated || 0} updated, ${job.failed || 0} failed`;
  if (job.status === 'processing') return `${job.processed || 0} of ${job.total} processed (${counts}).`;
  if (job.error) return `Failed: ${job.error}`;
  return `${counts} of ${job.total}.`;
}

/** Drops `id` from a map of running jobs. */
const without = (map, id) => {
  const next = { ...map };
  delete next[id];
  return next;
};

/**
 * The job list, with every unfinished job polled until it stops. `track(job)`
 * follows a job just submitted; `load()` re-reads the list.
 */
export function useBulkJobs() {
  const [listed, setListed] = useState([]);
  const [active, setActive] = useState({});
  const [listError, setListError] = useState(null);

  const load = useCallback(() => mddbClient.listBulkIngestJobs()
    .then((data) => {
      const jobs = data?.jobs || [];
      setListed(jobs);
      setListError(null);
      setActive((prev) => jobs.reduce((next, job) => (isBulkDone(job) || next[job.id] ? next : { ...next, [job.id]: job }), prev));
    })
    .catch(setListError), []);

  useEffect(() => {
    load();
  }, [load]);

  const finish = useCallback((id, job) => {
    setActive((prev) => without(prev, id));
    setListed((prev) => prev.map((j) => (j.id === id ? job : j)));
    load();
  }, [load]);

  useJobPolling(active, {
    getId: (job) => job.id,
    fetchJob: (id) => mddbClient.getBulkIngestJob(id),
    isDone: isBulkDone,
    onUpdate: (id, next) => setActive((prev) => (prev[id] ? { ...prev, [id]: next } : prev)),
    onFinish: finish,
  });

  const track = (job) => {
    setListed((prev) => [job, ...prev.filter((j) => j.id !== job.id)]);
    if (!isBulkDone(job)) setActive((prev) => ({ ...prev, [job.id]: job }));
  };

  const jobs = listed.map((job) => active[job.id] || job);
  return { jobs, listError, track, load };
}
