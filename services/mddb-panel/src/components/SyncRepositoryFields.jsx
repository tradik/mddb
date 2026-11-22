import { needsForge } from '../lib/automation-sync-form';
import { Select, Text } from './CronFormControls';
import SecretPicker from './SecretPicker';

// The repository fields a git sync (2.19.5, #421) and an issues sync
// (INT-023) share: where the repository is, its forge on a self-hosted host,
// and the stored secrets of its token and webhook. Laid out in the
// AutomationPanel form grid.

/** The forges a self-hosted host can run, as the server names them. */
export function ForgeSelect({ value, onChange, hint = 'How this host serves its API.' }) {
  return (
    <Select name="Forge" value={value} onChange={onChange} hint={hint}>
      <option value="">Select the forge</option>
      <option value="github">GitHub Enterprise Server</option>
      <option value="gitlab">GitLab (self-managed)</option>
      <option value="gitea">Gitea</option>
      <option value="forgejo">Forgejo</option>
    </Select>
  );
}

/**
 * The repository URL — optional, since the server can take it from a
 * collection's repository block — the collection to take it from, and the
 * forge when the URL's host is not one the server knows.
 */
export function RepositoryFields({ value, onChange, collections, placeholder, forgeHint }) {
  const set = (key) => (v) => onChange({ ...value, [key]: v });
  return (
    <>
      <Text
        name="Repository URL (optional)"
        type="url"
        value={value.url}
        onChange={set('url')}
        placeholder={placeholder}
        hint="https only. Leave empty to use a collection's repository: the one chosen below, else this rule's collection."
      />
      <Select
        name="Repository from collection"
        value={value.repositoryFrom}
        onChange={set('repositoryFrom')}
        hint="Its Repository settings supply the URL and token when this rule names none."
      >
        <option value="">This rule&apos;s collection</option>
        {collections.map((c) => <option key={c.name} value={c.name}>{c.name}</option>)}
      </Select>
      {needsForge(value.url) && <ForgeSelect value={value.forge} onChange={set('forge')} hint={forgeHint} />}
    </>
  );
}

/**
 * The token and webhook secrets, picked or pasted (SecretPicker). Empty
 * token: the collection's repository token, else the server's default token
 * for the host.
 */
export function SyncSecretFields({ value, onChange, store }) {
  const set = (key) => (v) => onChange({ ...value, [key]: v });
  return (
    <>
      <SecretPicker
        name="Token secret (private repository)"
        value={value.tokenSecretRef}
        onChange={set('tokenSecretRef')}
        store={store}
        hint="None: the collection's repository token, else the server's default token for this host."
      />
      <SecretPicker
        name="Webhook secret"
        value={value.hookSecretRef}
        onChange={set('hookSecretRef')}
        store={store}
        hint="Verifies POST /v1/automation/{id}/hook (GitHub X-Hub-Signature-256, GitLab X-Gitlab-Token, Gitea/Forgejo signatures). Empty: no webhook."
      />
      {store.error && <p role="alert" className="md:col-span-2 text-xs text-danger">Stored secrets could not be listed: {store.error}</p>}
      <p className="md:col-span-2 text-xs text-fg-subtle">
        Secret values are write-only: paste one here or set it in Chat → MCP secrets (needs MDDB_ENCRYPTION_KEY). The rule only names them.
      </p>
    </>
  );
}
