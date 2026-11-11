import { useEffect, useState } from 'react';
import mddbClient from './mddb-client';

/**
 * The signed-in account from /v1/auth/me — `{ username, admin, createdAt }`
 * — loaded once per mount. `error` keeps a failed lookup (no auth, expired
 * session) for the caller to show or ignore.
 */
export function useMe() {
  const [me, setMe] = useState(null);
  const [error, setError] = useState(null);

  useEffect(() => {
    mddbClient.getMe().then(setMe).catch(setError);
  }, []);

  return { me, error };
}
