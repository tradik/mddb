import { useCallback, useState } from 'react';

/**
 * The busy/error bookkeeping every form of the admin pages repeats: `run(fn)`
 * marks the action busy, clears the last error, awaits `fn()` and returns its
 * result — or keeps the thrown error (for ApiErrorNotice) and returns
 * undefined. `busy` is true while an action runs.
 */
export function useAction() {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(null);

  const run = useCallback(async (fn) => {
    setBusy(true);
    setError(null);
    try {
      return await fn();
    } catch (err) {
      setError(err);
      return undefined;
    } finally {
      setBusy(false);
    }
  }, []);

  return { busy, error, setError, run };
}
