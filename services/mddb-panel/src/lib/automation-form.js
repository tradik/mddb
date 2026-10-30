/**
 * Automation rules: the panel's form state and the REST payload.
 *
 * A cron rule runs one action (2.18.9+): fire a webhook, take a backup
 * (OPS-023) or sync a source into a collection (INT-022). Stored S3 secret
 * keys are never sent back by the server (`secretKeySet` says one exists), so
 * a blank secret in the form means "keep it" and is left out of the payload.
 */

const EMPTY_STORAGE = { endpoint: '', region: '', accessKey: '', secretKey: '', useTLS: true, secretKeySet: false };

export const EMPTY_BACKUP = {
  target: 'local', path: '', bucket: '', prefix: '', keep: '', collections: '*', storageConfig: EMPTY_STORAGE,
};

export const EMPTY_SYNC = {
  source: 'sitemap', collection: '', lang: 'en', url: '', urls: '', bucket: '', prefix: '',
  prune: false, maxDocuments: '', maxMiB: '', storageConfig: EMPTY_STORAGE,
};

export const EMPTY_RULE = {
  type: 'webhook',
  name: '',
  enabled: true,
  // webhook fields
  url: '',
  method: 'POST',
  headers: '',
  // trigger fields
  collection: '',
  searchType: 'fts',
  query: '',
  threshold: 50,
  webhookId: '',
  events: ['insert', 'update'],
  sentimentEnabled: false,
  sentimentMin: -1.0,
  sentimentMax: 1.0,
  conditionLogic: 'and',
  // cron fields
  schedule: '',
  cronAction: 'webhook',
  backup: EMPTY_BACKUP,
  sync: EMPTY_SYNC,
};

const MIB = 1024 * 1024;

/** A positive whole number from a form field, or undefined. */
function positive(value) {
  const n = Number(value);
  return Number.isInteger(n) && n > 0 ? n : undefined;
}

/** Copies the keys of `obj` whose values are not undefined or ''. */
function compact(obj) {
  return Object.fromEntries(Object.entries(obj).filter(([, v]) => v !== undefined && v !== ''));
}

/** The S3 block of a backup or sync; a blank secret keeps the stored one. */
export function buildStorage(sc) {
  return compact({
    endpoint: sc.endpoint.trim(),
    region: sc.region.trim(),
    accessKey: sc.accessKey.trim(),
    secretKey: sc.secretKey || undefined,
    useTLS: sc.useTLS,
  });
}

/** The `backup` action of a cron rule (OPS-023). */
export function buildBackup(b) {
  const collections = b.collections.split(',').map((c) => c.trim()).filter(Boolean);
  const out = compact({
    target: b.target,
    prefix: b.prefix.trim(),
    keep: positive(b.keep),
    collections: collections.length ? collections : ['*'],
  });
  if (b.target === 's3') {
    Object.assign(out, compact({ bucket: b.bucket.trim() }), { storageConfig: buildStorage(b.storageConfig) });
  } else if (b.path.trim()) {
    out.path = b.path.trim();
  }
  return out;
}

/** The `sync` action of a cron rule (INT-022). */
export function buildSync(s) {
  const out = compact({
    source: s.source,
    collection: s.collection,
    lang: s.lang.trim(),
    maxDocuments: positive(s.maxDocuments),
    maxBytes: positive(s.maxMiB) && positive(s.maxMiB) * MIB,
  });
  if (s.prune) out.prune = true;
  if (s.source === 'sitemap') out.url = s.url.trim();
  if (s.source === 'urls') out.urls = s.urls.split('\n').map((u) => u.trim()).filter(Boolean);
  if (s.source === 's3') {
    Object.assign(out, compact({ bucket: s.bucket.trim(), prefix: s.prefix.trim() }), { storageConfig: buildStorage(s.storageConfig) });
  }
  return out;
}

/** The REST payload of a rule from the form state. */
export function buildPayload(form) {
  const payload = {
    type: form.type,
    name: form.name,
    enabled: form.enabled,
  };
  if (form.type === 'webhook') {
    payload.url = form.url;
    payload.method = form.method;
    if (form.headers.trim()) {
      try {
        payload.headers = JSON.parse(form.headers);
      } catch {
        payload.headers = {};
      }
    }
  } else if (form.type === 'trigger') {
    payload.collection = form.collection;
    payload.searchType = form.searchType;
    payload.query = form.query;
    payload.threshold = form.threshold;
    payload.events = form.events && form.events.length > 0 ? form.events : ['insert', 'update'];
    if (form.webhookId) payload.webhookId = form.webhookId;
    payload.sentimentEnabled = form.sentimentEnabled;
    if (form.sentimentEnabled) {
      payload.sentimentMin = form.sentimentMin;
      payload.sentimentMax = form.sentimentMax;
    }
    if (form.query && form.sentimentEnabled) {
      payload.conditionLogic = form.conditionLogic;
    }
  } else if (form.type === 'cron') {
    payload.schedule = form.schedule;
    if (form.cronAction === 'backup') payload.backup = buildBackup(form.backup);
    else if (form.cronAction === 'sync') payload.sync = buildSync(form.sync);
    else if (form.webhookId) payload.webhookId = form.webhookId;
  }
  return payload;
}

/** The form state of a stored S3 block (its secret is never returned). */
function storageToForm(sc) {
  return { ...EMPTY_STORAGE, ...sc, secretKey: '', useTLS: sc?.useTLS ?? false };
}

/** The form state of a stored rule. */
export function ruleToForm(rule) {
  const b = rule.backup;
  const s = rule.sync;
  return {
    type: rule.type || 'webhook',
    name: rule.name || '',
    enabled: rule.enabled ?? true,
    url: rule.url || '',
    method: rule.method || 'POST',
    headers: rule.headers ? JSON.stringify(rule.headers, null, 2) : '',
    collection: rule.collection || '',
    searchType: rule.searchType || 'fts',
    query: rule.query || '',
    threshold: rule.threshold ?? 50,
    webhookId: rule.webhookId || '',
    events: rule.events && rule.events.length > 0 ? [...rule.events] : ['insert', 'update'],
    sentimentEnabled: rule.sentimentEnabled ?? false,
    sentimentMin: rule.sentimentMin ?? -1.0,
    sentimentMax: rule.sentimentMax ?? 1.0,
    conditionLogic: rule.conditionLogic || 'and',
    schedule: rule.schedule || '',
    cronAction: s ? 'sync' : b ? 'backup' : 'webhook',
    backup: b
      ? {
        target: b.target || 'local',
        path: b.path || '',
        bucket: b.bucket || '',
        prefix: b.prefix || '',
        keep: b.keep ? String(b.keep) : '',
        collections: (b.collections || ['*']).join(', '),
        storageConfig: storageToForm(b.storageConfig),
      }
      : EMPTY_BACKUP,
    sync: s
      ? {
        source: s.source,
        collection: s.collection,
        lang: s.lang,
        url: s.url || '',
        urls: (s.urls || []).join('\n'),
        bucket: s.bucket || '',
        prefix: s.prefix || '',
        prune: s.prune === true,
        maxDocuments: s.maxDocuments ? String(s.maxDocuments) : '',
        maxMiB: s.maxBytes ? String(Math.round(s.maxBytes / MIB)) : '',
        storageConfig: storageToForm(s.storageConfig),
      }
      : EMPTY_SYNC,
  };
}

/** One line saying what a cron rule does, for the rules table. */
export function describeCronAction(rule) {
  if (rule.sync) {
    const s = rule.sync;
    const from = s.source === 'urls' ? `${(s.urls || []).length} URLs` : s.source === 's3' ? `s3://${s.bucket}/${s.prefix || ''}` : s.url;
    return `sync ${s.source} ${from} → ${s.collection}${s.prune ? ' (prune)' : ''}`;
  }
  if (rule.backup) {
    const b = rule.backup;
    const where = b.target === 's3' ? `s3://${b.bucket || b.storageConfig?.bucket || ''}` : b.path || 'MDDB_BACKUP_DIR';
    return `backup → ${where}${b.keep ? `, keep ${b.keep}` : ''}`;
  }
  return rule.webhookId ? `webhook ${rule.webhookId}` : '';
}
