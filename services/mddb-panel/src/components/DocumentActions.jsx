import { useCallback, useEffect, useId, useRef, useState } from 'react';
import { MoreHorizontal, GitCompare, Download, Printer, FilePlus } from 'lucide-react';
import DocumentCompare from './DocumentCompare';
import DocumentPrint from './DocumentPrint';
import SaveTemplateDialog from './SaveTemplateDialog';
import { downloadDocumentHtml } from '../lib/document-export';

/**
 * DocumentActions is the viewer's "More" menu: Compare with… (FE-013),
 * Download HTML and Print / PDF (FE-017) and Save as template (FE-018). It
 * owns the overlay each action opens, so the viewer only places the button.
 *
 * The menu is a disclosure: the button says whether it is open
 * (aria-expanded), Escape or a click elsewhere closes it, and every item is
 * a plain button reached by Tab.
 */
export default function DocumentActions({ document }) {
  const id = useId();
  const root = useRef(null);
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(null);
  const [error, setError] = useState(null);
  const done = useCallback(() => setActive(null), []);

  useEffect(() => {
    if (!open) return undefined;
    const onKey = (e) => { if (e.key === 'Escape') setOpen(false); };
    const onClick = (e) => { if (!root.current.contains(e.target)) setOpen(false); };
    globalThis.document.addEventListener('keydown', onKey);
    globalThis.document.addEventListener('mousedown', onClick);
    return () => {
      globalThis.document.removeEventListener('keydown', onKey);
      globalThis.document.removeEventListener('mousedown', onClick);
    };
  }, [open]);

  const downloadHtml = async () => {
    setError(null);
    try {
      await downloadDocumentHtml(document);
    } catch (err) {
      setError(`Download failed: ${err.message}`);
    }
  };

  const items = [
    { name: 'compare', label: 'Compare with…', icon: GitCompare, run: () => setActive('compare') },
    { name: 'html', label: 'Download HTML', icon: Download, run: downloadHtml },
    { name: 'print', label: 'Print / PDF', icon: Printer, run: () => setActive('print') },
    { name: 'template', label: 'Save as template', icon: FilePlus, run: () => setActive('template') },
  ];

  return (
    <div ref={root} className="relative">
      <button
        type="button"
        aria-expanded={open}
        aria-controls={`${id}-menu`}
        onClick={() => { setError(null); setOpen(!open); }}
        className="flex items-center gap-2 px-3 py-1.5 text-sm text-gray-800 bg-gray-100 rounded-lg hover:bg-gray-200"
      >
        <MoreHorizontal className="w-4 h-4" aria-hidden="true" />
        <span>More</span>
      </button>
      {open && (
        <ul id={`${id}-menu`} className="absolute right-0 top-full mt-1 z-20 min-w-[12rem] py-1 bg-white border border-gray-200 rounded-lg shadow-lg">
          {items.map(({ name, label, icon: Icon, run }) => (
            <li key={name}>
              <button
                type="button"
                onClick={() => { setOpen(false); run(); }}
                className="flex w-full items-center gap-2 px-4 py-2 text-left text-sm text-gray-800 hover:bg-gray-100"
              >
                <Icon className="w-4 h-4" aria-hidden="true" />
                {label}
              </button>
            </li>
          ))}
        </ul>
      )}
      {error && <p role="alert" className="absolute right-0 top-full mt-1 w-64 rounded-lg border border-red-200 bg-red-50 p-2 text-xs text-red-800">{error}</p>}
      {active === 'compare' && <DocumentCompare document={document} onClose={done} />}
      {active === 'print' && <DocumentPrint document={document} onDone={done} />}
      {active === 'template' && <SaveTemplateDialog document={document} onClose={done} />}
    </div>
  );
}
