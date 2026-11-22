/**
 * Corpus packs as the panel shows them (FE-022). GET /v1/corpus lists one
 * entry per collection installed from a pack; a pack of several collections
 * has one entry for each, all carrying the pack's name. These helpers group
 * them back into packs, word the server's refusals for people, and build
 * the body of POST /v1/corpus/build — also to download an installed pack
 * again, built from its collections as they were recorded.
 */

/** "1.5 MB" for a byte count; "" when unknown. */
export function formatBytes(bytes) {
  if (!bytes) return '';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  return `${Math.round((bytes / 1024 ** i) * 10) / 10} ${units[i]}`;
}

/** One pack from its collections' entries (the same corpus.name). */
function toPack(name, entries) {
  const first = entries[0].corpus;
  const sources = entries.flatMap((e) => e.corpus.sources || []);
  return {
    name,
    version: first.version || '',
    license: first.packLicense || first.license,
    description: first.description || '',
    mddbVersion: first.mddbVersion,
    installedAt: first.installedAt,
    several: !!first.packLicense || entries.length > 1,
    readOnly: entries.every((e) => e.readOnly),
    vectorsDisabled: entries.some((e) => e.corpus.vectorsDisabled),
    docCount: entries.reduce((n, e) => n + (e.corpus.docCount || 0), 0),
    bytes: entries.reduce((n, e) => n + (e.corpus.bytes || 0), 0),
    sources,
    collections: entries.map((e) => ({
      name: e.collection, license: e.corpus.license, docCount: e.corpus.docCount || 0, sources: e.corpus.sources || [],
    })),
  };
}

/** The listed entries grouped into packs, by name. */
export function groupPacks(entries = []) {
  const byName = new Map();
  for (const entry of entries) {
    const name = entry.corpus?.name || entry.collection;
    byName.set(name, [...(byName.get(name) || []), entry]);
  }
  return [...byName.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([name, list]) => toPack(name, list));
}

/** Drops empty strings and empty lists, so the server sees only what was given. */
function compact(body) {
  return Object.fromEntries(Object.entries(body).filter(([, v]) => (Array.isArray(v) ? v.length > 0 : v !== '' && v != null)));
}

/** Sources with a web address; each takes the pack's licence unless it names one. */
function cleanSources(sources = [], license = '') {
  return sources
    .filter((s) => s.repo?.trim())
    .map((s) => compact({ repo: s.repo.trim(), commit: s.commit?.trim(), license: s.license?.trim() || license }));
}

/**
 * The build body for the form `{ collections, name, version, description,
 * license, sources, licenses }`: one collection travels as `collection`,
 * several as `collections` (with a licence where `licenses[name]` differs)
 * under the pack `name`.
 */
export function buildRequest(form) {
  const license = form.license.trim();
  const common = { license, version: form.version?.trim(), description: form.description?.trim(), sources: cleanSources(form.sources, license) };
  if (form.collections.length === 1) return compact({ collection: form.collections[0], ...common });
  const collections = form.collections.map((name) => {
    const own = form.licenses?.[name]?.trim();
    return own && own !== license ? { name, license: own } : name;
  });
  return compact({ name: form.name.trim(), collections, ...common });
}

/** The build body that makes an installed pack again from its collections. */
export function rebuildRequest(pack) {
  const common = { license: pack.license, version: pack.version, description: pack.description };
  if (!pack.several) return compact({ collection: pack.collections[0].name, ...common, sources: pack.sources });
  const collections = pack.collections.map((c) => compact({ name: c.name, license: c.license, sources: c.sources }));
  return compact({ name: pack.name, collections, ...common });
}

/** The file a build is saved as when the server names none. */
export function packFileName(body) {
  const name = body.name || body.collection;
  return body.version ? `${name}-${body.version}.mddbpack` : `${name}.mddbpack`;
}

/** What went wrong with a pack, in plain words, or null to show the server's. */
export function explainPackError(error) {
  const text = error?.body?.error || error?.message || '';
  if (error?.status === 409 || /already (installed|holds data)/.test(text)) {
    return 'A pack or collection with this name is already on this server. Remove the installed pack first, or install a pack with another name.';
  }
  if (/needs a licence|has no licence/.test(text)) {
    return 'The pack does not say under which licence its documents may be shared, so this server will not install it. Ask whoever made it for a pack that names its licence.';
  }
  if (/is it a \.mddbpack|unsupported pack format|sha256|not a usable database|manifest/.test(text)) {
    return 'This file is not a pack this server can read: it may be damaged, cut short, or made by a newer version.';
  }
  return null;
}
