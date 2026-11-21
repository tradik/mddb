/**
 * Addresses on LLM Connections, where every example uses the Server Domain.
 *
 * urlOnHost: a listen address (`:11023`, `0.0.0.0:11023`, `127.0.0.1:11023`)
 * as a URL on that domain. Only the port is kept: prefixing the whole listen
 * address used to give `http://localhost127.0.0.1:11023`.
 *
 * withServerDomain: the MCP config the server generates (`GET /v1/mcp/config`) names the
 * addresses an MCP client dials: `grpcAddress: "host:port"` and
 * `restBaseUrl: "http://host:port"`. LLM Connections shows every example with
 * the Server Domain field, so this puts that host in front of each port.
 *
 * Only the scheme and the port are kept from the server's value, which also
 * repairs what servers before 2.19.6 sent for a bound listen address
 * (`"localhost127.0.0.1:18924"`).
 *
 * The Server Domain is a host name (`myserver.com`) or, since 2.19.9, a full
 * URL (`https://myserver.com`, `https://myserver.com:8443`) for an instance
 * behind a TLS proxy or tunnel: then its scheme, host and port are used as
 * they are, for the HTTP and the MCP examples alike (#440).
 *
 * mcpEndpoint: the MCP URL every example names — the server's public URL
 * (`MDDB_MCP_PUBLIC_URL`) when it reports one, else the MCP port on the
 * Server Domain — followed by `/mcp`.
 */

const ADDRESS_KEYS = ['grpcAddress:', 'restBaseUrl:'];

/** rehost returns a `key: "…"` line with its address on `host`, other lines as they are. */
function rehost(line, host) {
  const trimmed = line.trimStart();
  if (!ADDRESS_KEYS.some((key) => trimmed.startsWith(key))) return line;
  const open = line.indexOf('"');
  const close = line.lastIndexOf('"');
  if (open < 0 || close <= open) return line;
  const value = line.slice(open + 1, close);
  const port = value.slice(value.lastIndexOf(':') + 1);
  if (!/^\d+$/.test(port)) return line;
  const schemeEnd = value.indexOf('://');
  const scheme = schemeEnd < 0 ? '' : value.slice(0, schemeEnd + 3);
  return `${line.slice(0, open + 1)}${scheme}${host}:${port}${line.slice(close)}`;
}

/** domainUrl is a Server Domain typed as an http(s) URL, parsed; else null. */
function domainUrl(domain) {
  if (!/^https?:\/\//i.test(domain)) return null;
  try {
    return new URL(domain);
  } catch {
    return null;
  }
}

/**
 * withServerDomain returns `yaml` with both addresses on `host` (the host
 * name of a URL); no host, no change.
 */
export function withServerDomain(yaml, host) {
  if (!host) return yaml;
  const name = domainUrl(host)?.hostname ?? host;
  return yaml
    .split('\n')
    .map((line) => rehost(line, name))
    .join('\n');
}

/**
 * urlOnHost returns `http://host:port` for the port of the listen address
 * `addr`, or the origin of `host` when it is a URL.
 */
export function urlOnHost(host, addr) {
  return domainUrl(host)?.origin ?? `http://${host}:${addr.slice(addr.lastIndexOf(':') + 1)}`;
}

/** mcpBaseUrl is the URL MCP clients dial, without `/mcp`. */
export function mcpBaseUrl({ publicUrl, domain, mcpAddr }) {
  return publicUrl || urlOnHost(domain, mcpAddr);
}

/** mcpEndpoint is the full MCP endpoint: mcpBaseUrl followed by `/mcp`. */
export function mcpEndpoint(address) {
  return `${mcpBaseUrl(address)}/mcp`;
}
