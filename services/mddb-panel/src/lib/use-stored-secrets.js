import { useCallback, useEffect, useState } from 'react';
import mddbClient from './mddb-client';

/**
 * The stored secrets (GET /v1/chat/mcp-secrets) a form names by name: a sync's
 * token and webhook secrets, a collection's repository token, the default
 * forge tokens (INT-023). Never a value — the server returns none.
 *
 * Returns `{ secrets, error, encryptionReady, reload }`: `error` is the
 * message of a listing that failed (admin only, or the module is off), and
 * `encryptionReady` is false only when the server says it has no at-rest key.
 * An answer that arrives after unmount is dropped.
 */
export function useStoredSecrets() {
  const [secrets, setSecrets] = useState([]);
  const [error, setError] = useState(null);
  const [encryptionReady, setEncryptionReady] = useState(true);
  const [reloads, setReloads] = useState(0);

  useEffect(() => {
    let cancelled = false;
    mddbClient.listChatMcpSecrets().then(
      (data) => {
        if (cancelled) return;
        setSecrets(data.secrets || []);
        setEncryptionReady(data.encryptionReady !== false);
        setError(null);
      },
      (err) => { if (!cancelled) setError(err.message); },
    );
    return () => { cancelled = true; };
  }, [reloads]);

  const reload = useCallback(() => setReloads((n) => n + 1), []);
  return { secrets, error, encryptionReady, reload };
}
