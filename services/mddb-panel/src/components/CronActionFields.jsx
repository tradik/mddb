// The fields of a cron rule: its schedule and its one action — fire a webhook,
// take a backup (OPS-023) or sync a sitemap, URL list or S3 prefix into a
// collection (INT-022). Laid out in the AutomationPanel form grid. Controls
// sit inside their labels (no ids), because the add form and an inline edit
// row can be open at once.

const field = 'mt-1 w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm text-gray-900';
const labelClass = 'block text-xs text-gray-600';

function Text({ name, value, onChange, placeholder, type = 'text', mono = false, hint }) {
  return (
    <label className={labelClass}>
      {name}
      <input
        type={type}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        className={`${field}${mono ? ' font-mono' : ''}`}
      />
      {hint && <span className="mt-0.5 block text-[11px] text-gray-600">{hint}</span>}
    </label>
  );
}

function Select({ name, value, onChange, children }) {
  return (
    <label className={labelClass}>
      {name}
      <select value={value} onChange={(e) => onChange(e.target.value)} className={field}>
        {children}
      </select>
    </label>
  );
}

function Check({ checked, onChange, children, wide = false }) {
  return (
    <label className={`${wide ? 'md:col-span-2 ' : ''}flex items-center gap-2 text-sm text-gray-700`}>
      <input type="checkbox" checked={checked} onChange={(e) => onChange(e.target.checked)} className="w-4 h-4 rounded border-gray-300" />
      {children}
    </label>
  );
}

/** Endpoint, region, keys and TLS of an S3-compatible store. */
function StorageFields({ value, onChange }) {
  const set = (key) => (v) => onChange({ ...value, [key]: v });
  return (
    <>
      <Text name="S3 endpoint" value={value.endpoint} onChange={set('endpoint')} placeholder="s3.amazonaws.com" />
      <Text name="Region" value={value.region} onChange={set('region')} placeholder="us-east-1" />
      <Text name="Access key" value={value.accessKey} onChange={set('accessKey')} />
      <Text
        name="Secret key"
        type="password"
        value={value.secretKey}
        onChange={set('secretKey')}
        placeholder={value.secretKeySet ? 'Stored — leave blank to keep' : ''}
        hint={value.secretKeySet ? 'A secret is stored. Leave blank to keep it.' : 'No secret stored yet.'}
      />
      <Check checked={value.useTLS} onChange={set('useTLS')}>Use TLS (HTTPS)</Check>
    </>
  );
}

function BackupFields({ value, onChange }) {
  const set = (key) => (v) => onChange({ ...value, [key]: v });
  return (
    <>
      <Select name="Backup target" value={value.target} onChange={set('target')}>
        <option value="local">Local directory (inside MDDB_BACKUP_DIR)</option>
        <option value="s3">S3-compatible bucket</option>
      </Select>
      {value.target === 'local' ? (
        <Text name="Directory (empty = MDDB_BACKUP_DIR)" value={value.path} onChange={set('path')} placeholder="nightly" />
      ) : (
        <Text name="Bucket" value={value.bucket} onChange={set('bucket')} placeholder="mddb-backups" />
      )}
      <Text name="Name prefix" value={value.prefix} onChange={set('prefix')} placeholder="mddb-" />
      <Text
        name="Keep newest"
        type="number"
        value={value.keep}
        onChange={set('keep')}
        placeholder="all"
        hint="Older backups are deleted only after a successful run."
      />
      <Text name="Collections (comma-separated, * = all)" value={value.collections} onChange={set('collections')} />
      {value.target === 's3' && <StorageFields value={value.storageConfig} onChange={set('storageConfig')} />}
    </>
  );
}

function SyncFields({ value, onChange, collections }) {
  const set = (key) => (v) => onChange({ ...value, [key]: v });
  return (
    <>
      <Select name="Source" value={value.source} onChange={set('source')}>
        <option value="sitemap">Sitemap (or sitemap index)</option>
        <option value="urls">List of URLs</option>
        <option value="s3">S3 prefix (read only)</option>
      </Select>
      <Select name="Into collection" value={value.collection} onChange={set('collection')}>
        <option value="">Select collection</option>
        {collections.map((c) => <option key={c.name} value={c.name}>{c.name}</option>)}
      </Select>
      <Text name="Language" value={value.lang} onChange={set('lang')} placeholder="en" />
      {value.source === 'sitemap' && (
        <Text name="Sitemap URL" type="url" value={value.url} onChange={set('url')} placeholder="https://example.com/sitemap.xml" />
      )}
      {value.source === 'urls' && (
        <label className={`${labelClass} md:col-span-2`}>
          URLs (one per line)
          <textarea rows={3} value={value.urls} onChange={(e) => set('urls')(e.target.value)} className={`${field} font-mono`} />
        </label>
      )}
      {value.source === 's3' && (
        <>
          <Text name="Bucket" value={value.bucket} onChange={set('bucket')} />
          <Text name="Prefix" value={value.prefix} onChange={set('prefix')} placeholder="docs/" />
          <StorageFields value={value.storageConfig} onChange={set('storageConfig')} />
        </>
      )}
      <Text name="Max documents per run" type="number" value={value.maxDocuments} onChange={set('maxDocuments')} placeholder="1000 (max 50000)" />
      <Text name="Max MiB per document" type="number" value={value.maxMiB} onChange={set('maxMiB')} placeholder="10 (max 100)" />
      <Check wide checked={value.prune} onChange={set('prune')}>
        Prune: delete the documents this rule wrote whose source is gone (404/410 or no longer listed)
      </Check>
    </>
  );
}

export default function CronActionFields({ form, setForm, webhooks, collections }) {
  return (
    <>
      <Text
        name="Schedule (cron expression)"
        value={form.schedule}
        onChange={(schedule) => setForm({ ...form, schedule })}
        placeholder="0 0 9 * * *"
        mono
        hint="6 fields: sec min hour day month weekday"
      />
      <Select name="Action" value={form.cronAction} onChange={(cronAction) => setForm({ ...form, cronAction })}>
        <option value="webhook">Call a webhook</option>
        <option value="backup">Back up the database</option>
        <option value="sync">Sync a source into a collection</option>
      </Select>
      {form.cronAction === 'webhook' && (
        <Select name="Webhook" value={form.webhookId} onChange={(webhookId) => setForm({ ...form, webhookId })}>
          <option value="">Select webhook</option>
          {webhooks.map((w) => (
            <option key={w.id} value={w.id}>
              {w.name} ({w.method || 'POST'} {w.url})
            </option>
          ))}
        </Select>
      )}
      {form.cronAction === 'backup' && (
        <BackupFields value={form.backup} onChange={(backup) => setForm({ ...form, backup })} />
      )}
      {form.cronAction === 'sync' && (
        <SyncFields value={form.sync} onChange={(sync) => setForm({ ...form, sync })} collections={collections} />
      )}
    </>
  );
}
