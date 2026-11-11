import PermissionScopeHint from './PermissionScopeHint';

/**
 * ApiErrorNotice shows a failed call as the server worded it, in an alert.
 * A 403 is a missing permission, not a broken page: it reads "Not permitted"
 * and explains, with PermissionScopeHint, which grant the action needs —
 * `collection` is the collection the call named ("*" for server-wide ones)
 * and `admin` says the action needs admin rather than read or write.
 */
export default function ApiErrorNotice({ error, collection = '*', admin = false }) {
  if (!error) return null;
  const message = typeof error === 'string' ? error : error.message;
  if (error.status === 403) {
    // The server's own words; the client's message appends the general rules
    // that PermissionScopeHint states below.
    return (
      <div role="alert" className="rounded-lg border border-amber-300 bg-amber-50 p-3 text-sm text-amber-950 space-y-1">
        <p className="font-medium">Not permitted</p>
        <p>{error.body?.error || message}</p>
        <PermissionScopeHint collection={collection} admin={admin} />
      </div>
    );
  }
  return (
    <p role="alert" className="rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-800">{message}</p>
  );
}
