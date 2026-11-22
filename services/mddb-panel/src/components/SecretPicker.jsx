import { useState } from 'react';
import mddbClient from '../lib/mddb-client';
import { encryptionKeyVar, secretErrorText } from '../lib/chat-mcp';
import { Select, Text, field } from './CronFormControls';

/**
 * A stored secret named by a form (INT-023): pick one by name from the list,
 * each shown as set (a sealed value is stored) or not usable — or paste a
 * token with a name for it, which is stored (PUT /v1/chat/mcp-secrets/{name})
 * and then picked. The pasted value goes only to the secret store: it is
 * never put into the form's value, and the field is cleared once saved.
 *
 * `store` is a useStoredSecrets() result, shared by the pickers of one form.
 * A name the list lacks — a secret deleted since, or a list that could not be
 * read — stays selectable.
 */
export default function SecretPicker({ name, value, onChange, store, hint }) {
  const [pasting, setPasting] = useState(false);
  const names = store.secrets.map((s) => s.name);

  return (
    <>
      <div>
        <Select name={name} value={value} onChange={onChange} hint={hint}>
          <option value="">None</option>
          {store.secrets.map((s) => (
            <option key={s.name} value={s.name}>{`${s.name} (${s.set ? 'set' : 'not sealed: set it again'})`}</option>
          ))}
          {value && !names.includes(value) && <option value={value}>{`${value} (not found)`}</option>}
        </Select>
        <button
          type="button"
          aria-expanded={pasting}
          onClick={() => setPasting(!pasting)}
          className="mt-1 text-xs text-info underline hover:text-info-strong"
        >
          {pasting ? 'Cancel pasting a token' : `Paste a new token for ${name}`}
        </button>
      </div>
      {pasting && (
        <PasteSecret
          name={name}
          encryptionReady={store.encryptionReady}
          onSaved={(key) => {
            setPasting(false);
            store.reload();
            onChange(key);
          }}
        />
      )}
    </>
  );
}

/** The paste form: a name and a write-only value, stored as one secret. */
function PasteSecret({ name, encryptionReady, onSaved }) {
  const [key, setKey] = useState('');
  const [token, setToken] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(null);

  const save = async () => {
    setBusy(true);
    setError(null);
    try {
      await mddbClient.putChatMcpSecret(key.trim(), { value: token });
      setToken('');
      onSaved(key.trim());
    } catch (err) {
      setError(secretErrorText(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <fieldset className="md:col-span-2 grid gap-2 rounded-lg border border-line p-3 md:grid-cols-[1fr_1fr_auto] md:items-end">
      <legend className="px-1 text-xs text-fg-subtle">Paste a token for {name}</legend>
      <Text name="New secret name" value={key} onChange={setKey} placeholder="forge-token" />
      <label className="block text-xs text-fg-subtle">
        <span>Token value</span>
        <input type="password" autoComplete="new-password" value={token} onChange={(e) => setToken(e.target.value)} className={field} />
      </label>
      <button
        type="button"
        onClick={save}
        disabled={busy || !encryptionReady || !key.trim() || !token}
        className="rounded bg-primary-700 px-3 py-2 text-sm text-white hover:bg-primary-800 disabled:opacity-60"
      >
        Store and use
      </button>
      <p className="md:col-span-3 text-xs text-fg-subtle">
        {encryptionReady
          ? 'The value is stored sealed and never shown again; this form keeps only its name.'
          : `This server has no encryption key, so it cannot store a token. Set ${encryptionKeyVar} and restart mddbd.`}
      </p>
      {error && <p role="alert" className="md:col-span-3 text-xs text-danger">{error}</p>}
    </fieldset>
  );
}
