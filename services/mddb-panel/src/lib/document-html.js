/**
 * The standalone HTML file of a document export (FE-017): the page around
 * the body markup, which DocumentPrintView renders through MarkdownView —
 * the sanitizer the viewer uses — so the export opens no new XSS path.
 *
 * The page also carries a Content-Security-Policy that allows no script at
 * all, so even markup the sanitizer let through could not run when the
 * file is opened from disk.
 */

/** Escapes the five characters that matter in HTML text and attributes. */
export function escapeHtml(text) {
  return String(text ?? '').replace(/[&<>"']/g, (c) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  })[c]);
}

/**
 * A document language as an HTML `lang` attribute: "en_US" becomes
 * "en-US"; anything that is not a language tag gives "" (no attribute).
 */
export function htmlLang(lang) {
  const tag = String(lang ?? '').replace(/_/g, '-');
  return /^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8})*$/.test(tag) ? tag : '';
}

/**
 * The download name of a document export: its key with path separators and
 * characters file systems refuse replaced, then its language and `ext`,
 * e.g. "docs-intro.en_US.html".
 */
export function exportFileName(doc, ext) {
  const safe = (s) => String(s ?? '').replace(/[\\/:*?"<>|\s]+/g, '-').replace(/^[-.]+|-+$/g, '');
  const name = [safe(doc.key) || 'document', safe(doc.lang)].filter(Boolean).join('.');
  return `${name}.${ext}`;
}

/** The inline style sheet of an exported page: readable without the panel's CSS. */
export const exportCss = `
body { margin: 0; background: #ffffff; color: #1f2937; font: 16px/1.6 system-ui, -apple-system, "Segoe UI", Roboto, sans-serif; }
main { max-width: 48rem; margin: 0 auto; padding: 2rem 1rem; overflow-wrap: anywhere; }
h1, h2, h3, h4 { color: #111827; line-height: 1.25; }
a { color: #0b57d0; }
pre, code { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 0.9em; }
pre, code.block { display: block; overflow-x: auto; padding: 1rem; background: #f3f4f6; border-radius: 6px; white-space: pre-wrap; }
code { background: #f3f4f6; padding: 0.1em 0.3em; border-radius: 4px; }
table { border-collapse: collapse; margin: 1rem 0; }
th, td { border: 1px solid #d1d5db; padding: 0.4rem 0.6rem; text-align: left; }
blockquote { margin: 1rem 0; padding-left: 1rem; border-left: 4px solid #d1d5db; color: #374151; }
img { max-width: 100%; height: auto; }
dl { display: grid; grid-template-columns: max-content 1fr; gap: 0.25rem 1rem; font-size: 0.9rem; }
dl > div { display: contents; }
dt { font-weight: 600; }
dd { margin: 0; }
.document-meta-line { color: #4b5563; font-size: 0.9rem; }
`.trim();

/**
 * The complete HTML page of an export: `body` is markup already rendered
 * and sanitized; `title` and `lang` are escaped here.
 */
export function standaloneHtml({ title, lang, body }) {
  const langAttr = htmlLang(lang);
  return [
    '<!doctype html>',
    `<html${langAttr ? ` lang="${langAttr}"` : ''}>`,
    '<head>',
    '<meta charset="utf-8">',
    '<meta name="viewport" content="width=device-width, initial-scale=1">',
    `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src * data:; style-src 'unsafe-inline'">`,
    '<meta name="generator" content="mddb-panel">',
    `<title>${escapeHtml(title)}</title>`,
    `<style>${exportCss}</style>`,
    '</head>',
    `<body><main>${body}</main></body>`,
    '</html>',
    '',
  ].join('\n');
}
