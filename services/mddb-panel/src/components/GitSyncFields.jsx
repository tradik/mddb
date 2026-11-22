import { LATEST_RELEASE } from '../lib/automation-sync-form';
import { useStoredSecrets } from '../lib/use-stored-secrets';
import { Check, Text, TextArea } from './CronFormControls';
import { RepositoryFields, SyncSecretFields } from './SyncRepositoryFields';

/**
 * The fields of a git repository as a synced source (2.19.5, #421): the
 * repository — or the collection whose repository block supplies it
 * (INT-023) —, the ref it follows, the files it takes, the forge of a
 * self-hosted host, and the stored secrets of a private repository's token
 * and of the push/release webhook. A secret's value is write-only — it is
 * pasted once or set in Chat → MCP secrets and never comes back — so the
 * form only names one, and the list says whether a sealed value is stored.
 */
export default function GitSyncFields({ value, onChange, collections = [] }) {
  const set = (key) => (v) => onChange({ ...value, [key]: v });
  const store = useStoredSecrets();

  return (
    <>
      <RepositoryFields
        value={value}
        onChange={onChange}
        collections={collections}
        placeholder="https://github.com/owner/repo.git"
        forgeHint="How this host serves releases and archives."
      />
      <Text
        name="Ref"
        value={value.ref}
        onChange={set('ref')}
        placeholder={LATEST_RELEASE}
        mono
        hint={`A branch (main), a tag (v1.2.0), or ${LATEST_RELEASE}: the forge's newest release, else the highest v1.2.3 tag. Empty follows the default branch.`}
      />
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
      <SyncSecretFields value={value} onChange={onChange} store={store} />
      <Check wide checked={value.adopt} onChange={set('adopt')}>
        Adopt: take over documents with the same path that no sync wrote (a collection seeded by mddb-cli autoindex)
      </Check>
    </>
  );
}
