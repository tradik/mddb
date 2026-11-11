/**
 * Imports that are not file uploads: a MediaWiki XML dump (/v1/import-wiki,
 * multipart; .xml or .xml.bz2, streamed by the server). URL import is the
 * client's existing `importURL`.
 */

export const importMethods = {
  /**
   * Uploads a wiki dump. Options are sent as form fields; empty ones are left
   * out so the server's defaults apply. Answers
   * `{ imported, skipped, failed, collection, durationMs }`.
   */
  importWiki({ file, collection, lang, namespaces, skipRedirects, skipFts, maxPages, signal }) {
    const form = new FormData();
    form.append('file', file);
    form.append('collection', collection);
    form.append('lang', lang);
    if (namespaces) form.append('namespaces', namespaces);
    if (skipRedirects) form.append('skipRedirects', 'true');
    if (skipFts) form.append('skipFts', 'true');
    if (maxPages) form.append('maxPages', String(maxPages));
    return this.postForm('/import-wiki', form, { signal, errorPrefix: 'Import Error' });
  },
};
