import { useEffect, useRef } from 'react';

/**
 * Polling of server-side background jobs, shared by the vector reindex jobs
 * and the bulk ingest jobs. `jobs` maps a caller-chosen key to the latest
 * record of a running job; while it is not empty every job is fetched each
 * POLL_MS. A record that `isDone` goes to `onFinish(key, job)`, any other to
 * `onUpdate(key, job)`; a failed poll finishes the job as
 * `{ status: 'failed', error }`. The callers keep the state — this hook only
 * owns the timer, which restarts when the set of job ids changes, not on
 * every progress update.
 */

/** How often a running job is polled. */
export const POLL_MS = 2000;

export function useJobPolling(jobs, { getId, fetchJob, isDone, onUpdate, onFinish }) {
  // The latest jobs and callbacks, so a new arrow per render or a progress
  // update does not restart the timer.
  const latest = useRef({ jobs, getId, fetchJob, isDone, onUpdate, onFinish });
  useEffect(() => {
    latest.current = { jobs, getId, fetchJob, isDone, onUpdate, onFinish };
  });

  const running = Object.values(jobs).map(getId).join(',');
  useEffect(() => {
    if (!running) return undefined;
    const timer = setInterval(() => {
      const current = latest.current;
      for (const [key, job] of Object.entries(current.jobs)) {
        current.fetchJob(current.getId(job))
          .then((next) => (current.isDone(next) ? current.onFinish(key, next) : current.onUpdate(key, next)))
          .catch((err) => current.onFinish(key, { ...job, status: 'failed', error: err.message }));
      }
    }, POLL_MS);
    return () => clearInterval(timer);
  }, [running]);
}
