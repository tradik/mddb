/**
 * The `sync` action of a cron rule (INT-022): its form state, its REST
 * payload and one line describing it — a sitemap, a list of URLs, an S3
 * prefix, a git repository at a branch, a tag or its latest release
 * (2.19.5, #421), or a forge repository's issues and pull requests as a
 * read-only mirror (INT-023).
 *
 * A git or issues sync names stored secrets (the write-only store the chat's
 * MCP servers use) for a private repository's token and for the webhook;
 * their values never pass through this form. Its URL may be left empty when
 * the server can resolve the repository: from the collection named in
 * `repositoryFrom`, else from the rule's target collection.
 */

const MIB = 1024 * 1024;

export const EMPTY_STORAGE = { endpoint: '', region: '', accessKey: '', secretKey: '', useTLS: true, secretKeySet: false };

export const EMPTY_SYNC = {
  source: 'sitemap', collection: '', lang: 'en', url: '', urls: '', bucket: '', prefix: '',
  prune: false, maxDocuments: '', maxMiB: '', storageConfig: EMPTY_STORAGE,
  // git (2.19.5)
  ref: '', include: '', exclude: '', forge: '', tokenSecretRef: '', hookSecretRef: '', adopt: false,
  // git and issues (INT-023): the collection whose repository block is used
  repositoryFrom: '',
  // issues (INT-023); `include` stays the git globs, so the kinds are two flags
  state: 'all', since: '', includeIssues: true, includePulls: false, comments: true,
};

/** The ref a git sync follows when it names none. */
export const LATEST_RELEASE = 'latest-release';

/** Hosts whose forge the server knows without `forge`. */
const KNOWN_FORGE_HOSTS = new Set(['github.com', 'gitlab.com', 'codeberg.org']);

/** A positive whole number from a form field, or undefined. */
export function positive(value) {
  const n = Number(value);
  return Number.isInteger(n) && n > 0 ? n : undefined;
}

/** Copies the keys of `obj` whose values are not undefined or ''. */
export function compact(obj) {
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

/** The form state of a stored S3 block (its secret is never returned). */
export function storageToForm(sc) {
  return { ...EMPTY_STORAGE, ...sc, secretKey: '', useTLS: sc?.useTLS ?? false };
}

/** Globs typed one per line (or comma-separated), as a list. */
function globList(text) {
  return text.split(/[\n,]/).map((g) => g.trim()).filter(Boolean);
}

/**
 * Whether a repository URL's host needs `forge`: any host but github.com,
 * gitlab.com and codeberg.org. False for a URL that does not parse yet.
 */
export function needsForge(url) {
  try {
    return !KNOWN_FORGE_HOSTS.has(new URL(url).host.toLowerCase());
  } catch {
    return false;
  }
}

/** The repository fields a git and an issues sync share; blanks are left out. */
function repositoryFields(s) {
  return compact({
    url: s.url.trim(),
    forge: needsForge(s.url) ? s.forge : '',
    repositoryFrom: s.repositoryFrom,
    tokenSecretRef: s.tokenSecretRef,
    hookSecretRef: s.hookSecretRef,
  });
}

/** The git fields of a sync payload. */
function gitFields(s) {
  const include = globList(s.include);
  const exclude = globList(s.exclude);
  const out = { ...repositoryFields(s), ...compact({ ref: s.ref.trim() }) };
  if (include.length) out.include = include;
  if (exclude.length) out.exclude = exclude;
  if (s.adopt) out.adopt = true;
  return out;
}

/**
 * The issues fields of a sync payload (INT-023). `comments` is always sent,
 * since the server's default is on; no kind ticked leaves `include` to the
 * server's default (issues).
 */
function issuesFields(s) {
  const include = [s.includeIssues && 'issues', s.includePulls && 'pulls'].filter(Boolean);
  const out = { ...repositoryFields(s), ...compact({ state: s.state, since: s.since }), comments: s.comments };
  if (include.length) out.include = include;
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
  if (s.source === 'git') Object.assign(out, gitFields(s));
  if (s.source === 'issues') Object.assign(out, issuesFields(s));
  return out;
}

/** The form state of a stored sync block. */
export function syncToForm(s) {
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
    ref: s.ref || '',
    include: s.source === 'issues' ? '' : (s.include || []).join('\n'),
    exclude: (s.exclude || []).join('\n'),
    forge: s.forge || '',
    tokenSecretRef: s.tokenSecretRef || '',
    hookSecretRef: s.hookSecretRef || '',
    adopt: s.adopt === true,
    repositoryFrom: s.repositoryFrom || '',
    state: s.state || 'all',
    since: s.since || '',
    includeIssues: s.source !== 'issues' || !s.include || s.include.includes('issues'),
    includePulls: s.source === 'issues' && Boolean(s.include?.includes('pulls')),
    comments: s.comments !== false,
  };
}

/** A git or issues sync's repository: its URL, else where the server resolves it. */
function repositoryText(s) {
  return s.url || `the repository of ${s.repositoryFrom || s.collection}`;
}

/**
 * Where a sync reads from: a URL count, an S3 location, a repository at its
 * ref, a repository's issues, or the sitemap URL.
 */
export function syncSourceText(s) {
  if (s.source === 'urls') return `${(s.urls || []).length} URLs`;
  if (s.source === 's3') return `s3://${s.bucket}/${s.prefix || ''}`;
  if (s.source === 'git') return `${repositoryText(s)} @ ${s.ref || 'default branch'}`;
  if (s.source === 'issues') return `${repositoryText(s)}, ${(s.include || ['issues']).join(' and ')} (${s.state || 'all'})`;
  return s.url;
}

/** A commit id as the forges show it: its first seven characters. */
export function shortCommit(commit) {
  return (commit || '').slice(0, 7);
}
