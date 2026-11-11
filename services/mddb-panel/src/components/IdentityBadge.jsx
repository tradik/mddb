import { UserRound } from 'lucide-react';
import { useMe } from '../lib/use-me';

/**
 * IdentityBadge names the signed-in account in the header (/v1/auth/me), with
 * "admin" for a global admin. Nothing is shown until the server answers, or
 * when it cannot say.
 */
export default function IdentityBadge() {
  const { me } = useMe();
  if (!me) return null;
  return (
    <span className="flex items-center gap-1 text-sm text-gray-800" title="Signed in">
      <UserRound className="w-4 h-4" aria-hidden="true" />
      <span className="sr-only">Signed in as</span>
      <span className="font-medium">{me.username}</span>
      {me.admin && <span className="rounded bg-gray-100 px-1.5 py-0.5 text-xs text-gray-900">admin</span>}
    </span>
  );
}
