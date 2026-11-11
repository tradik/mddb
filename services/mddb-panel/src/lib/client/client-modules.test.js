// The 2.19.0 client modules, through the real client: every method sends the
// URL, method and JSON body docs/API.md describes, and the file-shaped
// answers (export, wiki import) take their own paths.
import { describe, test, expect, vi } from 'vitest';
import { expectEndpoint, stubFetch, lastRequest, jsonResponse } from '../../../test/fetch-mock.js';

vi.mock('../auth', () => ({ authManager: { getToken: () => 'tok' } }));

const { default: client } = await import('../mddb-client');

const POST = 'POST';
const GET = 'GET';
const DELETE = 'DELETE';

describe('memory', () => {
  test.each([
    ['createMemorySession', (c) => c.createMemorySession({ userId: 'u', title: 't' }), '/v1/memory/session', POST, { userId: 'u', title: 't' }],
    ['listMemorySessions defaults', (c) => c.listMemorySessions(), '/v1/memory/sessions', POST, { limit: 50, offset: 0, sort: 'updatedAt', asc: false }],
    ['listMemorySessions filtered', (c) => c.listMemorySessions({ userId: 'u', scenario: 's', limit: 5, offset: 10, sort: 'createdAt', asc: true }), '/v1/memory/sessions', POST, { userId: 'u', scenario: 's', limit: 5, offset: 10, sort: 'createdAt', asc: true }],
    ['getMemoryHistory', (c) => c.getMemoryHistory({ sessionId: 's' }), '/v1/memory/history', POST, { sessionId: 's', limit: 100, offset: 0 }],
    ['addMemoryMessage', (c) => c.addMemoryMessage({ sessionId: 's', role: 'user', content: 'hi' }), '/v1/memory/message', POST, { sessionId: 's', role: 'user', content: 'hi' }],
    ['recallMemory defaults', (c) => c.recallMemory({ query: 'q' }), '/v1/memory/recall', POST, { query: 'q', topK: 10, strategy: 'hybrid', includeContent: true }],
    ['recallMemory scoped', (c) => c.recallMemory({ query: 'q', userId: 'u', sessionId: 's', topK: 3, strategy: 'keyword', includeContent: false }), '/v1/memory/recall', POST, { query: 'q', userId: 'u', sessionId: 's', topK: 3, strategy: 'keyword', includeContent: false }],
    ['summarizeMemorySession', (c) => c.summarizeMemorySession({ sessionId: 's' }), '/v1/memory/summarize', POST, { sessionId: 's' }],
  ])('%s', (_label, call, path, method, body) => expectEndpoint(client, call, path, method, body));
});

describe('schemas, keys, facets, duplicates, batches, maintenance', () => {
  test.each([
    ['listSchemas', (c) => c.listSchemas(), '/v1/schema/list', POST, {}],
    ['getSchema', (c) => c.getSchema('blog'), '/v1/schema/get', POST, { collection: 'blog' }],
    ['setSchema sends the text', (c) => c.setSchema('blog', '{"required":[]}'), '/v1/schema/set', POST, { collection: 'blog', schema: '{"required":[]}' }],
    ['deleteSchema', (c) => c.deleteSchema('blog'), '/v1/schema/delete', POST, { collection: 'blog' }],
    ['validateMeta', (c) => c.validateMeta('blog', { a: ['b'] }), '/v1/validate', POST, { collection: 'blog', meta: { a: ['b'] } }],
    ['startBulkIngestJob', (c) => c.startBulkIngestJob({ collection: 'c', documents: [{ key: 'k' }] }), '/v1/bulk-ingest-job', POST, { collection: 'c', documents: [{ key: 'k' }] }],
    ['getBulkIngestJob encodes the id', (c) => c.getBulkIngestJob('a/b'), '/v1/bulk-ingest-job/a%2Fb', GET],
    ['listBulkIngestJobs all', (c) => c.listBulkIngestJobs(), '/v1/bulk-ingest-jobs', GET],
    ['listBulkIngestJobs of one collection', (c) => c.listBulkIngestJobs({ collection: 'c d' }), '/v1/bulk-ingest-jobs?collection=c+d', GET],
    ['cancelBulkIngestJob', (c) => c.cancelBulkIngestJob('j1'), '/v1/bulk-ingest-job/j1', DELETE],
    ['getMe', (c) => c.getMe(), '/v1/auth/me', GET],
    ['listApiKeys', (c) => c.listApiKeys(), '/v1/auth/api-keys', GET],
    ['createApiKey never expires by default', (c) => c.createApiKey({ description: 'ci' }), '/v1/auth/api-key', POST, { description: 'ci', expiresAt: 0 }],
    ['createApiKey with expiry', (c) => c.createApiKey({ description: 'ci', expiresAt: 99 }), '/v1/auth/api-key', POST, { description: 'ci', expiresAt: 99 }],
    ['deleteApiKey', (c) => c.deleteApiKey('ab12'), '/v1/auth/api-keys/ab12', DELETE],
    ['aggregate', (c) => c.aggregate({ collection: 'c', fields: ['a', 'b'] }), '/v1/aggregate', POST, { collection: 'c', facets: [{ field: 'a', orderBy: 'count' }, { field: 'b', orderBy: 'count' }], maxFacetSize: 50 }],
    ['aggregate with an empty filter leaves it out', (c) => c.aggregate({ collection: 'c', fields: ['a'], filterMeta: {} }), '/v1/aggregate', POST, { collection: 'c', facets: [{ field: 'a', orderBy: 'count' }], maxFacetSize: 50 }],
    ['aggregate filtered and by value', (c) => c.aggregate({ collection: 'c', fields: ['a'], filterMeta: { s: ['x'] }, orderBy: 'value', maxFacetSize: 5 }), '/v1/aggregate', POST, { collection: 'c', facets: [{ field: 'a', orderBy: 'value' }], maxFacetSize: 5, filterMeta: { s: ['x'] } }],
    ['findDuplicates defaults', (c) => c.findDuplicates({ collection: 'c' }), '/v1/find-duplicates', POST, { collection: 'c', mode: 'both', threshold: 0.9, maxDocs: 5000, distanceMetric: 'cosine' }],
    ['addBatch', (c) => c.addBatch({ collection: 'c', documents: [{ key: 'k', lang: 'en', contentMd: '#' }] }), '/v1/add-batch', POST, { collection: 'c', documents: [{ key: 'k', lang: 'en', contentMd: '#' }] }],
    ['deleteBatch sends only key and lang', (c) => c.deleteBatch({ collection: 'c', documents: [{ key: 'k', lang: 'en', meta: {} }] }), '/v1/delete-batch', POST, { collection: 'c', documents: [{ key: 'k', lang: 'en' }] }],
    ['checksum', (c) => c.checksum('my docs'), '/v1/checksum?collection=my+docs', GET],
    ['restore', (c) => c.restore('backup-1.db'), '/v1/restore', POST, { from: 'backup-1.db' }],
  ])('%s', (_label, call, path, method, body) => expectEndpoint(client, call, path, method, body));
});

describe('file-shaped answers', () => {
  test('export answers with the file, not parsed JSON', async () => {
    const fetchMock = stubFetch(new Response('{"key":"a"}\n'));
    const { blob, filename } = await client.export({ collection: 'c', format: 'zip' });
    expect(await blob.text()).toBe('{"key":"a"}\n');
    expect(filename).toBeNull();
    const req = lastRequest(fetchMock);
    expect(req.url).toBe('/v1/export');
    expect(req.method).toBe(POST);
    expect(req.body).toEqual({ collection: 'c', filterMeta: {}, format: 'zip' });
    expect(req.headers).toEqual({ 'Content-Type': 'application/json', Authorization: 'Bearer tok' });
  });

  test('export takes the file name the server gives', async () => {
    stubFetch(new Response('PK', { headers: { 'Content-Disposition': 'attachment; filename="my-docs.zip"' } }));
    const { filename } = await client.export({ collection: 'my docs', format: 'zip' });
    expect(filename).toBe('my-docs.zip');
  });

  test('importWiki posts the dump and only the options that are set', async () => {
    const fetchMock = stubFetch(jsonResponse({ imported: 1 }));
    const file = new File(['<mediawiki/>'], 'dump.xml');
    await expect(client.importWiki({ file, collection: 'wiki', lang: 'en' })).resolves.toEqual({ imported: 1 });
    let form = lastRequest(fetchMock).body;
    expect([...form.keys()]).toEqual(['file', 'collection', 'lang']);

    await client.importWiki({ file, collection: 'wiki', lang: 'pl', namespaces: '0,14', skipRedirects: true, skipFts: true, maxPages: 10 });
    form = lastRequest(fetchMock).body;
    expect(Object.fromEntries([...form.entries()].filter(([k]) => k !== 'file'))).toEqual({
      collection: 'wiki', lang: 'pl', namespaces: '0,14', skipRedirects: 'true', skipFts: 'true', maxPages: '10',
    });
    expect(lastRequest(fetchMock).url).toBe('/v1/import-wiki');
  });

  test('a refused wiki import keeps the server wording', async () => {
    stubFetch(jsonResponse({ error: 'missing collection' }, { status: 400 }));
    await expect(client.importWiki({ file: new File([''], 'x.xml'), collection: '', lang: 'en' }))
      .rejects.toThrow('Import Error (400): missing collection');
  });
});
