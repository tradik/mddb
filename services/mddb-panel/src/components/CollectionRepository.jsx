import { useState } from 'react';
import { GitBranch } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { needsForge } from '../lib/automation-sync-form';
import { issuesSyncRule } from '../lib/collection-config';
import { useStoredSecrets } from '../lib/use-stored-secrets';
import ApiErrorNotice from './ApiErrorNotice';
import { Text } from './CronFormControls';
import SecretPicker from './SecretPicker';
import { ForgeSelect } from './SyncRepositoryFields';

/**
 * The repository a collection's documents come from (INT-023): its URL, the
 * forge of a self-hosted host, and its token as a stored secret's name. A git
 * or issues sync naming this collection (or writing into it) uses it when the
 * rule names no URL or token. Saved with the rest of the settings dialog; an
 * empty URL removes it.
 *
 * "Create issues sync" adds a cron rule mirroring the repository's issues and
 * pull requests into `<collection>-issues` — once the repository is saved,
 * since the server reads it when the rule runs.
 */
export default function CollectionRepository({ collection, value, onChange, savedUrl }) {
  const set = (key) => (v) => onChange({ ...value, [key]: v });
  const store = useStoredSecrets();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(null);
  const [message, setMessage] = useState(null);

  const createIssuesSync = async () => {
    setBusy(true);
    setError(null);
    setMessage(null);
    try {
      const rule = issuesSyncRule(collection);
      await mddbClient.createAutomation(rule);
      setMessage(`Created the rule "${rule.name}": issues and pull requests are mirrored into ${rule.sync.collection} every 15 minutes.`);
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <section aria-labelledby="collection-repository-title" className="space-y-2">
      <h3 id="collection-repository-title" className="flex items-center gap-2 text-sm font-semibold text-fg">
        <GitBranch className="h-4 w-4 text-accent" aria-hidden="true" /> Repository
      </h3>
      <p className="text-xs text-fg-subtle">
        Git and issues syncs use this repository when their rule names no URL or token. The token is a stored secret&apos;s name;
        without one, the server&apos;s default token for the host applies.
      </p>
      <div className="grid gap-3 md:grid-cols-2">
        <Text name="Repository URL" type="url" value={value.url} onChange={set('url')} placeholder="https://github.com/owner/repo" hint="https only. Empty removes the repository on save." />
        {needsForge(value.url) && <ForgeSelect value={value.forge} onChange={set('forge')} />}
        <SecretPicker name="Repository token" value={value.tokenSecretRef} onChange={set('tokenSecretRef')} store={store} />
      </div>
      {store.error && <p role="alert" className="text-xs text-danger">Stored secrets could not be listed: {store.error}</p>}
      <div className="flex flex-wrap items-center gap-2">
        <button
          type="button"
          onClick={createIssuesSync}
          disabled={busy || !savedUrl}
          aria-describedby="collection-repository-sync-hint"
          className="rounded bg-primary-700 px-3 py-1.5 text-sm text-white hover:bg-primary-800 disabled:opacity-60"
        >
          Create issues sync
        </button>
        <span id="collection-repository-sync-hint" className="text-xs text-fg-subtle">
          {savedUrl
            ? `Mirrors issues and pull requests into ${collection}-issues every 15 minutes.`
            : 'Save a repository first: the sync reads it when it runs.'}
        </span>
      </div>
      <ApiErrorNotice error={error} admin />
      {message && <output className="block rounded border border-success-line bg-success-soft p-2 text-sm text-success-strong">{message}</output>}
    </section>
  );
}
