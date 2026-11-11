import { UserRound } from 'lucide-react';
import { useMe } from '../lib/use-me';
import { formatUnix } from '../lib/time-format';
import ApiErrorNotice from './ApiErrorNotice';
import MyApiKeysPanel from './MyApiKeysPanel';

/**
 * AccountPanel is the My Account page: who is signed in (/v1/auth/me) and
 * that account's own API keys. Every signed-in user may use it, admin or not.
 */
export default function AccountPanel() {
  const { me, error } = useMe();

  return (
    <div className="h-full overflow-y-auto p-6 bg-gray-50 space-y-4">
      <h2 className="text-xl font-bold text-gray-900 flex items-center gap-2">
        <UserRound className="w-5 h-5" aria-hidden="true" /> My Account
      </h2>
      <section aria-label="Signed-in account" className="bg-white rounded-lg shadow p-4">
        <ApiErrorNotice error={error} />
        {me && (
          <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
            <dt className="text-gray-700">User</dt>
            <dd className="font-medium text-gray-900">{me.username}</dd>
            <dt className="text-gray-700">Role</dt>
            <dd className="text-gray-900">{me.admin ? 'Global admin (admin on "*")' : 'Per-collection permissions'}</dd>
            <dt className="text-gray-700">Created</dt>
            <dd className="text-gray-900">{formatUnix(me.createdAt)}</dd>
          </dl>
        )}
      </section>
      <MyApiKeysPanel />
    </div>
  );
}
