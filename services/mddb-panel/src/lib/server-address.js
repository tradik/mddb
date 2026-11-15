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

/** withServerDomain returns `yaml` with both addresses on `host`; no host, no change. */
export function withServerDomain(yaml, host) {
  if (!host) return yaml;
  return yaml
    .split('\n')
    .map((line) => rehost(line, host))
    .join('\n');
}

/** urlOnHost returns `http://host:port` for the port of the listen address `addr`. */
export function urlOnHost(host, addr) {
  return `http://${host}:${addr.slice(addr.lastIndexOf(':') + 1)}`;
}
