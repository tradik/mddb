import mddbClient from './mddb-client';
import { templates, templateLabels } from './markdown-templates';

/**
 * Document templates (FE-018): the built-in ones shipped with the panel and
 * a team's own, kept as ordinary documents in the `_templates` collection —
 * shared by every panel on the server, with revisions, backups and
 * replication, and no API of their own. A template document's key is the
 * template's name, its Markdown the starting content and its metadata the
 * new document's default metadata.
 *
 * Reading the collection needs read permission on it and saving a template
 * needs write permission; the server enforces both. A missing collection is
 * simply no custom templates, and the first saved template creates it.
 */

/** The collection that holds the custom templates. */
export const templateCollection = '_templates';

/** How many custom templates the pickers list. */
export const templateLimit = 200;

/** The built-in templates as picker entries: `{ id, name, contentMd, meta }`. */
export function builtinTemplates() {
  return Object.entries(templateLabels).map(([type, name]) => ({
    id: `builtin:${type}`,
    name,
    contentMd: templates[type],
    meta: {},
  }));
}

/**
 * A `_templates` document as a picker entry; every metadata value becomes a
 * list, the shape the new-document form edits.
 */
export function toTemplate(doc) {
  const meta = Object.entries(doc.meta || {}).map(([k, v]) => [k, Array.isArray(v) ? v : [String(v)]]);
  return {
    id: `custom:${doc.key}:${doc.lang}`,
    name: doc.key,
    lang: doc.lang,
    contentMd: doc.contentMd || '',
    meta: Object.fromEntries(meta),
  };
}

/**
 * The custom templates, by name: `{ templates, error }`. A failed read
 * (no read permission, server down) gives no templates and the error, so
 * the picker still offers the built-in ones.
 */
export async function loadCustomTemplates(client = mddbClient) {
  try {
    const { documents } = await client.search({ collection: templateCollection, sort: 'key', asc: true, limit: templateLimit });
    return { templates: documents.map(toTemplate), error: null };
  } catch (error) {
    return { templates: [], error };
  }
}

/**
 * Saves `document` as the template `name` (in the document's language),
 * with its metadata as the defaults when `includeMeta` is set. A template of
 * that name and language is replaced; the server keeps the old one as a revision.
 */
export function saveTemplate({ name, document, includeMeta }, client = mddbClient) {
  return client.addDocument({
    collection: templateCollection,
    key: name.trim(),
    lang: document.lang,
    meta: includeMeta ? document.meta || {} : {},
    contentMd: document.contentMd || '',
  });
}
