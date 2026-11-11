/**
 * The request shapes the client modules in this folder share, sent through
 * the MDDB client's authenticated `request()` (so errors become the client's
 * "API Error (<status>): <server message>" with `status` and `body`).
 */

/** POSTs `body` as JSON to `path`. */
export function postJson(client, path, body) {
  return client.request(path, { method: 'POST', body: JSON.stringify(body) });
}

/** GETs `path` with `params` as its query string (empty values left out). */
export function getWithQuery(client, path, params = {}) {
  const query = new URLSearchParams();
  for (const [name, value] of Object.entries(params)) {
    if (value !== undefined && value !== null && value !== '') query.set(name, String(value));
  }
  const qs = query.toString();
  return client.request(qs ? `${path}?${qs}` : path, { method: 'GET' });
}
