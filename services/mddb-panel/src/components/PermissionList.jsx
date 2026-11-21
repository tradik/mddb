import { useState } from 'react';
import { Trash2 } from 'lucide-react';
import { adminScopeLabel } from '../lib/permissions';
import ConfirmDialog from './ConfirmDialog';

/**
 * PermissionList is the "Current Permissions" list of the user and group
 * permission modals: one row per collection with its Read / Write / Admin
 * badges and a remove button (#438). Removing asks first; on confirm it
 * awaits `onRemove(collection)`, during which every remove button and the
 * confirmation are disabled. `onRemove` owns the request, the reload and the
 * error message, so it should not throw.
 */
export default function PermissionList({ permissions, loading, onRemove }) {
  const [pending, setPending] = useState(null);
  const [busy, setBusy] = useState(false);

  const confirmRemove = async () => {
    setBusy(true);
    try {
      await onRemove(pending);
    } finally {
      setBusy(false);
      setPending(null);
    }
  };

  if (loading) {
    return <div className="text-center py-4">Loading...</div>;
  }
  if (permissions.length === 0) {
    return <div className="text-sm text-fg-faint bg-surface-muted rounded p-4">No permissions set</div>;
  }

  return (
    <>
      <ul className="space-y-2">
        {permissions.map((perm) => {
          const label = `Remove permission on ${perm.collection}`;
          return (
            <li key={perm.collection} className="bg-surface-muted rounded p-3 flex items-center justify-between">
              <div>
                <span className="font-medium text-fg">{perm.collection}</span>
                <div className="flex gap-2 mt-1">
                  {perm.read && (
                    <span className="px-2 py-0.5 text-xs bg-success-muted text-success-strong rounded">Read</span>
                  )}
                  {perm.write && (
                    <span className="px-2 py-0.5 text-xs bg-info-muted text-info-strong rounded">Write</span>
                  )}
                  {perm.admin && (
                    <span className="px-2 py-0.5 text-xs bg-note-muted text-note-strong rounded">
                      {adminScopeLabel(perm.collection)}
                    </span>
                  )}
                </div>
              </div>
              <button
                type="button"
                onClick={() => setPending(perm.collection)}
                disabled={busy}
                aria-label={label}
                title={label}
                className="p-1 rounded text-danger hover:text-danger-strong disabled:opacity-50"
              >
                <Trash2 className="w-4 h-4" aria-hidden="true" />
              </button>
            </li>
          );
        })}
      </ul>
      {pending !== null && (
        <ConfirmDialog
          title="Remove permission"
          message={`Remove the grant on "${pending}"? It stops applying at once, to tokens already issued too. The other grants stay.`}
          confirmLabel="Remove"
          busy={busy}
          onConfirm={() => void confirmRemove()}
          onCancel={() => setPending(null)}
        />
      )}
    </>
  );
}
