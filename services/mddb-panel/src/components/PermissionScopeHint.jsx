// SEC-020 (2.18.10): admin on one collection no longer makes a global admin.
// Only admin on "*" manages users and groups, backups, quotas, corpus packs,
// the diagnose bundle and other server-wide operations; admin on a named
// collection covers that collection. The permission forms say which one the
// operator is about to grant.
export default function PermissionScopeHint({ collection, admin }) {
  const global = collection.trim() === '*';
  return (
    <p className="text-xs text-gray-700">
      {admin && !global && (
        <strong className="block text-amber-900">
          Admin on &quot;{collection.trim() || '…'}&quot; covers that collection only — not server-wide.
        </strong>
      )}
      Only admin on &quot;*&quot; is global: it manages users and groups, backups, quotas, corpus packs and
      the diagnose bundle. A request that names no collection needs its permission on &quot;*&quot;.
    </p>
  );
}
