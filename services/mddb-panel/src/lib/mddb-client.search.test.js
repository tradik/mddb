// mddb-client: documents, search (vector, geo, full-text, hybrid), synonyms,
// stop words and webhooks. Each row asserts the URL, query string, method and
// JSON body one call sends — defaults included, optional fields omitted.
import { describe, vi } from 'vitest';
import { expectEndpoints } from '../../test/fetch-mock.js';

vi.mock('./auth', () => ({ authManager: { getToken: () => null } }));

const { default: client } = await import('./mddb-client');

const POST = 'POST';
const GET = 'GET';
const DELETE = 'DELETE';

describe('documents', () => {
  expectEndpoints(client, [
    ['getStats', (c) => c.getStats(), '/v1/stats', GET],
    ['getReplicationStatus', (c) => c.getReplicationStatus(), '/v1/replication/status', GET],
    ['getDocument defaults env', (c) => c.getDocument({ collection: 'c', key: 'k', lang: 'en' }), '/v1/get', POST, { collection: 'c', key: 'k', lang: 'en', env: {} }],
    ['addDocument defaults meta', (c) => c.addDocument({ collection: 'c', key: 'k', lang: 'en', contentMd: '# x' }), '/v1/add', POST, { collection: 'c', key: 'k', lang: 'en', meta: {}, contentMd: '# x' }],
    ['export defaults', (c) => c.export({ collection: 'c' }), '/v1/export', POST, { collection: 'c', filterMeta: {}, format: 'ndjson' }],
    ['backup without a name', (c) => c.backup(), '/v1/backup', GET],
    ['backup encodes the name', (c) => c.backup('a b&x=1.db'), '/v1/backup?to=a%20b%26x%3D1.db', GET],
    ['truncate defaults', (c) => c.truncate({ collection: 'c' }), '/v1/truncate', POST, { collection: 'c', keepRevs: 3, dropCache: true }],
    ['deleteDocument', (c) => c.deleteDocument({ collection: 'c', key: 'k', lang: 'en' }), '/v1/delete', POST, { collection: 'c', key: 'k', lang: 'en' }],
    ['deleteCollection', (c) => c.deleteCollection({ collection: 'c' }), '/v1/delete-collection', POST, { collection: 'c' }],
    ['importURL minimal', (c) => c.importURL({ collection: 'c', url: 'https://x', lang: 'en' }), '/v1/import-url', POST, { collection: 'c', url: 'https://x', lang: 'en' }],
    ['importURL with key, meta, ttl', (c) => c.importURL({ collection: 'c', url: 'u', lang: 'en', key: 'k', meta: { a: ['b'] }, ttl: 60 }), '/v1/import-url', POST, { collection: 'c', url: 'u', lang: 'en', key: 'k', meta: { a: ['b'] }, ttl: 60 }],
    ['setTTL', (c) => c.setTTL({ collection: 'c', key: 'k', lang: 'en', ttl: 5 }), '/v1/set-ttl', POST, { collection: 'c', key: 'k', lang: 'en', ttl: 5 }],
    ['updateDocument minimal', (c) => c.updateDocument({ collection: 'c', key: 'k', lang: 'en' }), '/v1/update', 'PATCH', { collection: 'c', key: 'k', lang: 'en' }],
    ['updateDocument full', (c) => c.updateDocument({ collection: 'c', key: 'k', lang: 'en', meta: {}, contentMd: '', ttl: 0 }), '/v1/update', 'PATCH', { collection: 'c', key: 'k', lang: 'en', meta: {}, contentMd: '', ttl: 0 }],
    ['getDocumentMeta encodes and defaults lang', (c) => c.getDocumentMeta({ collection: 'a b', key: 'k/1' }), '/v1/doc-meta?collection=a%20b&key=k%2F1&lang=en', GET],
    ['getDocumentMeta with lang', (c) => c.getDocumentMeta({ collection: 'c', key: 'k', lang: 'pl' }), '/v1/doc-meta?collection=c&key=k&lang=pl', GET],
    ['getMetaKeys', (c) => c.getMetaKeys('a&b'), '/v1/meta-keys?collection=a%26b', GET],
    ['getRevisions', (c) => c.getRevisions({ collection: 'c', key: 'k', lang: 'en' }), '/v1/revisions', POST, { collection: 'c', key: 'k', lang: 'en' }],
    ['restoreRevision', (c) => c.restoreRevision({ collection: 'c', key: 'k', lang: 'en', timestamp: 7 }), '/v1/revisions/restore', POST, { collection: 'c', key: 'k', lang: 'en', timestamp: 7 }],
    ['classify', (c) => c.classify({ collection: 'c', key: 'k', lang: 'en', labels: ['a'], topK: 2, multi: true, threshold: 0.3 }), '/v1/classify', POST, { collection: 'c', key: 'k', lang: 'en', labels: ['a'], topK: 2, multi: true, threshold: 0.3 }],
  ]);
});

describe('vector and geo', () => {
  expectEndpoints(client, [
    ['vectorSearch defaults', (c) => c.vectorSearch({ collection: 'c', query: 'q' }), '/v1/vector-search', POST, { collection: 'c', query: 'q', topK: 5, threshold: 0, filterMeta: {}, includeContent: false, algorithm: 'flat', distanceMetric: 'cosine' }],
    ['vectorReindex', (c) => c.vectorReindex({ collection: 'c' }), '/v1/vector-reindex', POST, { collection: 'c', force: false }],
    ['vectorStats', (c) => c.vectorStats(), '/v1/vector-stats', GET],
    ['vectorProjection', (c) => c.vectorProjection({ collection: 'c', query: 'q' }), '/v1/vector-projection', POST, { collection: 'c', sample: 1000, query: 'q' }],
    ['getVectorStats', (c) => c.getVectorStats(), '/v1/vector-stats', GET],
    ['reindexVectors', (c) => c.reindexVectors('c'), '/v1/vector-reindex', POST, { collection: 'c' }],
    ['geoSearch defaults', (c) => c.geoSearch({ collection: 'c', lat: 1, lng: 2, radiusMeters: 3 }), '/v1/geo-search', POST, { collection: 'c', lat: 1, lng: 2, radiusMeters: 3, topK: 10, algorithm: 'rtree', filterMeta: {}, includeContent: false }],
    ['geoWithin defaults', (c) => c.geoWithin({ collection: 'c', minLat: 1, maxLat: 2, minLng: 3, maxLng: 4 }), '/v1/geo-within', POST, { collection: 'c', minLat: 1, maxLat: 2, minLng: 3, maxLng: 4, filterMeta: {}, includeContent: false }],
    ['geoPolygon polygon', (c) => c.geoPolygon({ collection: 'c', polygon: [[[0, 0]]] }), '/v1/geo-polygon', POST, { collection: 'c', filterMeta: {}, includeContent: false, polygon: [[[0, 0]]] }],
    ['geoPolygon multiPolygon', (c) => c.geoPolygon({ collection: 'c', multiPolygon: [[[[0, 0]]]] }), '/v1/geo-polygon', POST, { collection: 'c', filterMeta: {}, includeContent: false, multiPolygon: [[[[0, 0]]]] }],
    ['geoStats', (c) => c.geoStats(), '/v1/geo-stats', GET],
    ['geoEncode defaults precision', (c) => c.geoEncode({ lat: 1, lng: 2 }), '/v1/geo-encode', POST, { lat: 1, lng: 2, precision: 12 }],
    ['geoDecode', (c) => c.geoDecode({ geohash: 'u4pr' }), '/v1/geo-decode', POST, { geohash: 'u4pr' }],
    ['geoReindex without arguments', (c) => c.geoReindex(), '/v1/geo-reindex', POST, { collection: '', loadPostcodes: [] }],
  ]);
});

describe('full-text and hybrid', () => {
  const ftsBase = { collection: 'c', query: 'q', limit: 50, algorithm: 'tfidf', fuzzy: 0, disableStem: false, disableSynonyms: false };
  const hybridBase = { collection: 'c', query: 'q', topK: 10, algorithm: 'bm25', vectorAlgorithm: 'flat', alpha: 0.5, strategy: 'alpha', rrfK: 60, fuzzy: 0, threshold: 0, filterMeta: {}, includeContent: false, distanceMetric: 'cosine' };
  expectEndpoints(client, [
    ['ftsSearch omits every optional field', (c) => c.ftsSearch({ collection: 'c', query: 'q', filterMeta: null, rangeMeta: [], boost: {} }), '/v1/fts', POST, ftsBase],
    ['ftsSearch proximity, filters, bm25f weights, highlight options', (c) => c.ftsSearch({
      collection: 'c', query: 'q', algorithm: 'bm25f', mode: 'proximity', distance: 3, fieldWeights: { title: 2 },
      filterMeta: { a: ['b'] }, rangeMeta: [{ field: 'n', gte: 1 }], lang: 'pl', boost: { t: 2 },
      highlight: true, highlightTag: 'em', maxHighlights: 2, fragmentSize: 80,
    }), '/v1/fts', POST, {
      ...ftsBase, algorithm: 'bm25f', mode: 'proximity', distance: 3, fieldWeights: { title: 2 },
      filterMeta: { a: ['b'] }, rangeMeta: [{ field: 'n', gte: 1 }], lang: 'pl', boost: { t: 2 },
      highlight: true, highlightTag: 'em', maxHighlights: 2, fragmentSize: 80,
    }],
    ['ftsSearch phrase mode, weights ignored off bm25f, bare highlight', (c) => c.ftsSearch({ collection: 'c', query: 'q', mode: 'phrase', distance: 3, fieldWeights: { t: 1 }, highlight: true }), '/v1/fts', POST, { ...ftsBase, mode: 'phrase', highlight: true }],
    ['ftsSearch proximity without distance', (c) => c.ftsSearch({ collection: 'c', query: 'q', mode: 'proximity', algorithm: 'bm25f' }), '/v1/fts', POST, { ...ftsBase, algorithm: 'bm25f', mode: 'proximity' }],
    ['ftsReindex', (c) => c.ftsReindex({ collection: 'c' }), '/v1/fts-reindex', POST, { collection: 'c' }],
    ['ftsLanguages', (c) => c.ftsLanguages(), '/v1/fts-languages', GET],
    ['autocomplete defaults', (c) => c.autocomplete({ collection: 'c', q: 'pr' }), '/v1/autocomplete?collection=c&q=pr&topN=10', GET],
    ['autocomplete with field', (c) => c.autocomplete({ collection: 'c', q: 'a b', field: 'title', topN: 3 }), '/v1/autocomplete?collection=c&q=a+b&topN=3&field=title', GET],
    ['hybridSearch defaults, zero signals dropped', (c) => c.hybridSearch({ collection: 'c', query: 'q', signals: { recency: 0 }, boost: {} }), '/v1/hybrid-search', POST, hybridBase],
    ['hybridSearch all optional fields', (c) => c.hybridSearch({ collection: 'c', query: 'q', lang: 'en', signals: { recency: 0.2 }, boost: { a: 1 }, geo: { lat: 1 }, sort: 'date' }), '/v1/hybrid-search', POST, { ...hybridBase, lang: 'en', signals: { recency: 0.2 }, boost: { a: 1 }, geo: { lat: 1 }, sort: 'date' }],
    ['crossSearch', (c) => c.crossSearch({ query: 'q', collections: ['a'] }), '/v1/cross-search', POST, { query: 'q', collections: ['a'] }],
    ['searchAdvisor', (c) => c.searchAdvisor('a b'), '/v1/search-advisor?collection=a+b', GET],
    ['searchAdvisor apply', (c) => c.searchAdvisor('c', { apply: true }), '/v1/search-advisor?collection=c&apply=true', GET],
    ['codeGraph defaults', (c) => c.codeGraph({ collection: 'c', key: 'k' }), '/v1/code-graph?collection=c&key=k&direction=both&depth=1&maxDegree=100', GET],
    ['codeGraph with lines', (c) => c.codeGraph({ collection: 'c', key: 'k', direction: 'in', depth: 2, maxDegree: 5, lines: true }), '/v1/code-graph?collection=c&key=k&direction=in&depth=2&maxDegree=5&lines=true', GET],
  ]);
});

describe('synonyms, stop words, webhooks', () => {
  expectEndpoints(client, [
    ['listSynonyms', (c) => c.listSynonyms('a/b'), '/v1/synonyms?collection=a%2Fb', GET],
    ['setSynonym', (c) => c.setSynonym({ collection: 'c', term: 't', synonyms: ['s'] }), '/v1/synonyms', POST, { collection: 'c', term: 't', synonyms: ['s'] }],
    ['deleteSynonym', (c) => c.deleteSynonym({ collection: 'c', term: 't' }), '/v1/synonyms', DELETE, { collection: 'c', term: 't' }],
    ['listStopWords', (c) => c.listStopWords('c'), '/v1/stopwords?collection=c', GET],
    ['listStopWords with lang', (c) => c.listStopWords('c', 'pl'), '/v1/stopwords?collection=c&lang=pl', GET],
    ['addStopWords', (c) => c.addStopWords({ collection: 'c', words: ['a'] }), '/v1/stopwords', POST, { collection: 'c', words: ['a'] }],
    ['deleteStopWord', (c) => c.deleteStopWord({ collection: 'c', words: ['a'] }), '/v1/stopwords', DELETE, { collection: 'c', words: ['a'] }],
    ['registerWebhook global', (c) => c.registerWebhook({ url: 'u', events: ['add'] }), '/v1/webhooks', POST, { url: 'u', events: ['add'] }],
    ['registerWebhook per collection', (c) => c.registerWebhook({ url: 'u', events: ['add'], collection: 'c' }), '/v1/webhooks', POST, { url: 'u', events: ['add'], collection: 'c' }],
    ['listWebhooks', (c) => c.listWebhooks(), '/v1/webhooks', GET],
    ['deleteWebhook', (c) => c.deleteWebhook('w1'), '/v1/webhooks/delete', POST, { id: 'w1' }],
  ]);
});
