import { useStoredSecrets } from '../lib/use-stored-secrets';
import { Check, Select, Text } from './CronFormControls';
import { RepositoryFields, SyncSecretFields } from './SyncRepositoryFields';

/**
 * The fields of a forge repository's issues and pull requests as a synced
 * source (INT-023): a read-only mirror, one document per issue or pull
 * request. The repository comes from its URL or from a collection's
 * repository block; the state and start date narrow what is mirrored, and
 * comments are taken unless turned off. Secrets are named, never shown.
 */
export default function IssuesSyncFields({ value, onChange, collections = [] }) {
  const set = (key) => (v) => onChange({ ...value, [key]: v });
  const store = useStoredSecrets();

  return (
    <>
      <RepositoryFields
        value={value}
        onChange={onChange}
        collections={collections}
        placeholder="https://github.com/owner/repo (empty: the collection's repository)"
      />
      <Select name="State" value={value.state} onChange={set('state')}>
        <option value="all">Open and closed</option>
        <option value="open">Open only</option>
        <option value="closed">Closed only</option>
      </Select>
      <Text
        name="Updated since (optional)"
        type="date"
        value={value.since}
        onChange={set('since')}
        hint="Only issues and pull requests updated on or after this date. Empty: all of them."
      />
      <fieldset className="md:col-span-2 flex flex-wrap gap-4">
        <legend className="text-xs text-fg-subtle">Mirror</legend>
        <Check checked={value.includeIssues} onChange={set('includeIssues')}>Issues</Check>
        <Check checked={value.includePulls} onChange={set('includePulls')}>Pull requests</Check>
        <Check checked={value.comments} onChange={set('comments')}>With comments</Check>
      </fieldset>
      <SyncSecretFields value={value} onChange={onChange} store={store} />
    </>
  );
}
