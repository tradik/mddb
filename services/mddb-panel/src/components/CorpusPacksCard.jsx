import { useEffect, useRef, useState } from 'react';
import { Package, Lock, Upload, Trash2, Download, AlertTriangle } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useStore } from '../lib/store';
import { saveBlob } from '../lib/download';

// AGT-005: a corpus pack is a portable snapshot of a collection — documents,
// full-text index, vectors, geo points — installed read-only on any server.
// Listing is open to readers; building, installing and removing need admin on
// "*". A pack embedded by another model than the server's installs with
// vectorsDisabled: its hybrid search falls back to full text.

const inputClass = 'w-full px-2 py-1.5 border border-gray-300 rounded text-sm focus:outline-none focus:ring-2 focus:ring-primary-500';

function PackRow({ pack, onRemove, removing }) {
  const info = pack.corpus;
  return (
    <li className="py-3 flex items-start justify-between gap-3">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-medium text-gray-900">{pack.collection}</span>
          {info.version && <span className="text-xs text-gray-600">v{info.version}</span>}
          {pack.readOnly && (
            <span className="inline-flex items-center gap-1 rounded-full bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-800">
              <Lock className="h-3 w-3" aria-hidden="true" /> read-only
            </span>
          )}
        </div>
        <div className="mt-0.5 text-xs text-gray-600">
          {info.docCount} documents · licence {info.license} · built by MDDB {info.mddbVersion}
          {info.installedAt && ` · installed ${new Date(info.installedAt).toLocaleString()}`}
        </div>
        {info.vectorsDisabled && (
          <div className="mt-1 flex items-center gap-1 text-xs text-amber-800">
            <AlertTriangle className="h-3 w-3" aria-hidden="true" />
            Embedded by another model than this server&apos;s: vector search finds nothing here, hybrid uses full text.
          </div>
        )}
      </div>
      <button
        type="button"
        onClick={() => onRemove(pack.collection)}
        disabled={removing}
        aria-label={`Remove pack ${pack.collection}`}
        className="shrink-0 rounded p-1.5 text-red-700 hover:bg-red-50 disabled:opacity-50"
      >
        <Trash2 className="h-4 w-4" />
      </button>
    </li>
  );
}

export default function CorpusPacksCard() {
  const { stats, setStats } = useStore();
  const [packs, setPacks] = useState([]);
  const [error, setError] = useState(null);
  const [message, setMessage] = useState(null);
  const [busy, setBusy] = useState(null);
  const [file, setFile] = useState(null);
  const [build, setBuild] = useState({ collection: '', license: '', version: '' });
  const fileInput = useRef(null);

  const load = () => mddbClient
    .listCorpus()
    .then((data) => setPacks(data.packs || []))
    .catch((err) => setError(err.message));

  useEffect(() => {
    load();
  }, []);

  // Runs one admin action, then reloads the packs and the collection list.
  const run = async (name, action) => {
    setBusy(name);
    setError(null);
    setMessage(null);
    try {
      setMessage(await action());
      await load();
      setStats(await mddbClient.getStats());
    } catch (err) {
      setError(err.message);
    } finally {
      setBusy(null);
    }
  };

  const install = () => run('install', async () => {
    const res = await mddbClient.installCorpus(file);
    setFile(null);
    fileInput.current.value = '';
    const warnings = res.warnings?.length ? ` ${res.warnings.join(' ')}` : '';
    return `Installed ${res.name} (${res.docCount} documents), read-only.${warnings}`;
  });

  const remove = (name) => {
    if (!confirm(`Remove the pack "${name}" and its collection? Its documents are deleted from this server.`)) return;
    run('remove', async () => {
      const res = await mddbClient.removeCorpus(name);
      return `Removed ${name} (${res.deleted} documents deleted).`;
    });
  };

  const buildPack = async (e) => {
    e.preventDefault();
    setBusy('build');
    setError(null);
    setMessage(null);
    try {
      saveBlob(await mddbClient.buildCorpus(build), `${build.collection}.mddbpack`);
    } catch (err) {
      setError(err.message);
    } finally {
      setBusy(null);
    }
  };

  const collections = (stats?.collections || []).map((c) => c.name);

  return (
    <div className="bg-white rounded-lg shadow mb-6 p-6">
      <div className="flex items-center mb-2">
        <Package className="w-5 h-5 text-primary-600 mr-2" aria-hidden="true" />
        <h2 className="text-lg font-semibold text-gray-900">Corpus packs</h2>
      </div>
      <p className="text-sm text-gray-600 mb-3">
        Read-only collections installed from a <code>.mddbpack</code> — no re-embedding or reindexing.
        Every write to them is refused (403 READ_ONLY); remove the pack to use the name again.
        Installing, removing and building need admin on &quot;*&quot;.
      </p>

      {error && <p role="alert" className="mb-3 rounded border border-red-200 bg-red-50 p-2 text-sm text-red-800">{error}</p>}
      {message && <p role="status" className="mb-3 rounded border border-green-200 bg-green-50 p-2 text-sm text-green-800">{message}</p>}

      {packs.length === 0 ? (
        <p className="text-sm text-gray-600">No packs installed.</p>
      ) : (
        <ul className="divide-y divide-gray-200">
          {packs.map((pack) => (
            <PackRow key={pack.collection} pack={pack} onRemove={remove} removing={busy === 'remove'} />
          ))}
        </ul>
      )}

      <div className="mt-4 grid gap-4 md:grid-cols-2">
        <div>
          <h3 className="text-sm font-medium text-gray-800 mb-1">Install a pack</h3>
          <label htmlFor="corpus-file" className="sr-only">Pack file</label>
          <input
            id="corpus-file"
            ref={fileInput}
            type="file"
            accept=".mddbpack"
            onChange={(e) => setFile(e.target.files[0] || null)}
            className="block w-full text-sm text-gray-700"
          />
          <button
            type="button"
            onClick={install}
            disabled={!file || busy !== null}
            className="mt-2 flex items-center px-3 py-1.5 bg-primary-700 text-white text-sm rounded hover:bg-primary-800 disabled:opacity-50"
          >
            <Upload className="w-4 h-4 mr-1.5" aria-hidden="true" />
            {busy === 'install' ? 'Installing…' : 'Install'}
          </button>
        </div>

        <form onSubmit={buildPack}>
          <h3 className="text-sm font-medium text-gray-800 mb-1">Build a pack</h3>
          <div className="grid grid-cols-3 gap-2">
            <div>
              <label htmlFor="corpus-collection" className="block text-xs text-gray-600">Collection</label>
              <select
                id="corpus-collection"
                value={build.collection}
                onChange={(e) => setBuild({ ...build, collection: e.target.value })}
                className={inputClass}
              >
                <option value="">Select…</option>
                {collections.map((name) => <option key={name} value={name}>{name}</option>)}
              </select>
            </div>
            <div>
              <label htmlFor="corpus-license" className="block text-xs text-gray-600">Licence</label>
              <input
                id="corpus-license"
                value={build.license}
                onChange={(e) => setBuild({ ...build, license: e.target.value })}
                placeholder="CC-BY-4.0"
                className={inputClass}
              />
            </div>
            <div>
              <label htmlFor="corpus-version" className="block text-xs text-gray-600">Version</label>
              <input
                id="corpus-version"
                value={build.version}
                onChange={(e) => setBuild({ ...build, version: e.target.value })}
                placeholder="optional"
                className={inputClass}
              />
            </div>
          </div>
          <button
            type="submit"
            disabled={!build.collection || !build.license.trim() || busy !== null}
            className="mt-2 flex items-center px-3 py-1.5 bg-gray-100 text-gray-800 text-sm rounded hover:bg-gray-200 disabled:opacity-50"
          >
            <Download className="w-4 h-4 mr-1.5" aria-hidden="true" />
            {busy === 'build' ? 'Building…' : 'Build and download'}
          </button>
        </form>
      </div>
    </div>
  );
}
