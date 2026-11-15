import DocumentPrintView from '../components/DocumentPrintView';
import { standaloneHtml, exportFileName } from './document-html';
import { saveBlob } from './download';

/**
 * Download HTML (FE-017): the document rendered by DocumentPrintView (so
 * through MarkdownView and its sanitizer) to static markup, wrapped in a
 * standalone page and saved as "<key>.<lang>.html".
 *
 * react-dom/server is imported on first use, so the panel's main bundle
 * does not carry it.
 */

/** The complete standalone HTML page of `document`. */
export async function documentHtml(document) {
  const { renderToStaticMarkup } = await import('react-dom/server');
  const body = renderToStaticMarkup(<DocumentPrintView document={document} />);
  return standaloneHtml({ title: document.key, lang: document.lang, body });
}

/** Saves `document` as a standalone HTML file. */
export async function downloadDocumentHtml(document) {
  const html = await documentHtml(document);
  const blob = new Blob([html], { type: 'text/html;charset=utf-8' });
  saveBlob({ blob, filename: exportFileName(document, 'html') });
}
