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

/** A webhook's headers: the JSON typed in, {} when it does not parse. */
function parseHeaders(text) {
  try {
    return JSON.parse(text);
  } catch {
    return {};
  }
}

/** The fields of a webhook rule. */
function webhookFields(form) {
  const out = { url: form.url, method: form.method };
  if (form.headers.trim()) out.headers = parseHeaders(form.headers);
  return out;
}

/** The fields of a trigger rule; the sentiment bounds only when enabled. */
function triggerFields(form) {
  const out = {
    collection: form.collection,
    searchType: form.searchType,
    query: form.query,
    threshold: form.threshold,
    events: form.events?.length > 0 ? form.events : ['insert', 'update'],
  };
  if (form.webhookId) out.webhookId = form.webhookId;
  out.sentimentEnabled = form.sentimentEnabled;
  if (form.sentimentEnabled) {
    out.sentimentMin = form.sentimentMin;
    out.sentimentMax = form.sentimentMax;
  }
  if (form.query && form.sentimentEnabled) out.conditionLogic = form.conditionLogic;
  return out;
}

/** The fields of a cron rule: its schedule and one action. */
function cronFields(form) {
  const out = { schedule: form.schedule };
  if (form.cronAction === 'backup') out.backup = buildBackup(form.backup);
  else if (form.cronAction === 'sync') out.sync = buildSync(form.sync);
  else if (form.webhookId) out.webhookId = form.webhookId;
  return out;
}

const TYPE_FIELDS = { webhook: webhookFields, trigger: triggerFields, cron: cronFields };

/** The REST payload of a rule from the form state. */
export function buildPayload(form) {
  const fields = TYPE_FIELDS[form.type];
  return {
    type: form.type,
    name: form.name,
    enabled: form.enabled,
    ...(fields ? fields(form) : {}),
  };
}

/** The form state of a stored S3 block (its secret is never returned). */
function storageToForm(sc) {
  return { ...EMPTY_STORAGE, ...sc, secretKey: '', useTLS: sc?.useTLS ?? false };
}

/** The form state of a stored backup block. */
function backupToForm(b) {
  return {
    target: b.target || 'local',
    path: b.path || '',
    bucket: b.bucket || '',
    prefix: b.prefix || '',
    keep: b.keep ? String(b.keep) : '',
    collections: (b.collections || ['*']).join(', '),
    storageConfig: storageToForm(b.storageConfig),
  };
}

/** The form state of a stored sync block. */
function syncToForm(s) {
  return {
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
  };
}

/** What a stored cron rule runs: a sync, a backup or a webhook. */
function cronActionOf(rule) {
  if (rule.sync) return 'sync';
  if (rule.backup) return 'backup';
  return 'webhook';
}

/** The form state of a stored rule. */
export function ruleToForm(rule) {
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
    cronAction: cronActionOf(rule),
    backup: rule.backup ? backupToForm(rule.backup) : EMPTY_BACKUP,
    sync: rule.sync ? syncToForm(rule.sync) : EMPTY_SYNC,
  };
}

/** Where a sync reads from: a URL count, an S3 location or the sitemap URL. */
function syncSourceText(s) {
  if (s.source === 'urls') return `${(s.urls || []).length} URLs`;
  if (s.source === 's3') return `s3://${s.bucket}/${s.prefix || ''}`;
  return s.url;
}

/** One line saying what a cron rule does, for the rules table. */
export function describeCronAction(rule) {
  if (rule.sync) {
    const s = rule.sync;
    const prune = s.prune ? ' (prune)' : '';
    return `sync ${s.source} ${syncSourceText(s)} → ${s.collection}${prune}`;
  }
  if (rule.backup) {
    const b = rule.backup;
    const where = b.target === 's3' ? `s3://${b.bucket || b.storageConfig?.bucket || ''}` : b.path || 'MDDB_BACKUP_DIR';
    const keep = b.keep ? ', keep ' + b.keep : '';
    return `backup → ${where}${keep}`;
  }
  return rule.webhookId ? `webhook ${rule.webhookId}` : '';
}

/**
 * Whether a rule can be run now: a cron rule with a sync or a backup (2.18.11).
 */
export function isRunnable(rule) {
  return rule.type === 'cron' && Boolean(rule.sync || rule.backup);
}

/**
 * describeLastResult renders a rule's lastResult (2.18.11): the status, a
 * sync's counts and the error, e.g. "error · +2 ~1 =35 −0 ✕1 · 1 of 40
 * sources failed". Empty when the rule has not run.
 */
export function describeLastResult(rule) {
  const r = rule.lastResult;
  if (!r) return '';
  const parts = [r.status];
  if (rule.sync) {
    parts.push(`+${r.added || 0} ~${r.updated || 0} =${r.unchanged || 0} −${r.deleted || 0} ✕${r.failed || 0}`);
  }
  if (r.error) parts.push(r.error);
  return parts.join(' · ');
}
