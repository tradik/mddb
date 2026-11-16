// collection-config: what the settings dialog sends back to PUT
// /v1/collection-config — the stored config minus output-only fields, plus the
// 2.18.9/2.18.10 settings, with a quota only when it changed.
import { describe, test, expect } from 'vitest';
import {
  EDITED_RETRIEVAL_KEYS, QUOTA_FIELDS, omit, preservedConfig, advancedFromConfig, buildQuota, applyAdvanced,
  rerankFromProfile, buildRerank,
} from './collection-config.js';

const emptyQuota = { maxDocuments: '', maxBytes: '', maxVectors: '', embeddingCallsPerDay: '', embeddingTokensPerDay: '' };

/** Advanced dialog state with nothing set, overridden by `over`. */
function advanced(over = {}) {
  return {
    vectorAlgorithm: '', summaryMode: '', summaryWeight: '', maxTopK: '',
    rerankEnabled: '', rerankCandidates: '', rerankTopN: '',
    excludeMetaKeys: '', quota: emptyQuota, ...over,
  };
}

describe('constants', () => {
  test('quota fields keep the dialog order', () => {
    expect(QUOTA_FIELDS.map((f) => f.key)).toEqual([
      'maxDocuments', 'maxBytes', 'maxVectors', 'embeddingCallsPerDay', 'embeddingTokensPerDay',
    ]);
  });

  test('the dialog edits the 2.18.10 retrieval keys', () => {
    expect(EDITED_RETRIEVAL_KEYS).toEqual(expect.arrayContaining(['vectorAlgorithm', 'summaryMode', 'summaryWeight', 'maxTopK', 'rerank']));
  });
});

describe('omit', () => {
  test('copies without the named keys and leaves the source as it is', () => {
    const source = { a: 1, b: 2, c: 3 };
    expect(omit(source, ['b', 'missing'])).toEqual({ a: 1, c: 3 });
    expect(source).toEqual({ a: 1, b: 2, c: 3 });
  });

  test('an undefined source gives an empty object', () => {
    expect(omit(undefined, ['a'])).toEqual({});
  });
});

describe('preservedConfig', () => {
  test('with no argument returns an empty object', () => {
    expect(preservedConfig()).toEqual({});
  });

  test('drops output-only fields and the edited retrieval keys, keeps the rest', () => {
    const stored = {
      type: 'website',
      readOnly: true,
      corpus: { name: 'pack', version: '1', license: 'MIT' },
      evalSet: [{ q: 'x' }],
      quota: { maxDocuments: 5 },
      fts: { enabled: true },
      code: { languages: ['go'] },
      embedding: { model: 'm' },
      wordpress: { url: 'https://wp' },
      diskOnlyVectors: true,
      storageConfig: { bucket: 'b', secretKeySet: true },
      retrieval: { topK: 5, vectorAlgorithm: 'hnsw', oversample: 3, groupBy: 'author' },
    };
    expect(preservedConfig(stored)).toEqual({
      type: 'website',
      fts: { enabled: true },
      code: { languages: ['go'] },
      embedding: { model: 'm' },
      wordpress: { url: 'https://wp' },
      diskOnlyVectors: true,
      storageConfig: { bucket: 'b' },
      retrieval: { oversample: 3, groupBy: 'author' },
    });
    // The stored object is not mutated.
    expect(stored.storageConfig.secretKeySet).toBe(true);
    expect(stored.retrieval.topK).toBe(5);
  });
});

describe('advancedFromConfig', () => {
  test('with no argument every field is empty', () => {
    expect(advancedFromConfig()).toEqual(advanced());
  });

  test('reads the stored retrieval, fts and quota settings as strings', () => {
    expect(advancedFromConfig({
      retrieval: { vectorAlgorithm: 'auto', summaryMode: 'blend', summaryWeight: 0, maxTopK: 50 },
      fts: { excludeMetaKeys: ['url', 'sha'] },
      quota: { maxDocuments: 100, maxBytes: 0, embeddingTokensPerDay: 9000 },
    })).toEqual({
      vectorAlgorithm: 'auto',
      summaryMode: 'blend',
      summaryWeight: '0',
      maxTopK: '50',
      rerankEnabled: '',
      rerankCandidates: '',
      rerankTopN: '',
      excludeMetaKeys: 'url, sha',
      quota: { ...emptyQuota, maxDocuments: '100', embeddingTokensPerDay: '9000' },
    });
  });
});

describe('model reranking (RAG-011)', () => {
  test.each([
    ['nothing stored', undefined, { rerankEnabled: '', rerankCandidates: '', rerankTopN: '' }],
    ['on with numbers', { enabled: true, candidates: 30, topN: 5 }, { rerankEnabled: 'on', rerankCandidates: '30', rerankTopN: '5' }],
    ['off', { enabled: false }, { rerankEnabled: 'off', rerankCandidates: '', rerankTopN: '' }],
  ])('reads %s', (_label, stored, want) => {
    expect(rerankFromProfile(stored)).toEqual(want);
    expect(advancedFromConfig({ retrieval: { rerank: stored } })).toMatchObject(want);
  });

  test.each([
    ['nothing set', advanced(), null],
    ['on', advanced({ rerankEnabled: 'on' }), { enabled: true }],
    ['off with numbers', advanced({ rerankEnabled: 'off', rerankCandidates: '20', rerankTopN: '3' }), { enabled: false, candidates: 20, topN: 3 }],
    ['zero and negative numbers dropped', advanced({ rerankCandidates: '0', rerankTopN: '-1' }), null],
  ])('builds %s', (_label, fields, want) => {
    expect(buildRerank(fields)).toEqual(want);
  });

  test('applyAdvanced sends the block in the retrieval profile', () => {
    const out = applyAdvanced({ retrieval: { topK: 5 } }, advanced({ rerankEnabled: 'on', rerankCandidates: '40' }));
    expect(out.retrieval).toEqual({ topK: 5, rerank: { enabled: true, candidates: 40 } });
  });

  test('a stored block the dialog cleared is not kept', () => {
    expect(preservedConfig({ retrieval: { rerank: { enabled: true }, oversample: 2 } })).toEqual({ retrieval: { oversample: 2 } });
  });
});

describe('buildQuota', () => {
  test.each([
    ['positive integers kept', { ...emptyQuota, maxDocuments: '10', maxVectors: '3' }, { maxDocuments: 10, maxVectors: 3 }],
    ['empty, zero, negative and fractional dropped', { maxDocuments: '', maxBytes: '0', maxVectors: '-1', embeddingCallsPerDay: '1.5', embeddingTokensPerDay: 'x' }, {}],
  ])('%s', (_label, fields, want) => {
    expect(buildQuota(fields)).toEqual(want);
  });
});

describe('applyAdvanced', () => {
  test('adds the retrieval settings and keeps payload retrieval keys', () => {
    const payload = { retrieval: { oversample: 2 } };
    const out = applyAdvanced(payload, advanced({ vectorAlgorithm: 'hnsw', summaryMode: 'expand', summaryWeight: '1.5', maxTopK: '20' }));
    expect(out).toBe(payload);
    expect(out.retrieval).toEqual({ oversample: 2, vectorAlgorithm: 'hnsw', summaryMode: 'expand', summaryWeight: 1.5, maxTopK: 20 });
    expect(out).not.toHaveProperty('fts');
    expect(out).not.toHaveProperty('quota');
  });

  test.each([
    ['empty', ''],
    ['zero', '0'],
  ])('a %s summary weight is not sent', (_label, summaryWeight) => {
    const out = applyAdvanced({}, advanced({ summaryWeight, maxTopK: '0' }));
    expect(out).not.toHaveProperty('retrieval');
  });

  test('an empty retrieval block is removed', () => {
    expect(applyAdvanced({ retrieval: {} }, advanced())).toEqual({});
  });

  test('excludeMetaKeys are split, trimmed and merged into fts', () => {
    const out = applyAdvanced({ fts: { enabled: true } }, advanced({ excludeMetaKeys: ' url , ,license,' }));
    expect(out.fts).toEqual({ enabled: true, excludeMetaKeys: ['url', 'license'] });
  });

  test('clearing excludeMetaKeys keeps other fts keys', () => {
    const out = applyAdvanced({ fts: { enabled: true, excludeMetaKeys: ['url'] } }, advanced());
    expect(out.fts).toEqual({ enabled: true });
  });

  test('an fts block left empty is removed', () => {
    expect(applyAdvanced({ fts: { excludeMetaKeys: ['url'] } }, advanced())).toEqual({});
  });

  test.each([
    ['unchanged quota is not sent', { maxDocuments: 10 }, { ...emptyQuota, maxDocuments: '10' }, undefined],
    ['a changed limit sends the whole quota', { maxDocuments: 10 }, { ...emptyQuota, maxDocuments: '20', maxBytes: '5' }, { maxDocuments: 20, maxBytes: 5 }],
    ['clearing every limit sends {}', { maxDocuments: 10 }, emptyQuota, {}],
    ['a new quota on a config without one', undefined, { ...emptyQuota, maxVectors: '7' }, { maxVectors: 7 }],
    ['no quota stored and none set', undefined, emptyQuota, undefined],
  ])('%s', (_label, storedQuota, fields, want) => {
    const out = applyAdvanced({}, advanced({ quota: fields }), { quota: storedQuota });
    expect(out.quota).toEqual(want);
  });

  test('stored defaults to {} when omitted', () => {
    expect(applyAdvanced({}, advanced({ quota: { ...emptyQuota, maxBytes: '1' } })).quota).toEqual({ maxBytes: 1 });
  });
});
