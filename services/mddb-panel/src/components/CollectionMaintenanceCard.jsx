import { useState } from 'react';
import { Wrench } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useStore } from '../lib/store';
import { useAction } from '../lib/use-action';
import { saveBlob } from '../lib/download';
import ApiErrorNotice from './ApiErrorNotice';
import CollectionPicker from './CollectionPicker';
import ConfirmDialog from './ConfirmDialog';
import MaintenanceAction from './MaintenanceAction';

const button = 'px-3 py-2 text-sm rounded-lg disabled:opacity-50';
const plain = `${button} bg-surface-subtle text-fg hover:bg-surface-strong`;
const code = (text) => <code className="text-fg-soft">{text}</code>;

/**
 * CollectionMaintenanceCard works on one collection: its checksum
 * (/v1/checksum — compare it on each replica), an export as NDJSON or a ZIP
 * of Markdown files (/v1/export) and truncating its revision history
 * (/v1/truncate, after a confirmation naming the collection). Truncating is
 * off for a read-only collection, which refuses writes.
 */
export default function CollectionMaintenanceCard() {
  const firstCollection = useStore((s) => s.stats?.collections?.[0]?.name);
  const configs = useStore((s) => s.collectionConfigs);
  const [picked, setPicked] = useState('');
  const collection = picked || firstCollection || '';
  const readOnly = configs?.[collection]?.readOnly === true;
  const [format, setFormat] = useState('ndjson');
  const [keepRevs, setKeepRevs] = useState('3');
  const [confirming, setConfirming] = useState(false);
  const [message, setMessage] = useState(null);
  const { busy, error, run } = useAction();
  const keep = Number(keepRevs) || 0;

  const checksum = () => run(async () => {
    const r = await mddbClient.checksum(collection);
    setMessage(`Checksum of ${r.collection}: ${r.checksum} over ${r.documentCount} documents.`);
  });

  const exportFile = () => run(async () => {
    saveBlob(await mddbClient.export({ collection, format }), `${collection}.${format === 'zip' ? 'zip' : 'ndjson'}`);
    setMessage(`Exported ${collection} as ${format === 'zip' ? 'a ZIP of Markdown files' : 'NDJSON'}.`);
  });

  const truncate = () => run(async () => {
    await mddbClient.truncate({ collection, keepRevs: keep });
    setConfirming(false);
    setMessage(`Revision history of ${collection} truncated to the last ${keep} per document.`);
  });

  return (
    <section aria-labelledby="maintenance-title" className="bg-surface rounded-lg shadow p-6 space-y-4">
      <h2 id="maintenance-title" className="text-lg font-semibold text-fg flex items-center gap-2">
        <Wrench className="w-5 h-5 text-accent" aria-hidden="true" /> Maintenance
      </h2>
      <div className="max-w-xs"><CollectionPicker value={collection} onChange={(v) => { setPicked(v); setMessage(null); }} /></div>

      <MaintenanceAction
        title="Checksum"
        text="A fingerprint of the collection's documents. Compare it between two servers to see whether they hold the same content."
        help={<p>{code('GET /v1/checksum')}: a hash over every document. Equal on the leader and each follower once replication caught up. Needs read on the collection.</p>}
      >
        <button type="button" onClick={checksum} disabled={busy || !collection} className={plain}>Checksum</button>
      </MaintenanceAction>

      <MaintenanceAction
        title="Export"
        text="Downloads every document of the collection as one file, to keep a copy of it or load it into another server."
        help={<p>{code('POST /v1/export')}: NDJSON keeps the metadata on each line and loads back with Import; a ZIP holds one Markdown file per document. Needs read on the collection.</p>}
      >
        <label className="text-sm text-fg-soft">
          <span className="block mb-1">Export format</span>
          <select value={format} onChange={(e) => setFormat(e.target.value)} className="px-2 py-2 border border-line-strong rounded-lg">
            <option value="ndjson">NDJSON</option>
            <option value="zip">ZIP of Markdown files</option>
          </select>
        </label>
        <button type="button" onClick={exportFile} disabled={busy || !collection} className={plain}>Export</button>
      </MaintenanceAction>

      <MaintenanceAction
        title="Truncate revisions"
        text="Deletes the older versions of each document and keeps the newest ones. Use it when the history takes too much space; deleted versions cannot be brought back."
        help={<p>{code('POST /v1/truncate')} with {code('keepRevs')}, the number of revisions kept per document. Needs write on the collection, and a read-only collection refuses it.</p>}
      >
        <label className="text-sm text-fg-soft">
          <span className="block mb-1">Revisions to keep</span>
          <input type="number" min="0" value={keepRevs} onChange={(e) => setKeepRevs(e.target.value)} className="w-24 px-3 py-2 border border-line-strong rounded-lg" />
        </label>
        <button type="button" onClick={() => setConfirming(true)} disabled={busy || !collection || readOnly} className={`${button} text-danger-strong border border-danger-line hover:bg-danger-soft`}>Truncate revisions</button>
        {readOnly && <p className="text-sm text-fg-muted w-full">This collection is read-only, so its history cannot be changed.</p>}
      </MaintenanceAction>

      {message && <p role="status" className="text-sm text-fg">{message}</p>}
      <ApiErrorNotice error={error} collection={collection} />
      {confirming && (
        <ConfirmDialog
          title="Truncate revisions"
          message={<>Keep only the last {keep} revisions of each document in <strong>{collection}</strong>? Older revisions are deleted for good.</>}
          confirmLabel="Truncate"
          busy={busy}
          onConfirm={truncate}
          onCancel={() => setConfirming(false)}
        />
      )}
    </section>
  );
}
