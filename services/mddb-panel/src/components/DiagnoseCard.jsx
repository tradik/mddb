import { useState } from 'react';
import { Stethoscope, Download } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { saveBlob } from '../lib/download';

// OPS-025: GET /v1/admin/diagnose returns a tar.gz to attach to an issue —
// build, config, stats, storage, metrics, query shapes and error classes,
// with no document content, metadata values, keys, queries, tokens or IPs.
// Collection names are hashed unless keepNames is set. Admin on "*" only.
const OPTIONS = [
  { name: 'profiles', label: 'Runtime counters (goroutines, heap, GC)' },
  { name: 'integrity', label: 'Integrity check of every database file (slow on large databases)' },
  { name: 'keepNames', label: 'Keep collection names (otherwise c_ + hash)' },
];

export default function DiagnoseCard() {
  const [options, setOptions] = useState({ profiles: false, integrity: false, keepNames: false });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(null);

  const download = async () => {
    setBusy(true);
    setError(null);
    try {
      saveBlob(await mddbClient.downloadDiagnose(options), 'mddb-diagnose.tar.gz');
    } catch (err) {
      setError(err.message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="bg-surface rounded-lg shadow mb-6 p-6">
      <div className="flex items-center mb-2">
        <Stethoscope className="w-5 h-5 text-accent mr-2" aria-hidden="true" />
        <h2 className="text-lg font-semibold text-fg">Diagnose bundle</h2>
      </div>
      <p className="text-sm text-fg-subtle mb-3">
        A tar.gz to attach to a bug report: configuration, statistics, metrics and error classes, without
        document content, metadata values, keys, queries, tokens or IP addresses. MANIFEST.json lists every
        file and the redaction applied. Needs admin on &quot;*&quot;.
      </p>
      <fieldset className="space-y-1.5 mb-4">
        <legend className="sr-only">Bundle options</legend>
        {OPTIONS.map((opt) => (
          <label key={opt.name} className="flex items-center gap-2 text-sm text-fg-muted">
            <input
              type="checkbox"
              checked={options[opt.name]}
              onChange={(e) => setOptions({ ...options, [opt.name]: e.target.checked })}
              className="rounded border-line-strong text-accent focus:ring-focus"
            />
            {opt.label}
          </label>
        ))}
      </fieldset>
      {error && (
        <p role="alert" className="mb-3 rounded border border-danger-line bg-danger-soft p-2 text-sm text-danger-strong">{error}</p>
      )}
      <button
        type="button"
        onClick={download}
        disabled={busy}
        className="flex items-center px-4 py-2 bg-primary-700 text-white rounded hover:bg-primary-800 disabled:opacity-50"
      >
        <Download className="w-4 h-4 mr-2" aria-hidden="true" />
        {busy ? 'Collecting…' : 'Download diagnose bundle'}
      </button>
    </div>
  );
}
