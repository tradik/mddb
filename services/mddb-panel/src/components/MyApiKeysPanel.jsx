import { useCallback, useEffect, useState } from 'react';
import { KeyRound } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useAction } from '../lib/use-action';
import { formatUnix, dateInputToUnix } from '../lib/time-format';
import ApiErrorNotice from './ApiErrorNotice';
import ConfirmDialog from './ConfirmDialog';
import OneTimeSecret from './OneTimeSecret';

/**
 * MyApiKeysPanel manages the signed-in user's own API keys
 * (/v1/auth/api-key, /v1/auth/api-keys): create one with a description and
 * an optional expiry date — its secret is shown once — list them by hash and
 * revoke one after a confirmation. A key acts with its owner's permissions
 * (X-API-Key header). MCP keys are managed under LLM Connections.
 */
export default function MyApiKeysPanel() {
  const [keys, setKeys] = useState(null);
  const [loadError, setLoadError] = useState(null);
  const [description, setDescription] = useState('');
  const [expires, setExpires] = useState('');
  const [secret, setSecret] = useState(null);
  const [revoking, setRevoking] = useState(null);
  const { busy, error, run } = useAction();

  const load = useCallback(() => mddbClient.listApiKeys()
    .then((data) => {
      setKeys(data.keys || []);
      setLoadError(null);
    })
    .catch(setLoadError), []);

  useEffect(() => {
    load();
  }, [load]);

  const create = (e) => {
    e.preventDefault();
    run(async () => {
      const created = await mddbClient.createApiKey({ description: description.trim(), expiresAt: dateInputToUnix(expires) });
      setSecret(created.key);
      setDescription('');
      setExpires('');
      load();
    });
  };

  const revoke = () => run(async () => {
    await mddbClient.deleteApiKey(revoking.keyHash);
    setRevoking(null);
    load();
  });

  return (
    <section aria-labelledby="my-keys-title" className="bg-white rounded-lg shadow p-4 space-y-3">
      <h3 id="my-keys-title" className="text-base font-semibold text-gray-900 flex items-center gap-2">
        <KeyRound className="w-4 h-4" aria-hidden="true" /> My API keys
      </h3>
      <p className="text-sm text-gray-700">
        Keys for scripts and integrations, sent as the X-API-Key header. A key has your permissions; revoke it when it is no longer used.
      </p>
      <form onSubmit={create} className="flex flex-wrap gap-2 items-end">
        <label className="flex-1 min-w-[12rem] text-sm text-gray-800">
          <span className="block mb-1">Description</span>
          <input value={description} onChange={(e) => setDescription(e.target.value)} placeholder="CI pipeline" className="w-full px-3 py-2 border border-gray-300 rounded-lg" />
        </label>
        <label className="text-sm text-gray-800">
          <span className="block mb-1">Expires on (optional)</span>
          <input type="date" value={expires} onChange={(e) => setExpires(e.target.value)} className="px-3 py-2 border border-gray-300 rounded-lg" />
        </label>
        <button type="submit" disabled={busy || !description.trim()} className="px-4 py-2 bg-blue-700 text-white rounded-lg hover:bg-blue-800 disabled:opacity-50 text-sm">
          Create key
        </button>
      </form>
      {secret && <OneTimeSecret secret={secret} onDismiss={() => setSecret(null)} />}
      <ApiErrorNotice error={error || loadError} />
      {keys?.length === 0 && <p className="text-sm text-gray-700">You have no API keys.</p>}
      {keys?.length > 0 && (
        <table className="min-w-full text-sm" aria-label="My API keys">
          <thead>
            <tr className="text-left text-gray-800">
              <th scope="col" className="py-2 pr-3">Description</th>
              <th scope="col" className="py-2 pr-3">Key hash</th>
              <th scope="col" className="py-2 pr-3">Created</th>
              <th scope="col" className="py-2 pr-3">Expires</th>
              <th scope="col" className="py-2"><span className="sr-only">Actions</span></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100">
            {keys.map((k) => (
              <tr key={k.keyHash}>
                <td className="py-2 pr-3 text-gray-900">{k.description || '—'}</td>
                <td className="py-2 pr-3 font-mono text-xs text-gray-800" title={k.keyHash}>{k.keyHash.slice(0, 12)}…</td>
                <td className="py-2 pr-3 text-gray-800">{formatUnix(k.createdAt)}</td>
                <td className="py-2 pr-3 text-gray-800">{k.expiresAt ? formatUnix(k.expiresAt) : 'Never'}</td>
                <td className="py-2">
                  <button type="button" onClick={() => setRevoking(k)} className="text-red-800 underline">
                    Revoke <span className="sr-only">{k.description || k.keyHash.slice(0, 12)}</span>
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {revoking && (
        <ConfirmDialog
          title="Revoke API key"
          message={<>Revoke the key <strong>{revoking.description || revoking.keyHash.slice(0, 12)}</strong>? Requests that send it are refused from now on.</>}
          confirmLabel="Revoke"
          busy={busy}
          onConfirm={revoke}
          onCancel={() => setRevoking(null)}
        />
      )}
    </section>
  );
}
