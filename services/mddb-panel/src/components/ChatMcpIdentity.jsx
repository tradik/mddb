import { inputClass, labelClass } from '../lib/chat-scenario';
import { identityHeaderDefault, identityTtlDefault, identityTtlMax, identityTtlMin } from '../lib/chat-mcp-identity';

// #414: "Forward the user's identity" on one external MCP server. When on,
// mddbd sends the server a short-lived token naming the signed-in chat user,
// signed with a key secret of its own, next to the server's credential —
// and only signed-in chat sessions are offered the server's tools.

export default function ChatMcpIdentity({ id, row, set, secretNames }) {
  const keyNames = [...secretNames];
  if (row.identitySecretRef && !keyNames.includes(row.identitySecretRef)) keyNames.push(row.identitySecretRef);
  return (
    <div className="space-y-2">
      <label className="flex items-center gap-2 text-sm text-gray-800">
        <input
          type="checkbox"
          role="switch"
          checked={row.forwardIdentity}
          onChange={(e) => set('forwardIdentity', e.target.checked)}
          aria-describedby={`${id}-identity-help`}
          className="h-4 w-4 rounded border-gray-400 text-primary-700 focus:ring-primary-500"
        />
        Forward the user&apos;s identity
      </label>
      <p id={`${id}-identity-help`} className="text-xs text-gray-700">
        For a server that answers about the user&apos;s own account. Each call carries a token for the signed-in chat user,
        valid for at most {identityTtlMax} seconds; anonymous visitors are not offered this server&apos;s tools.
      </p>
      {row.forwardIdentity && (
        <div className="grid gap-2 md:grid-cols-3">
          <div>
            <label htmlFor={`${id}-identity-key`} className={labelClass}>Identity key secret</label>
            <select id={`${id}-identity-key`} value={row.identitySecretRef} onChange={(e) => set('identitySecretRef', e.target.value)} className={inputClass}>
              <option value="">Choose a secret</option>
              {keyNames.map((name) => <option key={name} value={name} disabled={name === row.secretRef}>{name}</option>)}
            </select>
            <p className="mt-1 text-xs text-gray-700">At least 32 bytes, never the credential.</p>
          </div>
          <div>
            <label htmlFor={`${id}-identity-header`} className={labelClass}>Identity header</label>
            <input id={`${id}-identity-header`} value={row.identityHeader} onChange={(e) => set('identityHeader', e.target.value)}
              placeholder={identityHeaderDefault} className={inputClass} />
          </div>
          <div>
            <label htmlFor={`${id}-identity-ttl`} className={labelClass}>Token lifetime, s ({identityTtlMin}–{identityTtlMax})</label>
            <input id={`${id}-identity-ttl`} inputMode="numeric" value={row.identityTtl} onChange={(e) => set('identityTtl', e.target.value)}
              placeholder={String(identityTtlDefault)} className={inputClass} />
          </div>
        </div>
      )}
    </div>
  );
}
