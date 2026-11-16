import { useEffect, useId, useRef } from 'react';
import { AlertTriangle, Loader2 } from 'lucide-react';
import ApiErrorNotice from './ApiErrorNotice';

/**
 * ClusterActionDialog confirms a leader election action of the Cluster tab
 * (GO-051): promote this node or step it down. Focus starts on Cancel (the
 * safe choice) and returns to the opener on close; Escape cancels. A refusal
 * (409, or 400 outside a cluster) stays in the dialog in the server's words.
 */
export default function ClusterActionDialog({ title, confirmLabel, busy, error, onConfirm, onCancel, children }) {
  const id = useId();
  const cancelRef = useRef(null);

  useEffect(() => {
    const opener = document.activeElement;
    cancelRef.current?.focus();
    return () => opener?.focus?.();
  }, []);

  useEffect(() => {
    const onKeyDown = (e) => {
      if (e.key === 'Escape') onCancel();
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [onCancel]);

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4">
      <dialog
        open
        aria-modal="true"
        aria-labelledby={`${id}-title`}
        className="relative m-0 border-0 bg-surface text-fg rounded-lg shadow-xl w-full max-w-md p-6 space-y-4"
      >
        <h3 id={`${id}-title`} className="flex items-center gap-2 text-lg font-semibold text-fg">
          <AlertTriangle className="w-5 h-5 text-warning" aria-hidden="true" />
          {title}
        </h3>
        <div className="text-sm text-fg-soft space-y-3">{children}</div>
        <ApiErrorNotice error={error} admin />
        <div className="flex justify-end gap-3">
          <button
            ref={cancelRef}
            type="button"
            onClick={onCancel}
            className="px-4 py-2 text-sm text-fg-soft bg-surface-subtle rounded-lg hover:bg-surface-strong focus-visible:bg-surface-strong"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={onConfirm}
            disabled={busy}
            className="flex items-center gap-2 px-4 py-2 text-sm text-white bg-primary-700 rounded-lg hover:bg-primary-800 focus-visible:bg-primary-800 disabled:opacity-50"
          >
            {busy && <Loader2 className="w-4 h-4 animate-spin" aria-hidden="true" />}
            {busy ? 'Working…' : confirmLabel}
          </button>
        </div>
      </dialog>
    </div>
  );
}
