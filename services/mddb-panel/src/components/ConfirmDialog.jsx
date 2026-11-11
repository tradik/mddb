import { useEffect, useId, useRef } from 'react';
import { AlertTriangle } from 'lucide-react';

/**
 * ConfirmDialog asks before a destructive or server-wide action. It is a
 * modal alertdialog: focus moves to Cancel when it opens (the safe choice),
 * Escape cancels, Tab stays inside, and focus returns to whatever opened it.
 * `message` should name what is affected — the collection, the key, the file.
 */
export default function ConfirmDialog({ title, message, confirmLabel = 'Delete', busy = false, onConfirm, onCancel }) {
  const titleId = useId();
  const messageId = useId();
  const cancelRef = useRef(null);
  const dialogRef = useRef(null);

  useEffect(() => {
    const opener = document.activeElement;
    cancelRef.current?.focus();
    return () => opener?.focus?.();
  }, []);

  const onKeyDown = (e) => {
    if (e.key === 'Escape') {
      e.stopPropagation();
      onCancel();
      return;
    }
    if (e.key !== 'Tab') return;
    const buttons = [...dialogRef.current.querySelectorAll('button:not([disabled])')];
    const first = buttons[0];
    const last = buttons.at(-1);
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault();
      first.focus();
    }
  };

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4">
      <div
        ref={dialogRef}
        role="alertdialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={messageId}
        onKeyDown={onKeyDown}
        className="bg-white rounded-lg shadow-xl p-6 max-w-md w-full"
      >
        <h2 id={titleId} className="flex items-center gap-2 text-lg font-semibold text-gray-900 mb-3">
          <AlertTriangle className="w-5 h-5 text-red-700" aria-hidden="true" />
          {title}
        </h2>
        <div id={messageId} className="text-sm text-gray-800 mb-6">{message}</div>
        <div className="flex justify-end gap-3">
          <button
            ref={cancelRef}
            type="button"
            onClick={onCancel}
            className="px-4 py-2 text-gray-800 bg-gray-100 hover:bg-gray-200 rounded-lg"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={onConfirm}
            disabled={busy}
            className="px-4 py-2 text-white bg-red-700 hover:bg-red-800 rounded-lg disabled:opacity-50"
          >
            {busy ? 'Working…' : confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
