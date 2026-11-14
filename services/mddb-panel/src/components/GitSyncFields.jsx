import { useEffect, useState } from 'react';
import mddbClient from '../lib/mddb-client';
import { LATEST_RELEASE, needsForge } from '../lib/automation-sync-form';
import { Check, Select, Text, TextArea } from './CronFormControls';

/**
 * The fields of a git repository as a synced source (2.19.5, #421): the
 * repository, the ref it follows, the files it takes, the forge of a
 * self-hosted host, and the stored secrets of a private repository's token
 * and of the push/release webhook. A secret's value is write-only — it is
 * set in Chat → MCP secrets and never comes back — so the form only names
 * one, and the list says whether a sealed value is stored.
 */
export default function GitSyncFields({ value, onChange }) {
  const set = (key) => (v) => onChange({ ...value, [key]: v });
  const [secrets, setSecrets] = useState([]);
  const [secretsError, setSecretsError] = useState(null);

  useEffect(() => {
    let cancelled = false;
    mddbClient.listChatMcpSecrets().then(
      (data) => { if (!cancelled) setSecrets(data.secrets || []); },
      (err) => { if (!cancelled) setSecretsError(err.message); },
    );
    return () => { cancelled = true; };
  }, []);

  return (
    <>
      <Text
        name="Repository URL"
        type="url"
        value={value.url}
        onChange={set('url')}
        placeholder="https://github.com/owner/repo.git"
        hint="https only. github.com, gitlab.com and codeberg.org are known; another host needs its forge."
      />
      <Text
        name="Ref"
        value={value.ref}
        onChange={set('ref')}
        placeholder={LATEST_RELEASE}
        mono
        hint={`A branch (main), a tag (v1.2.0), or ${LATEST_RELEASE}: the forge's newest release, else the highest v1.2.3 tag. Empty follows the default branch.`}
      />
      {needsForge(value.url) && (
        <Select name="Forge" value={value.forge} onChange={set('forge')} hint="How this host serves releases and archives.">
          <option value="">Select the forge</option>
          <option value="github">GitHub Enterprise Server</option>
          <option value="gitlab">GitLab (self-managed)</option>
          <option value="gitea">Gitea</option>
          <option value="forgejo">Forgejo</option>
        </Select>
      )}
      <TextArea
        name="Include (globs, one per line)"
        value={value.include}
        onChange={set('include')}
        placeholder={'*.md\ndocs/**'}
        hint="As mddb-cli autoindex --include: a glob matches the path or the file name; dir/** takes everything under dir. Empty takes every text file."
      />
      <TextArea
        name="Exclude (globs, one per line)"
        value={value.exclude}
        onChange={set('exclude')}
        placeholder={'testdata/**\ndist/**'}
      />
      <SecretSelect
        name="Token secret (private repository)"
        value={value.tokenSecretRef}
        onChange={set('tokenSecretRef')}
        secrets={secrets}
      />
      <SecretSelect
        name="Webhook secret"
        value={value.hookSecretRef}
        onChange={set('hookSecretRef')}
        secrets={secrets}
        hint="Verifies POST /v1/automation/{id}/hook (GitHub X-Hub-Signature-256, GitLab X-Gitlab-Token, Gitea/Forgejo signatures). Empty: no webhook."
      />
      {secretsError && <p role="alert" className="md:col-span-2 text-xs text-red-700">Stored secrets could not be listed: {secretsError}</p>}
      <p className="md:col-span-2 text-xs text-gray-600">
        Secret values are write-only: set them in Chat → MCP secrets (needs MDDB_ENCRYPTION_KEY). This form only names them.
      </p>
      <Check wide checked={value.adopt} onChange={set('adopt')}>
        Adopt: take over documents with the same path that no sync wrote (a collection seeded by mddb-cli autoindex)
      </Check>
    </>
  );
}

/**
 * A stored secret picked by name, each listed as set (a sealed value is
 * stored) or not usable. A name the list lacks — a secret deleted since, or
 * a list that could not be read — stays selectable.
 */
function SecretSelect({ name, value, onChange, secrets, hint }) {
  const names = secrets.map((s) => s.name);
  return (
    <Select name={name} value={value} onChange={onChange} hint={hint}>
      <option value="">None</option>
      {secrets.map((s) => (
        <option key={s.name} value={s.name}>{`${s.name} (${s.set ? 'set' : 'not sealed: set it again'})`}</option>
      ))}
      {value && !names.includes(value) && <option value={value}>{`${value} (not found)`}</option>}
    </Select>
  );
}

