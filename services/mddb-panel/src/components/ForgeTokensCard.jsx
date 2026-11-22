import { useEffect, useState } from 'react';
import { KeyRound, Plus, Trash2 } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useStoredSecrets } from '../lib/use-stored-secrets';
import ApiErrorNotice from './ApiErrorNotice';
import { Text } from './CronFormControls';
import SecretPicker from './SecretPicker';

/** The rows of a `{ host: secretName }` map; `id` keys the React list. */
function rowsOf(defaults = {}) {
  return Object.entries(defaults).map(([host, secret], id) => ({ id, host, secret }));
}

/**
 * The server's default forge tokens (INT-023): for each host, the stored
 * secret a git or issues sync uses when neither its rule nor its collection's
 * repository names a token. Saving replaces the whole map
 * (PUT /v1/forge-tokens); the server refuses an unknown secret or a bad host.
 * Admin only.
 */
export default function ForgeTokensCard() {
  const store = useStoredSecrets();
  const [rows, setRows] = useState([]);
  const [nextId, setNextId] = useState(0);
  const [loadError, setLoadError] = useState(null);
  const [saveError, setSaveError] = useState(null);
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState(null);

  useEffect(() => {
    let cancelled = false;
    mddbClient.getForgeTokens().then(
      (data) => {
        if (cancelled) return;
        const loaded = rowsOf(data.defaults);
        setRows(loaded);
        setNextId(loaded.length);
      },
      (err) => { if (!cancelled) setLoadError(err); },
    );
    return () => { cancelled = true; };
  }, []);

  const update = (id, patch) => setRows(rows.map((r) => (r.id === id ? { ...r, ...patch } : r)));
  const add = () => {
    setRows([...rows, { id: nextId, host: '', secret: '' }]);
    setNextId(nextId + 1);
  };

  const save = async () => {
    setSaving(true);
    setSaveError(null);
    setMessage(null);
    const defaults = Object.fromEntries(rows.filter((r) => r.host.trim()).map((r) => [r.host.trim().toLowerCase(), r.secret]));
    try {
      await mddbClient.setForgeTokens(defaults);
      const n = Object.keys(defaults).length;
      setMessage(`Saved ${n} default token${n === 1 ? '' : 's'}.`);
    } catch (err) {
      setSaveError(err);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex-1 overflow-y-auto p-6">
      <section aria-labelledby="forge-tokens-title" className="max-w-4xl mx-auto bg-surface rounded-lg shadow p-6 space-y-3">
        <h2 id="forge-tokens-title" className="flex items-center gap-2 text-lg font-semibold text-fg">
          <KeyRound className="w-5 h-5 text-accent" aria-hidden="true" /> Default forge tokens
        </h2>
        <p className="text-sm text-fg-subtle">
          The token a git or issues sync uses for a host when neither its rule nor its collection&apos;s repository names one.
          Tokens are stored secrets, picked by name or pasted once; their values are never shown.
        </p>
        <ApiErrorNotice error={loadError} admin />
        {rows.length === 0 && !loadError && <p className="text-sm text-fg-muted">No default tokens.</p>}
        <ul className="space-y-3">
          {rows.map((row) => (
            <li key={row.id} className="grid gap-3 md:grid-cols-[1fr_1fr_auto] md:items-start border-b border-line pb-3">
              <Text name="Host" value={row.host} onChange={(host) => update(row.id, { host })} placeholder="github.com" mono />
              <SecretPicker name={`Token for ${row.host || 'this host'}`} value={row.secret} onChange={(secret) => update(row.id, { secret })} store={store} />
              <button
                type="button"
                onClick={() => setRows(rows.filter((r) => r.id !== row.id))}
                aria-label={`Remove the token of ${row.host || 'this row'}`}
                className="mt-5 rounded p-1 text-danger hover:bg-danger-soft"
              >
                <Trash2 className="h-4 w-4" aria-hidden="true" />
              </button>
            </li>
          ))}
        </ul>
        {store.error && <p role="alert" className="text-xs text-danger">Stored secrets could not be listed: {store.error}</p>}
        <div className="flex flex-wrap gap-2">
          <button type="button" onClick={add} className="flex items-center gap-1 rounded bg-surface-subtle px-3 py-1.5 text-sm text-fg-muted hover:bg-surface-strong">
            <Plus className="h-4 w-4" aria-hidden="true" /> Add host
          </button>
          <button
            type="button"
            onClick={save}
            disabled={saving || Boolean(loadError)}
            className="rounded bg-primary-700 px-3 py-1.5 text-sm text-white hover:bg-primary-800 disabled:opacity-60"
          >
            Save default tokens
          </button>
        </div>
        <ApiErrorNotice error={saveError} admin />
        {message && <output className="block rounded border border-success-line bg-success-soft p-2 text-sm text-success-strong">{message}</output>}
      </section>
    </div>
  );
}
