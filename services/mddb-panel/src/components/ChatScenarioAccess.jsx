import { useEffect, useState } from 'react';
import { ShieldCheck } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useStore } from '../lib/store';
import { useFeatures } from '../lib/features';
import { navConditions } from '../lib/nav-groups';
import { accessLinks, accessLabel, throughLabel, sortReaders } from '../lib/chat-scenario-access';

// #441: who can use a stored scenario. Links to the screens that decide it
// (shown only when the sidebar shows them), and the server's list of users
// with the rule that lets each read the scenario's collections
// (GET /v1/chat/scenarios/{name}/access, admin on "*"). `version` reloads it
// after a save; `stale` says the form holds unsaved collection changes.

function AccessBody({ data, error }) {
  if (error?.status === 403) return <p className="mt-2 text-sm text-fg-muted">The list of users needs admin on &quot;*&quot;.</p>;
  if (error) return <p role="alert" className="mt-2 rounded border border-danger-line bg-danger-soft p-2 text-sm text-danger-strong">{error.message}</p>;
  if (data === null) return null;
  if (!data.authEnabled) return <p className="mt-2 text-sm text-fg-muted">Authentication is off. Everyone who reaches this server can read every collection.</p>;
  const total = data.collections.length;
  return (
    <>
      {total === 0 && (
        <p className="mt-2 text-sm text-fg-muted">This scenario allows no collections, so every signed-in user sees it, and the assistant has nothing to search.</p>
      )}
      <p className="mt-2 text-xs text-fg-subtle">
        A scenario is offered only to an account that can read all of its collections. mddb-chat uses the account in{' '}
        <code>[mddb] auth_username</code>: it must be in this list with <em>All collections</em>.
      </p>
      <div className="mt-2 overflow-x-auto">
        <table className="w-full text-left text-sm">
          <caption className="sr-only">Who can read this scenario</caption>
          <thead className="text-xs text-fg-muted">
            <tr>
              <th scope="col" className="py-1 pr-3 font-medium">User</th>
              <th scope="col" className="py-1 pr-3 font-medium">Access</th>
              <th scope="col" className="py-1 font-medium">Through</th>
            </tr>
          </thead>
          <tbody>
            {sortReaders(data.users).map((u) => (
              <tr key={u.username} className="border-t border-line">
                <td className="py-1 pr-3 text-fg">{u.username}{u.disabled && <span className="text-fg-muted"> (disabled)</span>}</td>
                <td className="py-1 pr-3 text-fg-soft">{accessLabel(u, total)}</td>
                <td className="py-1 text-fg-soft">{throughLabel(u)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  );
}

export default function ChatScenarioAccess({ name, version, stale = false }) {
  const { config, openView } = useStore();
  const { features } = useFeatures();
  const [data, setData] = useState(null);
  const [error, setError] = useState(null);
  const links = accessLinks({ features, ...navConditions(config) });

  useEffect(() => {
    let current = true;
    mddbClient
      .getChatScenarioAccess(name)
      .then((d) => {
        if (!current) return;
        setData(d);
        setError(null);
      })
      .catch((err) => current && setError(err));
    return () => {
      current = false;
    };
  }, [name, version]);

  return (
    <section aria-labelledby="chat-access-heading" className="rounded-lg border border-line bg-surface p-4">
      <h3 id="chat-access-heading" className="flex items-center gap-2 text-sm font-semibold text-fg">
        <ShieldCheck className="h-4 w-4 text-accent" aria-hidden="true" /> Access
      </h3>
      {links.length > 0 && (
        <div className="mt-2 flex flex-wrap gap-2">
          {links.map((link) => (
            <button
              key={link.label}
              type="button"
              onClick={() => openView(link.mode, link.target)}
              className="rounded border border-line px-2 py-1 text-xs font-medium text-accent-strong hover:bg-surface-subtle focus:outline-none focus:ring-2 focus:ring-focus"
            >
              {link.label}
            </button>
          ))}
        </div>
      )}
      <p className="mt-2 text-xs text-fg-subtle">
        An MCP API key bound to this scenario (MCP keys, below) reads only its collections, read-only.
      </p>
      {stale && <p className="mt-2 text-xs text-warning-strong">Saved state. Save the scenario to refresh.</p>}
      <AccessBody data={data} error={error} />
    </section>
  );
}
