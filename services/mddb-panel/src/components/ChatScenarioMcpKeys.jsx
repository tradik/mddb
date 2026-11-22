import { useCallback, useEffect, useState } from 'react';
import { Key, Plus } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useStore } from '../lib/store';
import { mcpKeyAuthOff } from '../lib/mcp-key-auth';
import { inputClass, labelClass } from '../lib/chat-scenario';
import OneTimeSecret from './OneTimeSecret';
import MCPKeyAuthNotice from './MCPKeyAuthNotice';
import { KeyRow } from './MCPAPIKeysPanel';

/**
 * ChatScenarioMcpKeys lists the MCP API keys bound to a scenario (#439),
 * creates one and revokes one. A bound key reaches the scenario's allowed
 * collections, read-only, and gets its composed prompt as the MCP
 * instructions; listing and creating keys need admin. On a server that does
 * not check MCP keys it says so and offers no Create (#445).
 */
export default function ChatScenarioMcpKeys({ scenario }) {
  const [keys, setKeys] = useState([]);
  const [error, setError] = useState(null);
  const [name, setName] = useState('');
  // The new key and the server's warning about it, shown once (#445).
  const [created, setCreated] = useState(null);
  const [busy, setBusy] = useState(false);
  const keyAuthOff = mcpKeyAuthOff(useStore().config);

  const load = useCallback(() => mddbClient.listMCPAPIKeys().then(
    (data) => {
      setKeys((data.keys || []).filter((k) => k.scenario === scenario));
      setError(null);
    },
    (err) => setError(err.message || 'MCP keys could not be loaded'),
  ), [scenario]);

  // The parent keys this section by scenario, so another scenario starts
  // with fresh state (no secret of the previous one shown).
  useEffect(() => { void load(); }, [load]);

  // Runs a create, disable or delete, then reloads the list.
  const run = async (action) => {
    setBusy(true);
    try {
      await action();
      await load();
    } catch (err) {
      setError(err.message);
    } finally {
      setBusy(false);
    }
  };

  const create = (e) => {
    e.preventDefault();
    if (!name.trim() || keyAuthOff) return;
    void run(async () => {
      const data = await mddbClient.createMCPAPIKey(name.trim(), 0, { scenario });
      setCreated({ key: data.key, warning: data.warning });
      setName('');
    });
  };

  const revoke = (ref) => {
    if (!confirm('Delete this MCP key? A client using it loses access.')) return;
    void run(() => mddbClient.deleteMCPAPIKey(ref));
  };

  return (
    <section aria-labelledby="scenario-mcp-keys" className="rounded-lg bg-surface p-4 shadow">
      <h3 id="scenario-mcp-keys" className="mb-1 flex items-center gap-2 text-sm font-semibold text-fg">
        <Key className="h-4 w-4 text-fg-faint" aria-hidden="true" /> MCP keys
      </h3>
      <p className="mb-3 text-xs text-fg-muted">
        A key bound to this scenario gives an MCP client the same limits as the chat widget: its allowed collections, read-only,
        and its composed prompt as instructions. While the scenario is disabled, or chat is off, the key&apos;s calls are refused.
      </p>
      {keyAuthOff && <div className="mb-3"><MCPKeyAuthNotice /></div>}
      {error && <p role="alert" className="mb-2 rounded border border-danger-line bg-danger-soft p-2 text-sm text-danger-strong">{error}</p>}
      <form onSubmit={create} className="mb-3 flex items-end gap-2">
        <div className="flex-1">
          <label htmlFor="scenario-mcp-key-name" className={labelClass}>New key name</label>
          <input id="scenario-mcp-key-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="website-widget" className={inputClass} />
        </div>
        <button type="submit" disabled={busy || keyAuthOff || !name.trim()} className="flex items-center gap-1 rounded bg-primary-700 px-3 py-1.5 text-sm text-white hover:bg-primary-800 disabled:opacity-50">
          <Plus className="h-4 w-4" aria-hidden="true" /> Create key
        </button>
      </form>
      {created && (
        <div className="mb-3 space-y-2">
          <OneTimeSecret secret={created.key} onDismiss={() => setCreated(null)} />
          {created.warning && <MCPKeyAuthNotice warning={created.warning} />}
        </div>
      )}
      {keys.length === 0 ? (
        <p className="text-sm text-fg-subtle">No key is bound to this scenario.</p>
      ) : (
        <div className="divide-y rounded border">
          {keys.map((k) => (
            <KeyRow key={k.fingerprint || k.name} apiKey={k} onDisable={(ref) => run(() => mddbClient.disableMCPAPIKey(ref))} onDelete={revoke} />
          ))}
        </div>
      )}
    </section>
  );
}
