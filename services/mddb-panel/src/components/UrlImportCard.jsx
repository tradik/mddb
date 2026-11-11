import { useState } from 'react';
import { Link2 } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useAction } from '../lib/use-action';
import { useStore } from '../lib/store';
import ApiErrorNotice from './ApiErrorNotice';
import CollectionNameInput from './CollectionNameInput';

/**
 * UrlImportCard has the server fetch one Markdown document from a URL
 * (/v1/import-url): YAML frontmatter becomes metadata, the key comes from the
 * URL path unless given, and an optional TTL makes it expire. The server
 * refuses private and loopback addresses.
 */
export default function UrlImportCard() {
  const currentCollection = useStore((s) => s.currentCollection);
  const [collection, setCollection] = useState(currentCollection || '');
  const [url, setUrl] = useState('');
  const [lang, setLang] = useState('en');
  const [key, setKey] = useState('');
  const [ttl, setTtl] = useState('');
  const [saved, setSaved] = useState(null);
  const { busy, error, run } = useAction();

  const submit = (e) => {
    e.preventDefault();
    setSaved(null);
    run(async () => setSaved(await mddbClient.importURL({
      collection: collection.trim(),
      url: url.trim(),
      lang: lang.trim() || 'en',
      key: key.trim() || undefined,
      ttl: Number(ttl) || 0,
    })));
  };

  const input = (label, value, setValue, props = {}) => (
    <label className="block text-sm text-gray-800">
      <span className="block mb-1">{label}</span>
      <input value={value} onChange={(e) => setValue(e.target.value)} className="w-full px-3 py-2 border border-gray-300 rounded-lg" {...props} />
    </label>
  );

  return (
    <section aria-labelledby="url-import-title" className="bg-white rounded-lg shadow p-4">
      <h3 id="url-import-title" className="text-base font-semibold text-gray-900 flex items-center gap-2 mb-3">
        <Link2 className="w-4 h-4" aria-hidden="true" /> Import from a URL
      </h3>
      <form onSubmit={submit} className="space-y-3">
        {input('URL of a Markdown document', url, setUrl, { type: 'url', placeholder: 'https://example.com/post.md' })}
        <div className="grid grid-cols-1 md:grid-cols-4 gap-3">
          <CollectionNameInput value={collection} onChange={setCollection} />
          {input('Language', lang, setLang)}
          {input('Key (optional)', key, setKey, { placeholder: 'from the URL path' })}
          {input('Expires after (seconds, optional)', ttl, setTtl, { type: 'number', min: 0 })}
        </div>
        <ApiErrorNotice error={error} collection={collection.trim() || '*'} />
        {saved && <p role="status" className="text-sm text-green-800">Imported {saved.key} ({saved.lang}) into {collection.trim()}.</p>}
        <button
          type="submit"
          disabled={busy || !url.trim() || !collection.trim()}
          className="px-4 py-2 bg-blue-700 text-white rounded-lg hover:bg-blue-800 disabled:opacity-50 text-sm"
        >
          {busy ? 'Importing…' : 'Import'}
        </button>
      </form>
    </section>
  );
}
