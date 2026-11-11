import { useState } from 'react';
import { Wrench } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useStore } from '../lib/store';
import { useAction } from '../lib/use-action';
import { saveBlob } from '../lib/download';
import ApiErrorNotice from './ApiErrorNotice';
import CollectionPicker from './CollectionPicker';
import ConfirmDialog from './ConfirmDialog';

/**
 * CollectionMaintenanceCard works on one collection: its CRC32 checksum
 * (/v1/checksum — compare it on each replica), an export as NDJSON or a ZIP
 * of Markdown files (/v1/export) and truncating its revision history
 * (/v1/truncate, after a confirmation naming the collection).
 */
export default function CollectionMaintenanceCard() {
  const firstCollection = useStore((s) => s.stats?.collections?.[0]?.name);
  const [picked, setPicked] = useState('');
  const collection = picked || firstCollection || '';
  const [format, setFormat] = useState('ndjson');
  const [keepRevs, setKeepRevs] = useState('3');
  const [confirming, setConfirming] = useState(false);
  const [message, setMessage] = useState(null);
  const { busy, error, run } = useAction();

  const checksum = () => run(async () => {
    const r = await mddbClient.checksum(collection);
    setMessage(`Checksum of ${r.collection}: ${r.checksum} over ${r.documentCount} documents.`);
  });

  const exportFile = () => run(async () => {
    saveBlob(await mddbClient.export({ collection, format }), `${collection}.${format === 'zip' ? 'zip' : 'ndjson'}`);
    setMessage(`Exported ${collection} as ${format === 'zip' ? 'a ZIP of Markdown files' : 'NDJSON'}.`);
  });

  const truncate = () => run(async () => {
    await mddbClient.truncate({ collection, keepRevs: Number(keepRevs) || 0 });
    setConfirming(false);
    setMessage(`Revision history of ${collection} truncated to the last ${Number(keepRevs) || 0} per document.`);
  });

  const button = 'px-3 py-2 text-sm rounded-lg disabled:opacity-50';

  return (
    <div className="bg-white rounded-lg shadow mb-6 p-6 space-y-3">
      <h2 className="text-lg font-semibold text-gray-900 flex items-center gap-2">
        <Wrench className="w-5 h-5 text-primary-600" aria-hidden="true" /> Collection maintenance
      </h2>
      <div className="flex flex-wrap gap-3 items-end">
        <div className="min-w-[10rem]"><CollectionPicker value={collection} onChange={(v) => { setPicked(v); setMessage(null); }} /></div>
        <button type="button" onClick={checksum} disabled={busy || !collection} className={`${button} bg-gray-100 text-gray-900 hover:bg-gray-200`}>Checksum</button>
        <label className="text-sm text-gray-800">
          <span className="block mb-1">Export format</span>
          <select value={format} onChange={(e) => setFormat(e.target.value)} className="px-2 py-2 border border-gray-300 rounded-lg">
            <option value="ndjson">NDJSON</option>
            <option value="zip">ZIP of Markdown files</option>
          </select>
        </label>
        <button type="button" onClick={exportFile} disabled={busy || !collection} className={`${button} bg-gray-100 text-gray-900 hover:bg-gray-200`}>Export</button>
        <label className="text-sm text-gray-800">
          <span className="block mb-1">Revisions to keep</span>
          <input type="number" min="0" value={keepRevs} onChange={(e) => setKeepRevs(e.target.value)} className="w-24 px-3 py-2 border border-gray-300 rounded-lg" />
        </label>
        <button type="button" onClick={() => setConfirming(true)} disabled={busy || !collection} className={`${button} text-red-800 border border-red-300 hover:bg-red-50`}>Truncate revisions</button>
      </div>
      {message && <p role="status" className="text-sm text-gray-900">{message}</p>}
      <ApiErrorNotice error={error} collection={collection} />
      {confirming && (
        <ConfirmDialog
          title="Truncate revisions"
          message={<>Keep only the last {Number(keepRevs) || 0} revisions of each document in <strong>{collection}</strong>? Older revisions are deleted for good.</>}
          confirmLabel="Truncate"
          busy={busy}
          onConfirm={truncate}
          onCancel={() => setConfirming(false)}
        />
      )}
    </div>
  );
}
