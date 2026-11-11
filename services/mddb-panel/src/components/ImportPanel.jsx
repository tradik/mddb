import { Import } from 'lucide-react';
import { useBulkJobs } from '../lib/use-bulk-jobs';
import DocumentsJsonForm from './DocumentsJsonForm';
import BulkJobsTable from './BulkJobsTable';
import UrlImportCard from './UrlImportCard';
import WikiImportCard from './WikiImportCard';

/**
 * ImportPanel is the Import page: ways to add documents other than the file
 * upload — one document from a URL, a Wikipedia dump, several documents from
 * JSON at once or as a background job, with the bulk import jobs and their
 * progress.
 */
export default function ImportPanel() {
  const { jobs, listError, track, load } = useBulkJobs();

  return (
    <div className="h-full overflow-y-auto p-6 bg-gray-50 space-y-4">
      <div>
        <h2 className="text-xl font-bold text-gray-900 flex items-center gap-2">
          <Import className="w-5 h-5" aria-hidden="true" /> Import
        </h2>
        <p className="text-sm text-gray-700">
          Add documents without uploading files one by one. Files themselves go through Upload in the document list.
        </p>
      </div>
      <UrlImportCard />
      <WikiImportCard />
      <DocumentsJsonForm onQueued={track} />
      <BulkJobsTable jobs={jobs} error={listError} onRefresh={load} />
    </div>
  );
}
