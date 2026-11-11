import { useState } from 'react';
import { KeyRound, Trash2 } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { inputClass, labelClass } from '../lib/chat-scenario';

// MCP-005: the credentials of external MCP servers. Write-only: the server
// never returns a value, so the card lists names and lets an administrator
// set, replace or delete one. A scenario's server names one as its
// credential. `secrets` is null when the list was refused (admin only).

export default function ChatMcpSecretsCard({ secrets, onChanged }) {
  const [name, setName] = useState('');
  const [header, setHeader] = useState('');
  const [value, setValue] = useState('');
  const [error, setError] = useState(null);
  const [message, setMessage] = useState(null);

  const run = async (action) => {
    setError(null);
    setMessage(null);
    try {
      setMessage(await action());
      await onChanged();
    } catch (err) {
      setError(err.message);
    }
  };
  const save = (e) => {
    e.preventDefault();
    const key = name.trim();
    void run(async () => {
      await mddbClient.putChatMcpSecret(key, { value, header: header.trim() });
      setValue('');
      return `Saved the secret ${key}.`;
    });
  };
  const remove = (secret) => {
    if (!confirm(`Delete the secret "${secret}"? A server using it can no longer authenticate.`)) return;
    void run(async () => {
      await mddbClient.deleteChatMcpSecret(secret);
      return `Deleted ${secret}.`;
    });
  };

  return (
    <section aria-labelledby="chat-mcp-secrets-heading" className="mt-4 rounded-lg bg-white p-4 shadow">
      <h3 id="chat-mcp-secrets-heading" className="flex items-center gap-2 text-sm font-semibold text-gray-900">
        <KeyRound className="h-4 w-4 text-primary-700" aria-hidden="true" /> MCP secrets
      </h3>
      <p className="mt-1 text-xs text-gray-600">
        Tokens for external MCP servers. Values are write-only: they are never shown again, only replaced.
      </p>
      {secrets === null ? (
        <p className="mt-2 text-sm text-gray-700">Secrets need admin on &quot;*&quot;.</p>
      ) : (
        <>
          {secrets.length === 0 ? (
            <p className="mt-2 text-sm text-gray-700">No secrets yet.</p>
          ) : (
            <ul className="mt-2 divide-y divide-gray-200 text-sm text-gray-800">
              {secrets.map((s) => (
                <li key={s.name} className="flex items-center gap-2 py-1">
                  <span className="font-medium">{s.name}</span>
                  <span className="text-xs text-gray-600">{s.header || 'Authorization: Bearer'} · set</span>
                  {s.usedBy?.length > 0 && <span className="text-xs text-gray-600">used by {s.usedBy.join(', ')}</span>}
                  <button type="button" onClick={() => remove(s.name)} aria-label={`Delete secret ${s.name}`} className="ml-auto rounded p-1 text-red-700 hover:bg-red-50">
                    <Trash2 className="h-4 w-4" aria-hidden="true" />
                  </button>
                </li>
              ))}
            </ul>
          )}
          <form onSubmit={save} className="mt-3 grid gap-2 md:grid-cols-[1fr_1fr_1fr_auto] md:items-end">
            <div>
              <label htmlFor="chat-mcp-secret-name" className={labelClass}>Secret name</label>
              <input id="chat-mcp-secret-name" required value={name} onChange={(e) => setName(e.target.value)} placeholder="wiki" className={inputClass} />
            </div>
            <div>
              <label htmlFor="chat-mcp-secret-header" className={labelClass}>Header (optional)</label>
              <input id="chat-mcp-secret-header" value={header} onChange={(e) => setHeader(e.target.value)} placeholder="X-Api-Key" className={inputClass} />
            </div>
            <div>
              <label htmlFor="chat-mcp-secret-value" className={labelClass}>Secret value</label>
              <input id="chat-mcp-secret-value" type="password" autoComplete="new-password" required value={value} onChange={(e) => setValue(e.target.value)} className={inputClass} />
            </div>
            <button type="submit" className="rounded bg-primary-700 px-3 py-1.5 text-sm text-white hover:bg-primary-800">Set or replace</button>
          </form>
        </>
      )}
      {error && <p role="alert" className="mt-2 rounded border border-red-200 bg-red-50 p-2 text-sm text-red-800">{error}</p>}
      {message && <output className="mt-2 block rounded border border-green-200 bg-green-50 p-2 text-sm text-green-800">{message}</output>}
    </section>
  );
}
