import { Download, Trash2 } from 'lucide-react';
import { statusLabel, formatBytes } from '../lib/training';
import { secondaryButton, hintClass } from './training-styles';

/**
 * A dataset's state and, once built, its summary: the records and the split,
 * what each collection gave and under which licence, the first records, and
 * the two parts to download.
 */
export default function TrainingDatasetSummary({ dataset, onDownload, onDelete }) {
  const sum = dataset.summary;
  return (
    <div className="rounded border border-line p-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="min-w-0">
          <span className="font-medium text-fg break-all">{dataset.name || dataset.id}</span>{' '}
          <span className="text-sm text-fg-soft">· {statusLabel(dataset.status)}</span>
        </div>
        <div className="flex flex-wrap gap-2">
          {sum && ['train', 'validation'].map((part) => (
            <button key={part} type="button" className={secondaryButton} onClick={() => onDownload(dataset.id, part)}>
              <Download className="h-4 w-4" aria-hidden="true" /> {part === 'train' ? 'Training part' : 'Validation part'}
            </button>
          ))}
          <button type="button" className={secondaryButton} onClick={() => onDelete(dataset.id)} aria-label={`Delete dataset ${dataset.name || dataset.id}`}>
            <Trash2 className="h-4 w-4" aria-hidden="true" />
          </button>
        </div>
      </div>
      {dataset.error && <p className="mt-2 text-sm text-danger-strong">{dataset.error}</p>}
      {sum && (
        <>
          <p className="mt-2 text-sm text-fg">
            {sum.records} records ({sum.format}): {sum.trainRecords} to train on, {sum.validationRecords} held back · {formatBytes(sum.bytes)}
          </p>
          {sum.unlicensedAcknowledged?.length > 0 && (
            <p className="mt-1 text-sm text-warning-strong">Without licence information, acknowledged: {sum.unlicensedAcknowledged.join(', ')}</p>
          )}
          <div className="mt-2 overflow-x-auto">
            <table className="min-w-full text-sm">
              <thead>
                <tr className="text-left text-fg-soft">
                  <th className="pr-3 font-medium">Collection</th><th className="pr-3 font-medium">Licence</th>
                  <th className="pr-3 font-medium">Documents</th><th className="pr-3 font-medium">Records</th>
                  <th className="pr-3 font-medium">Duplicates</th><th className="font-medium">Skipped</th>
                </tr>
              </thead>
              <tbody>
                {sum.collections.map((c) => (
                  <tr key={c.name} className="text-fg">
                    <td className="pr-3">{c.name}</td><td className="pr-3">{c.license || 'none recorded'}</td>
                    <td className="pr-3">{c.documents}</td><td className="pr-3">{c.records}</td>
                    <td className="pr-3">{c.duplicates}</td><td>{c.skipped}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <p className={`${hintClass} mt-2`}>The first records:</p>
          <pre className="mt-1 max-h-48 overflow-auto whitespace-pre-wrap break-words rounded bg-surface-muted p-2 text-xs text-fg">
            {sum.preview.map((r) => JSON.stringify(r)).join('\n')}
          </pre>
        </>
      )}
    </div>
  );
}
