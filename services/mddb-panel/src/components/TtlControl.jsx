import { useState } from 'react';
import mddbClient from '../lib/mddb-client';
import { useStore } from '../lib/store';
import { useAction } from '../lib/use-action';
import { formatUnix } from '../lib/time-format';
import ApiErrorNotice from './ApiErrorNotice';

/**
 * TtlControl sets or clears a document's time-to-live (/v1/set-ttl): the
 * document expires that many seconds from now; 0 keeps it for good. The
 * answer is the updated document, which replaces the one in the viewer.
 */
export default function TtlControl({ document: doc }) {
  const setCurrentDocument = useStore((s) => s.setCurrentDocument);
  const [seconds, setSeconds] = useState('86400');
  const { busy, error, run } = useAction();

  const apply = (ttl) => run(async () => {
    const updated = await mddbClient.setTTL({ collection: doc.collection, key: doc.key, lang: doc.lang, ttl });
    setCurrentDocument({ ...doc, ...updated, expiresAt: updated.expiresAt });
  });

  return (
    <div className="space-y-2 text-sm">
      <p role="status" className="text-gray-800">
        {doc.expiresAt ? `Expires ${formatUnix(doc.expiresAt)}.` : 'Does not expire.'}
      </p>
      <div className="flex flex-wrap items-end gap-2">
        <label className="text-gray-800">
          <span className="block mb-1">Expire after (seconds)</span>
          <input type="number" min="1" value={seconds} onChange={(e) => setSeconds(e.target.value)} className="w-32 px-2 py-1.5 border border-gray-300 rounded" />
        </label>
        <button type="button" onClick={() => apply(Number(seconds))} disabled={busy || !(Number(seconds) > 0)} className="px-3 py-1.5 bg-blue-700 text-white rounded hover:bg-blue-800 disabled:opacity-50">
          Set expiry
        </button>
        {doc.expiresAt > 0 && (
          <button type="button" onClick={() => apply(0)} disabled={busy} className="px-3 py-1.5 bg-gray-100 text-gray-900 rounded hover:bg-gray-200 disabled:opacity-50">
            Remove expiry
          </button>
        )}
      </div>
      <ApiErrorNotice error={error} collection={doc.collection} />
    </div>
  );
}
