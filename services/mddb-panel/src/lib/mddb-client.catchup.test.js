// mddb-client: the 2.18.2–2.19.0 server features the panel reaches since
// 2.19.0 — aliases, evaluation, storage, indexing status, async vector
// reindex jobs, code symbols, image documents and a collection's sources.
import { describe, test, expect, vi, beforeEach } from 'vitest';
import { stubFetch, lastRequest, textResponse, expectEndpoint } from '../../test/fetch-mock.js';

vi.mock('./auth', () => ({ authManager: { getToken: vi.fn() } }));

const { authManager } = await import('./auth');
const { default: client } = await import('./mddb-client');

const GET = 'GET';
const POST = 'POST';
const PUT = 'PUT';
const DELETE = 'DELETE';

beforeEach(() => {
  vi.mocked(authManager.getToken).mockReturnValue(null);
  vi.spyOn(console, 'error').mockImplementation(() => {});
});

describe('endpoints', () => {
  const evalQuery = { collection: 'docs', id: 'q1', query: 'backup', expectedKeys: ['BACKUP.md'] };
  test.each([
    ['listAutomation by collection', (c) => c.listAutomation(undefined, { collection: 'a b' }), '/v1/automation?collection=a+b', GET],
    ['listAutomation by type and collection', (c) => c.listAutomation('cron', { collection: 'docs' }), '/v1/automation?type=cron&collection=docs', GET],
    ['listAliases', (c) => c.listAliases(), '/v1/aliases', GET],
    ['setAlias', (c) => c.setAlias('docs/x', 'docs-v2'), '/v1/aliases/docs%2Fx', PUT, { collection: 'docs-v2' }],
    ['deleteAlias', (c) => c.deleteAlias('docs'), '/v1/aliases/docs', DELETE],
    ['listEvalQueries', (c) => c.listEvalQueries('docs'), '/v1/eval-queries?collection=docs', GET],
    ['saveEvalQuery', (c) => c.saveEvalQuery(evalQuery), '/v1/eval-queries', POST, evalQuery],
    ['deleteEvalQuery', (c) => c.deleteEvalQuery('docs', 'q&1'), '/v1/eval-queries?collection=docs&id=q%261', DELETE],
    ['runEval defaults', (c) => c.runEval({ collection: 'docs' }), '/v1/eval', POST, { collection: 'docs' }],
    ['runEval every option', (c) => c.runEval({ collection: 'docs', k: 5, configs: [{ searchType: 'fts' }], details: true }), '/v1/eval', POST,
      { collection: 'docs', k: 5, configs: [{ searchType: 'fts' }], details: true }],
    ['runEval empty configs', (c) => c.runEval({ collection: 'docs', configs: [] }), '/v1/eval', POST, { collection: 'docs' }],
    ['getStorage all', (c) => c.getStorage(), '/v1/storage', GET],
    ['getStorage one', (c) => c.getStorage({ collection: 'src' }), '/v1/storage?collection=src', GET],
    ['getIndexingStatus all', (c) => c.getIndexingStatus(), '/v1/indexing-status', GET],
    ['getIndexingStatus one', (c) => c.getIndexingStatus('notes'), '/v1/indexing-status?collection=notes', GET],
    ['startVectorReindexJob', (c) => c.startVectorReindexJob({ collection: 'docs' }), '/v1/vector-reindex', POST, { collection: 'docs', force: false, async: true }],
    ['startVectorReindexJob force', (c) => c.startVectorReindexJob({ collection: 'docs', force: true }), '/v1/vector-reindex', POST, { collection: 'docs', force: true, async: true }],
    ['listVectorReindexJobs', (c) => c.listVectorReindexJobs(), '/v1/vector-reindex/jobs', GET],
    ['getVectorReindexJob', (c) => c.getVectorReindexJob('reindex_1'), '/v1/vector-reindex/jobs/reindex_1', GET],
    ['codeSymbol minimal', (c) => c.codeSymbol({ collection: 'repo', name: 'Run' }), '/v1/symbol?collection=repo&name=Run', GET],
    ['codeSymbol every option', (c) => c.codeSymbol({ collection: 'repo', name: 'Run', include: ['definitions', 'tests'], limit: 5, includeContent: true }),
      '/v1/symbol?collection=repo&name=Run&include=definitions%2Ctests&limit=5&includeContent=true', GET],
    ['codeSymbol with a path', (c) => c.codeSymbol({ collection: 'repo', name: 'Conn.Close', path: 'net/conn.go', include: ['implementations'] }),
      '/v1/symbol?collection=repo&name=Conn.Close&path=net%2Fconn.go&include=implementations', GET],
    ['codeLanguages', (c) => c.codeLanguages(), '/v1/code-languages', GET],
  ])('%s', (_label, ...row) => expectEndpoint(client, ...row));
});

describe('passing a signal', () => {
  test.each([
    ['runEval', (c, signal) => c.runEval({ collection: 'docs', signal })],
    ['getStorage', (c, signal) => c.getStorage({ signal })],
    ['codeSymbol', (c, signal) => c.codeSymbol({ collection: 'r', name: 'n', signal })],
  ])('%s forwards it to fetch', async (_label, call) => {
    const fetchMock = stubFetch();
    const { signal } = new AbortController();
    await call(client, signal);
    expect(lastRequest(fetchMock).signal).toBe(signal);
  });
});

describe('getImage()', () => {
  test('returns the image bytes with the language', async () => {
    const fetchMock = stubFetch(textResponse('PNG', { headers: { 'Content-Type': 'image/png' } }));
    const { blob } = await client.getImage({ collection: 'docs', key: 'a/images/b.png', lang: 'en' });
    expect(await blob.text()).toBe('PNG');
    expect(lastRequest(fetchMock).url).toBe('/v1/image?collection=docs&key=a%2Fimages%2Fb.png&lang=en');
  });

  test('omits an empty language and reports a 404', async () => {
    const fetchMock = stubFetch(textResponse('not an image', { status: 404 }));
    await expect(client.getImage({ collection: 'docs', key: 'k' })).rejects.toThrow('API Error (404): not an image');
    expect(lastRequest(fetchMock).url).toBe('/v1/image?collection=docs&key=k');
  });
});
