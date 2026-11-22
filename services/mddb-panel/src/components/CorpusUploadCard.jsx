import { useRef, useState } from 'react';
import { Upload, AlertTriangle } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useAction } from '../lib/use-action';
import { explainPackError } from '../lib/corpus-packs';
import ApiErrorNotice from './ApiErrorNotice';

// FE-022: upload a pack. Choosing a file reads only its description (the
// first megabytes go to /v1/corpus/inspect), so what it holds — and whether
// it can be installed here — is shown before the whole file is uploaded.

/** A refusal in plain words, with the server's own words below. */
function PackError({ error }) {
  const plain = explainPackError(error);
  if (!plain) return <ApiErrorNotice error={error} admin />;
  return (
    <div role="alert" className="rounded-lg border border-danger-line bg-danger-soft p-3 text-sm text-danger-strong space-y-1">
      <p>{plain}</p>
      <p className="text-xs break-words">Server: {error.body?.error || error.message}</p>
    </div>
  );
}

function ManifestSummary({ manifest, conflicts }) {
  const collections = manifest.collections?.map((c) => c.name) || [manifest.name];
  return (
    <div className="rounded-lg border border-line p-3 text-sm text-fg-soft space-y-1">
      <p>
        <strong className="text-fg break-all">{manifest.name}</strong>
        {manifest.version && ` v${manifest.version}`} · licence {manifest.license} · {manifest.docCount} documents
      </p>
      {manifest.description && <p>{manifest.description}</p>}
      <p>Installs as: {collections.join(', ')} (read-only)</p>
      {conflicts.length > 0 && (
        <div className="text-warning-strong">
          <p className="flex items-start gap-1">
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" /> {explainPackError({ status: 409 })}
          </p>
          <ul className="text-xs list-disc pl-5 break-words">{conflicts.map((c) => <li key={c}>{c}</li>)}</ul>
        </div>
      )}
    </div>
  );
}

/** CorpusUploadCard checks and installs a pack file; `onInstalled` refreshes the page. */
export default function CorpusUploadCard({ onInstalled }) {
  const { busy, error, setError, run } = useAction();
  const [file, setFile] = useState(null);
  const [inspected, setInspected] = useState(null);
  const [message, setMessage] = useState(null);
  const input = useRef(null);

  const choose = (chosen) => {
    setFile(chosen);
    setInspected(null);
    setMessage(null);
    setError(null);
    if (chosen) run(async () => setInspected(await mddbClient.inspectCorpus(chosen)));
  };

  const install = () => run(async () => {
    const res = await mddbClient.installCorpus(file);
    const warnings = res.warnings?.length ? ` Note: ${res.warnings.join(' ')}` : '';
    setMessage(`Installed ${res.name}: ${res.docCount} documents in ${(res.collections || [res.name]).join(', ')}, read-only.${warnings}`);
    setFile(null);
    setInspected(null);
    input.current.value = '';
    await onInstalled();
  });

  const ready = inspected?.conflicts.length === 0;
  return (
    <section aria-labelledby="corpus-upload" className="bg-surface rounded-lg shadow p-4 sm:p-6 space-y-3">
      <h3 id="corpus-upload" className="flex items-center gap-2 text-lg font-semibold text-fg">
        <Upload className="w-5 h-5 text-accent" aria-hidden="true" /> Upload a pack
      </h3>
      <p className="text-sm text-fg-muted">Choose a <code>.mddbpack</code> file. You see what it holds before anything is installed.</p>
      <label htmlFor="corpus-file" className="block text-sm text-fg-soft">Pack file</label>
      <input id="corpus-file" ref={input} type="file" accept=".mddbpack"
        onChange={(e) => choose(e.target.files[0] || null)} className="block w-full text-sm text-fg-muted" />
      {inspected && <ManifestSummary {...inspected} />}
      {error && <PackError error={error} />}
      {message && <output className="block rounded border border-success-line bg-success-soft p-2 text-sm text-success-strong">{message}</output>}
      <button type="button" onClick={install} disabled={!ready || busy}
        className="flex items-center px-3 py-1.5 bg-primary-700 text-white text-sm rounded hover:bg-primary-800 disabled:opacity-50">
        <Upload className="w-4 h-4 mr-1.5" aria-hidden="true" />
        {busy && file ? 'Working…' : 'Install'}
      </button>
      {busy && file && <output className="block text-sm text-fg-muted">Reading and checking the pack… a large pack takes a while.</output>}
    </section>
  );
}
