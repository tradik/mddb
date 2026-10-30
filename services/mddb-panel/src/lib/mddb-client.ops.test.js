// mddb-client: what 2.18.9/2.18.10 added — error mapping for the permission
// gate (SEC-019/020) and read-only collections (AGT-005), the result-cap
// report (#368), search planning options (SRCH-013/015/020), corpus packs and
// the diagnose bundle (OPS-025).
import { describe, test, expect, vi, beforeEach } from 'vitest';
import { stubFetch, lastRequest, jsonResponse, textResponse, expectEndpoint } from '../../test/fetch-mock.js';

vi.mock('./auth', () => ({ authManager: { getToken: vi.fn() } }));

const { authManager } = await import('./auth');
const { default: client, apiError, planningOptions, dispositionFilename, FORBIDDEN_HINT } = await import('./mddb-client');
const { getLimitNotice, reportLimit } = await import('./limit-notice');

beforeEach(() => {
  vi.mocked(authManager.getToken).mockReturnValue(null);
  vi.spyOn(console, 'error').mockImplementation(() => {});
  reportLimit(null);
});

describe('apiError()', () => {
  test('a JSON body names its error, code and status', async () => {
    const body = { error: 'collection "kb" is read-only', code: 'READ_ONLY', status: 403 };
    const err = await apiError(jsonResponse(body, { status: 403 }));
    expect(err.message).toBe('API Error (403): collection "kb" is read-only');
    expect(err.status).toBe(403);
    expect(err.code).toBe('READ_ONLY');
    expect(err.body).toEqual(body);
  });

  test('a plain 403 explains the permission rules', async () => {
    const err = await apiError(jsonResponse({ error: 'forbidden' }, { status: 403 }));
    expect(err.message).toBe(`API Error (403): forbidden — ${FORBIDDEN_HINT}`);
    expect(err.code).toBeUndefined();
  });

  test('plain text stays as it is, with the caller prefix', async () => {
    const err = await apiError(textResponse('too big', { status: 413 }), 'Upload Error');
    expect(err.message).toBe('Upload Error (413): too big');
    expect(err.body).toBeNull();
  });

  test('a JSON body without a string error keeps the raw text', async () => {
    const err = await apiError(jsonResponse({ error: { nested: true } }, { status: 500 }));
    expect(err.message).toBe('API Error (500): {"error":{"nested":true}}');
  });

  test('401 is Unauthorized', async () => {
    const err = await apiError(textResponse('expired', { status: 401 }));
    expect(err.message).toBe('Unauthorized: expired');
  });

  test('request() rejects with it, keeping the training flag of a 503', async () => {
    stubFetch(jsonResponse({ error: 'the ivf index is still training', training: true }, { status: 503 }));
    const err = await client.vectorSearch({ collection: 'c', query: 'q' }).catch((e) => e);
    expect(err.status).toBe(503);
    expect(err.body.training).toBe(true);
  });
});

describe('planningOptions()', () => {
  test('nothing set sends nothing', () => {
    expect(planningOptions()).toEqual({});
    expect(planningOptions({ explain: false, queryDecomposition: 'off', positive: [], negative: [] })).toEqual({});
  });

  test('explain, decomposition and feedback with the default mode', () => {
    expect(planningOptions({ explain: true, queryDecomposition: 'auto', positive: ['a'] })).toEqual({
      explain: true, queryDecomposition: 'auto', positive: ['a'], feedbackMode: 'query',
    });
  });

  test('negatives alone and an explicit mode', () => {
    expect(planningOptions({ negative: ['b'], feedbackMode: 'rescore' })).toEqual({ negative: ['b'], feedbackMode: 'rescore' });
  });
});

describe('dispositionFilename()', () => {
  test.each([
    ['attachment; filename="mddb-diagnose-1.tar.gz"', 'mddb-diagnose-1.tar.gz'],
    ['attachment; filename=kb-1.0.mddbpack', 'kb-1.0.mddbpack'],
    ['inline', null],
    [null, null],
  ])('%s → %s', (header, name) => {
    expect(dispositionFilename(header)).toBe(name);
  });
});

describe('result cap report', () => {
  test('a capped search is reported with its endpoint and limit', async () => {
    stubFetch(jsonResponse({ results: [], limitCapped: true, limit: 1000 }));
    await client.ftsSearch({ collection: 'c', query: 'q', limit: 5000 });
    expect(getLimitNotice()).toEqual({ endpoint: '/fts', limit: 1000 });
  });

  test('a capped body without a limit reports null', async () => {
    stubFetch(jsonResponse({ limitCapped: true }));
    await client.crossSearch({ query: 'q' });
    expect(getLimitNotice()).toEqual({ endpoint: '/cross-search', limit: null });
  });

  test('the next uncapped search clears it; other endpoints leave it', async () => {
    reportLimit({ endpoint: '/fts', limit: 10 });
    stubFetch(jsonResponse({ ok: 1 }));
    await client.getStats();
    expect(getLimitNotice()).toEqual({ endpoint: '/fts', limit: 10 });
    stubFetch(jsonResponse({ results: [] }));
    await client.hybridSearch({ collection: 'c', query: 'q' });
    expect(getLimitNotice()).toBeNull();
  });

  test('/v1/search reports a cap from its headers', async () => {
    stubFetch(jsonResponse([], { headers: { 'X-Limit-Capped': 'true', 'X-Limit': '1000' } }));
    await client.search({ collection: 'c', limit: 5000 });
    expect(getLimitNotice()).toEqual({ endpoint: '/search', limit: 1000 });
    stubFetch(jsonResponse([]));
    await client.search({ collection: 'c' });
    expect(getLimitNotice()).toBeNull();
  });
});

describe('search planning options reach the body', () => {
  test.each([
    ['vectorSearch', (c) => c.vectorSearch({ collection: 'c', query: 'q', algorithm: 'auto', explain: true, queryDecomposition: 'auto', positive: ['k1'], negative: ['k2'] }), '/v1/vector-search', 'POST',
      { collection: 'c', query: 'q', topK: 5, threshold: 0, filterMeta: {}, includeContent: false, algorithm: 'auto', distanceMetric: 'cosine', explain: true, queryDecomposition: 'auto', positive: ['k1'], negative: ['k2'], feedbackMode: 'query' }],
    ['hybridSearch', (c) => c.hybridSearch({ collection: 'c', query: 'q', vectorAlgorithm: 'auto', explain: true, queryDecomposition: 'auto' }), '/v1/hybrid-search', 'POST',
      { collection: 'c', query: 'q', topK: 10, algorithm: 'bm25', vectorAlgorithm: 'auto', alpha: 0.5, strategy: 'alpha', rrfK: 60, fuzzy: 0, threshold: 0, filterMeta: {}, includeContent: false, distanceMetric: 'cosine', explain: true, queryDecomposition: 'auto' }],
  ])('%s', (_label, ...row) => expectEndpoint(client, ...row));
});

describe('corpus packs and diagnose', () => {
  test.each([
    ['listCorpus', (c) => c.listCorpus(), '/v1/corpus', 'GET'],
    ['removeCorpus', (c) => c.removeCorpus('kb'), '/v1/corpus/remove', 'POST', { name: 'kb' }],
  ])('%s', (_label, ...row) => expectEndpoint(client, ...row));

  test('installCorpus uploads the file as "pack" with the token', async () => {
    vi.mocked(authManager.getToken).mockReturnValue('t');
    const fetchMock = stubFetch(jsonResponse({ name: 'kb', docCount: 3 }));
    const pack = new File(['x'], 'kb.mddbpack');
    await expect(client.installCorpus(pack)).resolves.toEqual({ name: 'kb', docCount: 3 });
    const req = lastRequest(fetchMock);
    expect(req.url).toBe('/v1/corpus/install');
    expect(req.method).toBe('POST');
    expect(req.headers).toEqual({ Authorization: 'Bearer t' });
    expect(req.body.get('pack').name).toBe('kb.mddbpack');
  });

  test('installCorpus reports a 409 conflict', async () => {
    stubFetch(jsonResponse({ error: 'collection "kb" holds data' }, { status: 409 }));
    await expect(client.installCorpus(new File(['x'], 'kb.mddbpack'))).rejects.toThrow('API Error (409): collection "kb" holds data');
  });

  test('buildCorpus posts the request and returns the file', async () => {
    const fetchMock = stubFetch(new Response('PACK', { headers: { 'Content-Disposition': 'attachment; filename="kb-1.mddbpack"' } }));
    const out = await client.buildCorpus({ collection: 'kb', license: 'MIT', version: '1', description: 'docs' });
    expect(out.filename).toBe('kb-1.mddbpack');
    expect(await out.blob.text()).toBe('PACK');
    const req = lastRequest(fetchMock);
    expect(req.url).toBe('/v1/corpus/build');
    expect(req.method).toBe('POST');
    expect(req.headers).toEqual({ 'Content-Type': 'application/json' });
    expect(req.body).toEqual({ collection: 'kb', license: 'MIT', version: '1', description: 'docs' });
  });

  test('buildCorpus leaves out an empty version and description', async () => {
    const fetchMock = stubFetch(new Response('PACK'));
    const out = await client.buildCorpus({ collection: 'kb', license: 'MIT' });
    expect(out.filename).toBeNull();
    expect(lastRequest(fetchMock).body).toEqual({ collection: 'kb', license: 'MIT' });
  });

  test.each([
    [{}, '/v1/admin/diagnose'],
    [undefined, '/v1/admin/diagnose'],
    [{ profiles: true, integrity: true, keepNames: true }, '/v1/admin/diagnose?profiles=true&integrity=true&keepNames=true'],
    [{ integrity: true }, '/v1/admin/diagnose?integrity=true'],
  ])('downloadDiagnose(%o) → %s', async (opts, url) => {
    vi.mocked(authManager.getToken).mockReturnValue('t');
    const fetchMock = stubFetch(new Response('tgz'));
    await client.downloadDiagnose(opts);
    const req = lastRequest(fetchMock);
    expect(req.url).toBe(url);
    expect(req.method).toBe('GET');
    expect(req.headers).toEqual({ Authorization: 'Bearer t' });
  });

  test('a refused download is an API error', async () => {
    stubFetch(jsonResponse({ error: 'admin access required' }, { status: 403 }));
    await expect(client.downloadDiagnose()).rejects.toMatchObject({ status: 403 });
  });
});
