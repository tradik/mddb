// Shared helpers for the mddb-client suites: a stubbed global `fetch` and a
// readable view of the request the client sent.
import { vi, expect } from 'vitest';

/** A real fetch Response carrying `body` as JSON. */
export function jsonResponse(body, init = {}) {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
    ...init,
  });
}

/** A Response with a plain-text body (error pages, YAML). */
export function textResponse(text, init = {}) {
  return new Response(text, { status: 200, ...init });
}

/**
 * Replaces the global fetch. Each queued response is returned once, in order;
 * after that every call gets `{ ok: true }`.
 */
export function stubFetch(...responses) {
  const fetchMock = vi.fn(async () => jsonResponse({ ok: true }));
  for (const r of responses) {
    if (r instanceof Error) fetchMock.mockRejectedValueOnce(r);
    else fetchMock.mockResolvedValueOnce(r);
  }
  vi.stubGlobal('fetch', fetchMock);
  return fetchMock;
}

/** The last request sent through `fetchMock`, with a JSON body parsed. */
export function lastRequest(fetchMock) {
  const [url, init = {}] = fetchMock.mock.calls.at(-1);
  const body = typeof init.body === 'string' ? JSON.parse(init.body) : init.body;
  return { url, method: init.method ?? 'GET', headers: init.headers ?? {}, body, signal: init.signal };
}

/**
 * One row of a table-driven check of client methods. A row is
 *   [label, (client) => client.method(...), url, method, body?]
 * and this asserts the URL (with query string), HTTP method and JSON body
 * sent, and that the parsed response comes back. `body` undefined means none
 * sent. The suites call it from their own `test.each`, so each file declares
 * its tests where a reader (and SonarCloud) looks for them.
 */
export async function expectEndpoint(client, call, url, method, body) {
  const fetchMock = stubFetch(jsonResponse({ result: 'ok' }));
  await expect(call(client)).resolves.toEqual({ result: 'ok' });
  expect(fetchMock).toHaveBeenCalledTimes(1);
  const req = lastRequest(fetchMock);
  expect(req.url).toBe(url);
  expect(req.method).toBe(method);
  expect(req.body).toEqual(body);
}
