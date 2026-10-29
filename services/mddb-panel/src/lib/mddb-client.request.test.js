// mddb-client: the shared request() path and the methods that call fetch
// directly (search, uploadFile, getMCPConfigText) — headers, auth token,
// error mapping and network failures.
import { describe, test, expect, vi, beforeEach } from 'vitest';
import { stubFetch, lastRequest, jsonResponse, textResponse } from '../../test/fetch-mock.js';

vi.mock('./auth', () => ({ authManager: { getToken: vi.fn() } }));

const { authManager } = await import('./auth');
const { default: client } = await import('./mddb-client');

beforeEach(() => {
  vi.mocked(authManager.getToken).mockReturnValue(null);
  vi.spyOn(console, 'error').mockImplementation(() => {});
});

describe('request()', () => {
  test('prefixes /v1, sends JSON and returns the parsed body', async () => {
    const fetchMock = stubFetch(jsonResponse({ docs: 3 }));
    await expect(client.request('/stats', { method: 'GET' })).resolves.toEqual({ docs: 3 });
    const req = lastRequest(fetchMock);
    expect(req.url).toBe('/v1/stats');
    expect(req.method).toBe('GET');
    expect(req.headers).toEqual({ 'Content-Type': 'application/json' });
  });

  test('adds the bearer token when the user is signed in', async () => {
    vi.mocked(authManager.getToken).mockReturnValue('tok.en.jwt');
    const fetchMock = stubFetch();
    await client.getStats();
    expect(lastRequest(fetchMock).headers.Authorization).toBe('Bearer tok.en.jwt');
  });

  test('sends no Authorization header without a token', async () => {
    const fetchMock = stubFetch();
    await client.getStats();
    expect(lastRequest(fetchMock).headers).not.toHaveProperty('Authorization');
  });

  test('caller headers are merged, not swapped in, and never mutated', async () => {
    vi.mocked(authManager.getToken).mockReturnValue('t');
    const callerHeaders = { 'X-Trace': '1' };
    const fetchMock = stubFetch();
    await client.request('/x', { method: 'POST', headers: callerHeaders });
    expect(lastRequest(fetchMock).headers).toEqual({
      'Content-Type': 'application/json',
      'X-Trace': '1',
      Authorization: 'Bearer t',
    });
    expect(callerHeaders).toEqual({ 'X-Trace': '1' });
  });

  test('401 becomes an Unauthorized error carrying the server text', async () => {
    stubFetch(textResponse('token expired', { status: 401 }));
    await expect(client.getStats()).rejects.toThrow('Unauthorized: token expired');
  });

  test.each([
    [400, 'bad collection'],
    [404, 'not found'],
    [500, 'boom'],
  ])('HTTP %i becomes "API Error (%i): <body>"', async (status, text) => {
    stubFetch(textResponse(text, { status }));
    await expect(client.getHealth()).rejects.toThrow(`API Error (${status}): ${text}`);
    expect(console.error).toHaveBeenCalledWith('MDDB API Error:', expect.any(Error));
  });

  test('a network failure is logged and rethrown unchanged', async () => {
    const failure = new TypeError('Failed to fetch');
    stubFetch(failure);
    await expect(client.getConfig()).rejects.toBe(failure);
    expect(console.error).toHaveBeenCalledWith('MDDB API Error:', failure);
  });

  test('an aborted request is rethrown without logging', async () => {
    const abort = new DOMException('aborted', 'AbortError');
    stubFetch(abort);
    await expect(client.getConfig()).rejects.toBe(abort);
    expect(console.error).not.toHaveBeenCalled();
  });

  test('passes the abort signal through', async () => {
    const fetchMock = stubFetch();
    const { signal } = new AbortController();
    await client.vectorSearch({ collection: 'c', query: 'q', signal });
    expect(lastRequest(fetchMock).signal).toBe(signal);
  });
});

describe('search()', () => {
  test('posts the filter with defaults and reads X-Total-Count', async () => {
    const docs = [{ key: 'a' }];
    const fetchMock = stubFetch(jsonResponse(docs, { headers: { 'X-Total-Count': '42' } }));
    await expect(client.search({ collection: 'blog' })).resolves.toEqual({ documents: docs, totalCount: 42 });
    const req = lastRequest(fetchMock);
    expect(req.url).toBe('/v1/search');
    expect(req.method).toBe('POST');
    expect(req.headers).toEqual({ 'Content-Type': 'application/json' });
    expect(req.body).toEqual({ collection: 'blog', filterMeta: {}, sort: 'addedAt', asc: false, limit: 100, offset: 0 });
  });

  test('sends the token, a missing count reads as 0 and a non-array as empty', async () => {
    vi.mocked(authManager.getToken).mockReturnValue('t');
    const fetchMock = stubFetch(jsonResponse(null));
    const out = await client.search({ collection: 'c', filterMeta: { a: ['1'] }, sort: 'key', asc: true, limit: 5, offset: 10 });
    expect(out).toEqual({ documents: [], totalCount: 0 });
    const req = lastRequest(fetchMock);
    expect(req.headers.Authorization).toBe('Bearer t');
    expect(req.body).toEqual({ collection: 'c', filterMeta: { a: ['1'] }, sort: 'key', asc: true, limit: 5, offset: 10 });
  });

  test('non-2xx is an API Error', async () => {
    stubFetch(textResponse('nope', { status: 503 }));
    await expect(client.search({ collection: 'c' })).rejects.toThrow('API Error (503): nope');
  });
});

describe('uploadFile()', () => {
  const file = (name) => new File(['# hi'], name, { type: 'text/markdown' });

  test('one file goes as "file" with no optional fields and no token', async () => {
    const fetchMock = stubFetch(jsonResponse({ added: 1 }));
    await expect(client.uploadFile({ files: file('a.md') })).resolves.toEqual({ added: 1 });
    const req = lastRequest(fetchMock);
    expect(req.url).toBe('/v1/upload');
    expect(req.method).toBe('POST');
    expect(req.headers).toEqual({});
    expect([...req.body.keys()]).toEqual(['file']);
  });

  test('several files go as "files[]" with collection, lang, key and the token', async () => {
    vi.mocked(authManager.getToken).mockReturnValue('t');
    const fetchMock = stubFetch();
    const { signal } = new AbortController();
    await client.uploadFile({ files: [file('a.md'), file('b.md')], collection: 'docs', lang: 'pl', key: 'k', signal });
    const req = lastRequest(fetchMock);
    expect(req.headers).toEqual({ Authorization: 'Bearer t' });
    expect(req.signal).toBe(signal);
    expect(req.body.getAll('files[]').map((f) => f.name)).toEqual(['a.md', 'b.md']);
    expect(req.body.get('collection')).toBe('docs');
    expect(req.body.get('lang')).toBe('pl');
    expect(req.body.get('key')).toBe('k');
  });

  test.each([
    [401, 'login', 'Unauthorized: login'],
    [413, 'too big', 'Upload Error (413): too big'],
  ])('HTTP %i is reported', async (status, text, message) => {
    stubFetch(textResponse(text, { status }));
    await expect(client.uploadFile({ files: file('a.md') })).rejects.toThrow(message);
  });
});

describe('getMCPConfigText()', () => {
  test('returns the YAML text and sends the token', async () => {
    vi.mocked(authManager.getToken).mockReturnValue('t');
    const fetchMock = stubFetch(textResponse('server: mddb\n'));
    await expect(client.getMCPConfigText()).resolves.toBe('server: mddb\n');
    const req = lastRequest(fetchMock);
    expect(req.url).toBe('/v1/mcp/config');
    expect(req.headers).toEqual({ Authorization: 'Bearer t' });
  });

  test('sends no headers without a token and fails on non-2xx', async () => {
    const fetchMock = stubFetch(textResponse('x', { status: 500 }));
    await expect(client.getMCPConfigText()).rejects.toThrow('Failed to load MCP config');
    expect(lastRequest(fetchMock).headers).toEqual({});
  });
});
