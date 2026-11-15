import { useEffect } from 'react';
import { createPortal } from 'react-dom';
import DocumentPrintView from './DocumentPrintView';

/**
 * DocumentPrint is Print / PDF (FE-017): while mounted it puts the document,
 * as DocumentPrintView, straight under <body> and opens the browser's print
 * dialog, where "Save as PDF" makes the PDF — no PDF library. The print
 * style sheet in index.css hides everything else under <body> (the panel)
 * and shows only `.mddb-print-root`; on screen that root stays hidden.
 *
 * `onDone` runs when the dialog closes (`afterprint`), and the caller
 * unmounts this component then.
 */
export default function DocumentPrint({ document, onDone }) {
  useEffect(() => {
    window.addEventListener('afterprint', onDone, { once: true });
    window.print();
    return () => window.removeEventListener('afterprint', onDone);
  }, [onDone]);

  return createPortal(
    <div className="mddb-print-root">
      <DocumentPrintView document={document} />
    </div>,
    globalThis.document.body,
  );
}
