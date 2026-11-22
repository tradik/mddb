import { useState } from 'react';
import { Package, Lock, Trash2, Download, AlertTriangle } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useStore } from '../lib/store';
import { saveBlob } from '../lib/download';
import { useAction } from '../lib/use-action';
import { formatBytes, packFileName, rebuildRequest } from '../lib/corpus-packs';
import ApiErrorNotice from './ApiErrorNotice';
import ConfirmDialog from './ConfirmDialog';

// AGT-005, FE-022: the packs installed on this server, one row per pack —
// a pack of several collections lists them all. Downloading builds the pack
// again from its collections as they were recorded; removing deletes every
// collection the pack installed. Both need admin on "*", like installing.

function PackFacts({ pack }) {
  const size = formatBytes(pack.bytes);
  return (
    <>
      <p className="mt-0.5 text-xs text-fg-subtle">
        {pack.docCount} documents{size && ` · ${size}`} · licence {pack.license}
      </p>
      <p className="mt-0.5 text-xs text-fg-subtle">
        Made with MDDB {pack.mddbVersion}
        {pack.installedAt && `, installed ${new Date(pack.installedAt).toLocaleString()}`}
        {pack.sources.length > 0 && ` · from ${pack.sources.map((s) => s.repo + (s.commit ? ' @ ' + s.commit : '')).join(', ')}`}
      </p>
    </>
  );
}

function PackRow({ pack, canManage, busy, highlighted, onDownload, onRemove, onOpen }) {
  return (
    <li id={`pack-${pack.name}`} className={`py-3 flex flex-wrap items-start justify-between gap-3 ${highlighted ? 'bg-info-muted -mx-2 px-2 rounded' : ''}`}>
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-medium text-fg break-all">{pack.name}</span>
          {pack.version && <span className="text-xs text-fg-subtle">v{pack.version}</span>}
          {pack.readOnly && (
            <span className="inline-flex items-center gap-1 rounded-full bg-surface-subtle px-2 py-0.5 text-xs font-medium text-fg-soft">
              <Lock className="h-3 w-3" aria-hidden="true" /> read-only
            </span>
          )}
        </div>
        {pack.description && <p className="mt-0.5 text-sm text-fg-soft">{pack.description}</p>}
        <PackFacts pack={pack} />
        {pack.several && (
          <p className="mt-1 text-xs text-fg-soft">
            Collections:{' '}
            {pack.collections.map((c, i) => (
              <span key={c.name}>
                {i > 0 && ', '}
                <button type="button" onClick={() => onOpen(c.name)} className="underline text-info-strong hover:text-fg">{c.name}</button>
                {` (${c.docCount}, ${c.license})`}
              </span>
            ))}
          </p>
        )}
        {pack.vectorsDisabled && (
          <p className="mt-1 flex items-center gap-1 text-xs text-warning-strong">
            <AlertTriangle className="h-3 w-3 shrink-0" aria-hidden="true" />
            Made with another embedding model than this server uses: meaning-based search finds nothing here, keyword search works.
          </p>
        )}
      </div>
      {canManage && (
        <div className="flex shrink-0 gap-1">
          <button type="button" onClick={() => onDownload(pack)} disabled={busy} aria-label={`Download pack ${pack.name}`}
            className="rounded p-1.5 text-fg-soft hover:bg-surface-subtle disabled:opacity-50">
            <Download className="h-4 w-4" aria-hidden="true" />
          </button>
          <button type="button" onClick={() => onRemove(pack)} disabled={busy} aria-label={`Remove pack ${pack.name}`}
            className="rounded p-1.5 text-danger hover:bg-danger-soft disabled:opacity-50">
            <Trash2 className="h-4 w-4" aria-hidden="true" />
          </button>
        </div>
      )}
    </li>
  );
}

/**
 * CorpusPacksCard lists `packs` (groupPacks). `onChanged` runs after a
 * removal; `highlight` names a pack to mark (opened from its collection).
 */
export default function CorpusPacksCard({ packs, loadError, canManage, highlight, onChanged }) {
  const { setCurrentCollection, setViewMode } = useStore();
  const { busy, error, run } = useAction();
  const [removing, setRemoving] = useState(null);
  const [message, setMessage] = useState(null);

  const download = (pack) => run(async () => {
    setMessage(null);
    const body = rebuildRequest(pack);
    saveBlob(await mddbClient.buildCorpus(body), packFileName(body));
  });

  const remove = () => run(async () => {
    const res = await mddbClient.removeCorpus(removing.name);
    setMessage(`Removed ${removing.name}: ${res.deleted} documents deleted.`);
    setRemoving(null);
    await onChanged();
  });

  const openCollection = (name) => {
    setCurrentCollection(name);
    setViewMode('documents');
  };

  return (
    <section aria-labelledby="corpus-installed" className="bg-surface rounded-lg shadow p-4 sm:p-6">
      <h3 id="corpus-installed" className="flex items-center gap-2 text-lg font-semibold text-fg mb-2">
        <Package className="w-5 h-5 text-accent" aria-hidden="true" /> Installed packs
      </h3>
      <ApiErrorNotice error={loadError || error} />
      {message && <output className="mb-2 block rounded border border-success-line bg-success-soft p-2 text-sm text-success-strong">{message}</output>}
      {packs.length === 0 ? (
        <p className="text-sm text-fg-subtle">No packs installed on this server.</p>
      ) : (
        <ul className="divide-y divide-line">
          {packs.map((pack) => (
            <PackRow key={pack.name} pack={pack} canManage={canManage} busy={busy} highlighted={pack.name === highlight}
              onDownload={download} onRemove={setRemoving} onOpen={openCollection} />
          ))}
        </ul>
      )}
      {busy && !removing && <p role="status" className="mt-2 text-sm text-fg-muted">Preparing the download… a large pack takes a while.</p>}
      {removing && (
        <ConfirmDialog
          title="Remove a corpus pack"
          message={<>Remove the pack <strong>{removing.name}</strong>? Its collections ({removing.collections.map((c) => c.name).join(', ')}) and their {removing.docCount} documents are deleted from this server. The pack file you installed it from is not affected.</>}
          confirmLabel="Remove"
          busy={busy}
          onConfirm={remove}
          onCancel={() => setRemoving(null)}
        />
      )}
    </section>
  );
}
