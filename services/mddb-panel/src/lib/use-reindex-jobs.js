import { useCallback, useEffect, useRef, useState } from 'react';
import mddbClient from './mddb-client';
import { formatEta } from './storage-format';
import { useJobPolling, POLL_MS } from './use-job-polling';

// Vector reindex as background jobs (2.18.4): POST /v1/vector-reindex with
// async:true answers 202 with a job, GET /v1/vector-reindex/jobs/{id} reports
// its progress and GET /v1/vector-reindex/jobs lists the last 50. A second
// reindex of a collection while one runs is 409. Jobs live in server memory,
// so a restart forgets them (the vectors written stay).

/** How often a running job is polled (shared with the bulk ingest jobs). */
export { POLL_MS };

/** A reindex job's id. */
const jobIdOf = (job) => job.jobId;

/** True once a job has stopped. */
export const isFinished = (job) => job?.status === 'completed' || job?.status === 'failed';

/** One line describing a job's progress or outcome. */
export function describeJob(job) {
  if (job.status === 'failed') return `Reindex failed: ${job.error || 'unknown error'}`;
  if (job.status === 'completed') {
    const r = job.result || {};
    const parts = [`${r.embedded ?? job.done ?? 0} embedded`];
    if (r.skipped) parts.push(`${r.skipped} unchanged`);
    if (r.failed) parts.push(`${r.failed} failed`);
    return `Reindexed: ${parts.join(', ')} of ${r.total ?? job.done ?? 0}`;
  }
  const eta = formatEta(job.etaSeconds);
  return `Reindexing… ${Math.round(job.percent || 0)}% (${job.done || 0} done${eta ? `, about ${eta} left` : ''})`;
}

/**
 * Starts reindex jobs, polls the running ones and keeps the recent-jobs list.
 * `onFinished` runs when a job stops (the panel reloads its stats then).
 */
export function useReindexJobs(onFinished) {
  const [active, setActive] = useState({});
  const [messages, setMessages] = useState({});
  const [recent, setRecent] = useState([]);
  const [recentError, setRecentError] = useState(null);
  // The latest callback, so a new arrow per render does not restart polling.
  const finishedRef = useRef(onFinished);
  useEffect(() => {
    finishedRef.current = onFinished;
  });

  const loadRecent = useCallback(() => mddbClient
    .listVectorReindexJobs()
    .then((data) => {
      setRecent(data?.jobs || []);
      setRecentError(null);
    })
    .catch((err) => setRecentError(err.message)), []);

  useEffect(() => {
    loadRecent();
  }, [loadRecent]);

  const finish = useCallback((collection, job) => {
    setActive((prev) => {
      const next = { ...prev };
      delete next[collection];
      return next;
    });
    setMessages((prev) => ({ ...prev, [collection]: { success: job.status === 'completed', message: describeJob(job) } }));
    loadRecent();
    finishedRef.current?.();
  }, [loadRecent]);

  useJobPolling(active, {
    getId: jobIdOf,
    fetchJob: (id) => mddbClient.getVectorReindexJob(id),
    isDone: isFinished,
    onUpdate: (collection, next) => setActive((prev) => (prev[collection] ? { ...prev, [collection]: next } : prev)),
    onFinish: finish,
  });

  const start = async (collection, force) => {
    setMessages((prev) => {
      const next = { ...prev };
      delete next[collection];
      return next;
    });
    try {
      const job = await mddbClient.startVectorReindexJob({ collection, force });
      if (isFinished(job)) finish(collection, job);
      else setActive((prev) => ({ ...prev, [collection]: job }));
    } catch (err) {
      const message = err.status === 409
        ? `A reindex of ${collection} is already running.`
        : err.message;
      setMessages((prev) => ({ ...prev, [collection]: { success: false, message } }));
      if (err.status === 409 && err.body?.jobId) {
        setActive((prev) => ({ ...prev, [collection]: err.body }));
      }
    }
  };

  return { active, messages, recent, recentError, start, loadRecent };
}
