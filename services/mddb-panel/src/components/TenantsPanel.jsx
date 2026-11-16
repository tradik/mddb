import { useCallback, useEffect, useState } from 'react';
import { Building2, RefreshCw } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useMe } from '../lib/use-me';
import TenantsTable from './TenantsTable';
import TenantDetail from './TenantDetail';

// GO-054 (2.19.8): the Tenants page under Security. A global admin sees every
// tenant — configured, or assigned to a user — with what its collections hold
// against its quota, its queues and its refused requests, and sets its quota
// and request limits. Anyone else is refused the list (403); a tenant's own
// admin then sees the tenant's own report, read-only.

/** The text for a list that could not be loaded and no fallback. */
function loadError(err, me) {
  if (err.status === 403 && !me?.tenant) {
    return 'Only a global admin sees every tenant, and your account belongs to no tenant, so there is no tenant report to show.';
  }
  if (err.status === 403) {
    return `Only a global admin sees every tenant, and the report of your tenant "${me.tenant}" was refused too: it needs read on every collection of the tenant ("*").`;
  }
  return `Could not load the tenants: ${err.message}`;
}

export default function TenantsPanel() {
  const { me, error: meError } = useMe();
  const [tenants, setTenants] = useState([]);
  const [admin, setAdmin] = useState(false);
  const [selected, setSelected] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const meKnown = me !== null || meError !== null;

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await mddbClient.listTenants();
      setTenants(res.tenants || []);
      setAdmin(true);
    } catch (err) {
      setAdmin(false);
      setTenants([]);
      if (err.status === 403 && me?.tenant) {
        try {
          const own = await mddbClient.getTenantUsage(me.tenant);
          setTenants([own]);
          setSelected(own.tenant);
        } catch (ownErr) {
          setError(loadError(ownErr, me));
        }
      } else {
        setError(loadError(err, me));
      }
    } finally {
      setLoading(false);
    }
  }, [me]);

  useEffect(() => {
    if (meKnown) void load();
  }, [meKnown, load]);

  const report = tenants.find((t) => t.tenant === selected);

  return (
    <div className="h-full overflow-y-auto bg-surface-muted p-6">
      <div className="mb-4 flex items-center justify-between">
        <div className="flex items-center gap-2">
          <Building2 className="h-6 w-6 text-accent" aria-hidden="true" />
          <h2 className="text-xl font-bold text-fg">Tenants</h2>
        </div>
        <button
          type="button"
          onClick={() => void load()}
          disabled={loading}
          className="flex items-center gap-1.5 rounded px-3 py-1.5 text-sm text-fg-soft hover:bg-surface-subtle disabled:opacity-50"
        >
          <RefreshCw className="h-4 w-4" aria-hidden="true" /> Reload
        </button>
      </div>
      <p className="mb-4 max-w-3xl text-sm text-fg-muted">
        A tenant owns the collections named &quot;tenant/…&quot;. Its quota caps what all of them hold together, its
        request limits cap how fast and how many at once its users and API keys may call the server, and its jobs
        share the indexing and embedding queues in turn with other tenants&apos;. A tenant without a quota or limits
        of its own has only the server&apos;s defaults. Usage is read when the page loads.
      </p>

      {error && <p role="alert" className="mb-3 rounded border border-danger-line bg-danger-soft p-2 text-sm text-danger-strong">{error}</p>}

      {loading && tenants.length === 0 && <p className="text-sm text-fg-muted">Loading tenants…</p>}
      {!loading && !error && tenants.length === 0 && (
        <p className="text-sm text-fg-muted">No tenants yet: none is configured and no user is assigned to one.</p>
      )}
      {tenants.length > 0 && <TenantsTable tenants={tenants} selected={selected} onSelect={setSelected} />}
      {report && <TenantDetail report={report} admin={admin} onSaved={load} />}
    </div>
  );
}
