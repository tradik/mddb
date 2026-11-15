import { useState } from 'react';
import { X, Loader2 } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import CollectionNameInput from './CollectionNameInput';
import ApiErrorNotice from './ApiErrorNotice';
import LineDiff from './LineDiff';
import MetaDiff from './MetaDiff';

/**
 * DocumentCompare is "Compare with…" (FE-013): the open document against
 * any other one — another language of the same key, another key, another
 * collection — as the metadata keys and content lines that differ. The
 * other document is read with the caller's permissions; a refusal or a
 * missing document shows as the server worded it.
 */
export default function DocumentCompare({ document, onClose }) {
  const [collection, setCollection] = useState(document.collection || '');
  const [key, setKey] = useState(document.key);
  const [lang, setLang] = useState('');
  const [other, setOther] = useState(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);

  const compare = async (e) => {
    e.preventDefault();
    setLoading(true);
    setError(null);
    setOther(null);
    try {
      const doc = await mddbClient.getDocument({ collection, key: key.trim(), lang: lang.trim() });
      setOther({ ...doc, collection });
    } catch (err) {
      setError(err);
    } finally {
      setLoading(false);
    }
  };

  const ready = collection.trim() && key.trim() && lang.trim();
  return (
    <div className="fixed inset-0 bg-surface z-50 flex flex-col">
      <div className="border-b border-line p-4 flex items-center justify-between">
        <div className="min-w-0">
          <h2 className="text-xl font-semibold text-fg">Compare with…</h2>
          <p className="text-sm text-fg-muted mt-1 break-words">
            {document.collection} / {document.key} ({document.lang})
          </p>
        </div>
        <button onClick={onClose} aria-label="Close comparison" className="p-2 text-fg-subtle hover:text-fg-soft rounded-lg hover:bg-surface-subtle">
          <X className="w-5 h-5" />
        </button>
      </div>
      <div className="flex-1 overflow-y-auto p-6 space-y-4">
        <form onSubmit={compare} className="grid gap-3 sm:grid-cols-[1fr_1fr_8rem_auto] sm:items-end">
          <CollectionNameInput value={collection} onChange={setCollection} />
          <label className="block text-sm text-fg-soft">
            <span className="block mb-1">Key</span>
            <input value={key} onChange={(e) => setKey(e.target.value)} className="w-full px-3 py-2 border border-line-strong rounded-lg" />
          </label>
          <label className="block text-sm text-fg-soft">
            <span className="block mb-1">Language</span>
            <input value={lang} onChange={(e) => setLang(e.target.value)} placeholder="e.g. pl_PL" className="w-full px-3 py-2 border border-line-strong rounded-lg" />
          </label>
          <button
            type="submit"
            disabled={!ready || loading}
            className="flex items-center justify-center gap-2 px-4 py-2 text-sm text-white bg-primary-700 rounded-lg hover:bg-primary-800 disabled:opacity-50"
          >
            {loading && <Loader2 className="w-4 h-4 animate-spin" />}
            Compare
          </button>
        </form>
        <ApiErrorNotice error={error} collection={collection} />
        {other && (
          <div className="space-y-4">
            <p className="text-sm text-fg-soft">
              From <strong>{document.key} ({document.lang})</strong> to <strong>{other.collection} / {other.key} ({other.lang})</strong>:
            </p>
            <MetaDiff before={document.meta} after={other.meta} label="Metadata" />
            <LineDiff before={document.contentMd || ''} after={other.contentMd || ''} label="Content" />
          </div>
        )}
      </div>
    </div>
  );
}
