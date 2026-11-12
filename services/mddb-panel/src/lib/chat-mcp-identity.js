/**
 * Forwarding the signed-in user's identity to an external MCP server (#414):
 * the editor fields of a server's `forwardIdentity`. Off by default; when on,
 * mddbd signs a short-lived token for the chat user with a separate key
 * secret, and only signed-in chat sessions are offered the server's tools.
 *
 * Numbers are held as the strings the inputs produce; empty means mddbd's
 * default (header X-MDDB-Chat-Identity, 120 seconds).
 */

/** The header mddbd sends the token in when none is set. */
export const identityHeaderDefault = 'X-MDDB-Chat-Identity';
/** The token lifetime mddbd uses when none is set, and its bounds. */
export const identityTtlDefault = 120;
export const identityTtlMin = 10;
export const identityTtlMax = 300;

/** The identity fields of an editor row, from a stored server. */
export function identityRowFields(server = {}) {
  const f = server.forwardIdentity;
  return {
    forwardIdentity: Boolean(f),
    identitySecretRef: f?.secretRef || '',
    identityHeader: f?.header || '',
    identityTtl: f?.ttlSeconds ? String(f.ttlSeconds) : '',
  };
}

/** The `forwardIdentity` part of a server to PUT: nothing while it is off. */
export function identityFromRow(row) {
  if (!row.forwardIdentity) return {};
  return {
    forwardIdentity: {
      secretRef: row.identitySecretRef,
      header: row.identityHeader.trim(),
      ttlSeconds: Number.parseInt(row.identityTtl, 10) || 0,
    },
  };
}

/** Why a row's identity setting cannot be saved, or null when it can. */
export function identityProblem(row, name) {
  if (!row.forwardIdentity) return null;
  if (!row.identitySecretRef) return `The MCP server "${name}" forwards the user's identity and needs a key secret.`;
  if (row.identitySecretRef === row.secretRef) return `The MCP server "${name}" needs an identity key other than its credential.`;
  const header = row.identityHeader.trim();
  if (header && !/^[A-Za-z0-9-]{1,64}$/.test(header)) return `The identity header of "${name}" may hold only letters, digits and "-".`;
  const ttl = row.identityTtl.trim();
  const n = Number(ttl);
  if (ttl && !(Number.isInteger(n) && n >= identityTtlMin && n <= identityTtlMax)) {
    return `The identity token lifetime of "${name}" must be ${identityTtlMin} to ${identityTtlMax} seconds.`;
  }
  return null;
}
