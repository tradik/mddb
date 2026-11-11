import { RefreshCw } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { describeBulkJob } from '../lib/use-bulk-jobs';
import { formatUnix } from '../lib/time-format';
import { useAction } from '../lib/use-action';
import ApiErrorNotice from './ApiErrorNotice';

const STATUS_CLASS = {
  pending: 'bg-gray-100 text-gray-900',
  processing: 'bg-blue-100 text-blue-900',
  completed: 'bg-green-100 text-green-900',
  failed: 'bg-red-100 text-red-900',
  cancelled: 'bg-amber-100 text-amber-950',
};

/**
 * BulkJobsTable lists the bulk ingest jobs (/v1/bulk-ingest-jobs), newest
 * first, with live progress for running ones, the first errors of a job and
 * Cancel for a job still pending (/v1/bulk-ingest-job/{id} DELETE).
 */
export default function BulkJobsTable({ jobs, error, onRefresh }) {
  const cancelling = useAction();

  const cancel = (id) => cancelling.run(async () => {
    await mddbClient.cancelBulkIngestJob(id);
    onRefresh();
  });

  return (
    <section aria-labelledby="bulk-jobs-title" className="bg-white rounded-lg shadow">
      <div className="px-4 py-3 border-b border-gray-200 flex items-center justify-between">
        <div>
          <h3 id="bulk-jobs-title" className="text-base font-semibold text-gray-900">Bulk import jobs</h3>
          <p className="text-xs text-gray-700">Kept in server memory; a restart forgets them. One worker runs them in order.</p>
        </div>
        <button type="button" onClick={onRefresh} aria-label="Refresh bulk import jobs" className="p-2 text-gray-800 rounded hover:bg-gray-100">
          <RefreshCw className="w-4 h-4" aria-hidden="true" />
        </button>
      </div>
      <div className="p-4 space-y-2">
        <ApiErrorNotice error={error || cancelling.error} />
        {!error && jobs.length === 0 && <p className="text-sm text-gray-700">No bulk import jobs since the server started.</p>}
      </div>
      {!error && jobs.length > 0 && (
        <div className="overflow-x-auto">
          <table className="min-w-full text-sm" aria-label="Bulk import jobs">
            <thead>
              <tr className="text-left text-gray-800">
                <th scope="col" className="px-4 py-2">Collection</th>
                <th scope="col" className="px-4 py-2">Status</th>
                <th scope="col" className="px-4 py-2">Submitted</th>
                <th scope="col" className="px-4 py-2">Progress</th>
                <th scope="col" className="px-4 py-2"><span className="sr-only">Actions</span></th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {jobs.map((job) => (
                <tr key={job.id}>
                  <td className="px-4 py-2 text-gray-900">{job.collection}</td>
                  <td className="px-4 py-2">
                    <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${STATUS_CLASS[job.status] || STATUS_CLASS.pending}`}>{job.status}</span>
                  </td>
                  <td className="px-4 py-2 text-gray-800">{formatUnix(job.submittedAt)}</td>
                  <td className="px-4 py-2 text-gray-800">
                    {job.status === 'processing' && (
                      <progress max={job.total} value={job.processed || 0} aria-label={`Progress of ${job.collection}`} className="block w-32" />
                    )}
                    {describeBulkJob(job)}
                    {job.errors?.length > 0 && (
                      <details className="text-xs text-red-800">
                        <summary className="cursor-pointer">{job.errors.length} error messages</summary>
                        <ul className="list-disc pl-4">{job.errors.map((e) => <li key={e}>{e}</li>)}</ul>
                      </details>
                    )}
                  </td>
                  <td className="px-4 py-2">
                    {job.status === 'pending' && (
                      <button
                        type="button"
                        onClick={() => cancel(job.id)}
                        disabled={cancelling.busy}
                        className="text-sm text-red-800 underline disabled:opacity-50"
                      >
                        Cancel <span className="sr-only">job for {job.collection}</span>
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
