import { useEffect, useState } from 'react';
import { Shield } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { edgeRows } from '../lib/server-edge';

// Search surface, tracing and proxy settings from GET /v1/config (2.18.10):
// the Elasticsearch subset and its prefix, the server's result cap,
// OpenTelemetry export (the collector's scheme and host only) and how the
// client address is found behind a proxy (how many proxies are trusted,
// never which). The Config page passes its config; the System page lets the
// card load it.

export default function ServerEdgeCard({ config: given }) {
  const [loaded, setLoaded] = useState(null);
  const [error, setError] = useState(null);

  useEffect(() => {
    if (given) return;
    mddbClient.getConfig().then(setLoaded).catch((e) => setError(e.message));
  }, [given]);

  const config = given || loaded;

  return (
    <section aria-labelledby="server-edge-title" className="bg-surface rounded-lg shadow mb-6 p-6">
      <div className="flex items-center mb-4">
        <Shield className="w-5 h-5 text-accent mr-2" aria-hidden="true" />
        <h2 id="server-edge-title" className="text-lg font-semibold text-fg">Search surface, tracing &amp; proxies</h2>
      </div>
      {error && <p className="text-sm text-danger">Could not load the server configuration: {error}</p>}
      {!error && !config && <p className="text-sm text-fg-subtle">Loading…</p>}
      {config && (
        <dl className="divide-y">
          {edgeRows(config).map(([label, value]) => (
            <div key={label} className="flex justify-between gap-4 py-2">
              <dt className="text-sm text-fg-subtle">{label}</dt>
              <dd className="text-sm font-medium text-fg text-right break-all">{value}</dd>
            </div>
          ))}
        </dl>
      )}
    </section>
  );
}
