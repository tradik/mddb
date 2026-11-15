import { useEffect, useId, useRef } from 'react';
import { Keyboard } from 'lucide-react';
import { shortcutList } from '../lib/editor-shortcuts';

/**
 * ShortcutsDialog (FE-016): the list of keyboard shortcuts, opened with `?`.
 * A modal dialog: focus moves to Close when it opens, Escape closes it, Tab
 * stays on Close, and focus returns to whatever had it before.
 */
export default function ShortcutsDialog({ isMac, onClose }) {
  const titleId = useId();
  const closeRef = useRef(null);

  useEffect(() => {
    const opener = document.activeElement;
    closeRef.current.focus();
    return () => opener?.focus?.();
  }, []);

  const onKeyDown = (e) => {
    if (e.key === 'Escape') {
      e.stopPropagation();
      onClose();
    } else if (e.key === 'Tab') {
      e.preventDefault();
    }
  };

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4">
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        onKeyDown={onKeyDown}
        className="bg-white rounded-lg shadow-xl p-6 max-w-md w-full"
      >
        <h2 id={titleId} className="flex items-center gap-2 text-lg font-semibold text-gray-900 mb-4">
          <Keyboard className="w-5 h-5 text-gray-700" aria-hidden="true" />
          Keyboard shortcuts
        </h2>
        <table className="w-full text-sm">
          <tbody>
            {shortcutList(isMac).map(({ keys, description }) => (
              <tr key={keys} className="border-t border-gray-200">
                <th scope="row" className="py-1.5 pr-4 text-left font-mono font-medium text-gray-900 whitespace-nowrap">{keys}</th>
                <td className="py-1.5 text-gray-700">{description}</td>
              </tr>
            ))}
          </tbody>
        </table>
        <p className="mt-3 text-xs text-gray-700">
          <kbd className="font-mono">/</kbd> and <kbd className="font-mono">?</kbd> work when the focus is not in a form field.
        </p>
        <div className="mt-4 flex justify-end">
          <button
            ref={closeRef}
            type="button"
            onClick={onClose}
            className="px-4 py-2 text-gray-800 bg-gray-100 hover:bg-gray-200 rounded-lg"
          >
            Close
          </button>
        </div>
      </div>
    </div>
  );
}
