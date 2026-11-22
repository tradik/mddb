// training: the Model training page's form logic, labels and the
// before/after of an evaluation.
import { describe, test, expect } from 'vitest';
import {
  emptyDatasetForm, withFormat, datasetRequest, parameterDefaults, parameterValue, parameterRequest,
  statusLabel, isActive, formatBytes, percent, evaluationView, DEFAULT_VALIDATION_RATIO,
} from './training';

describe('the dataset form', () => {
  test('starts as pairs with a validation share', () => {
    const form = emptyDatasetForm();
    expect(form.format).toBe('pairs');
    expect(form.validationRatio).toBe(DEFAULT_VALIDATION_RATIO);
  });

  test('switching to pairs gives a share when there was none, and keeps one that was set', () => {
    expect(withFormat({ ...emptyDatasetForm(), validationRatio: '0' }, 'pairs').validationRatio).toBe('0.1');
    expect(withFormat({ ...emptyDatasetForm(), validationRatio: '' }, 'pairs').validationRatio).toBe('0.1');
    expect(withFormat({ ...emptyDatasetForm(), validationRatio: '0.3' }, 'pairs').validationRatio).toBe('0.3');
    expect(withFormat({ ...emptyDatasetForm(), validationRatio: '0' }, 'text').validationRatio).toBe('0');
  });

  test('becomes the request, leaving out what is empty', () => {
    expect(datasetRequest({ ...emptyDatasetForm(), collections: ['a'], format: 'text', seed: 'x', validationRatio: '' })).toEqual({
      collections: ['a'], format: 'text', shuffle: true, seed: 0, validationRatio: 0, dedupe: true, acknowledgeUnlicensed: false,
    });
    const full = datasetRequest({
      ...emptyDatasetForm(), name: ' n ', collections: ['a', 'b'], input: '{meta.q}', system: 'S', lang: ' en ',
      maxRecords: '10', filterKey: 'cuisine', filterValue: 'french', acknowledgeUnlicensed: true,
    });
    expect(full).toMatchObject({
      name: 'n', lang: 'en', maxRecords: 10, filterMeta: { cuisine: ['french'] }, acknowledgeUnlicensed: true,
      pairs: { input: '{meta.q}', output: '{content}', system: 'S' }, seed: 1, validationRatio: 0.1,
    });
    expect(datasetRequest({ ...emptyDatasetForm(), filterKey: 'k' }).filterMeta).toBeUndefined();
    expect(datasetRequest(emptyDatasetForm()).pairs).toEqual({ input: '', output: '{content}' });
  });
});

describe('parameters', () => {
  const profile = { parameters: [
    { name: 'epochs', type: 'int', default: 3 }, { name: 'fast', type: 'bool' }, { name: 'tag', type: 'string' },
  ] };

  test('start at their defaults, switches off', () => {
    expect(parameterDefaults(profile)).toEqual({ epochs: 3, fast: false });
    expect(parameterDefaults(undefined)).toEqual({});
    expect(parameterDefaults({})).toEqual({});
  });

  test('take the type of their declaration', () => {
    expect(parameterValue({ type: 'int' }, '4')).toBe(4);
    expect(parameterValue({ type: 'float' }, '0.5')).toBe(0.5);
    expect(parameterValue({ type: 'int' }, '')).toBeUndefined();
    expect(parameterValue({ type: 'string' }, undefined)).toBeUndefined();
    expect(parameterValue({ type: 'enum' }, 'a')).toBe('a');
    expect(parameterValue({ type: 'bool' }, true)).toBe(true);
    expect(parameterValue({ type: 'bool' }, '')).toBe(false);
  });

  test('are sent without empty values', () => {
    expect(parameterRequest({ a: 1, b: undefined, c: '', d: false })).toEqual({ a: 1, d: false });
  });
});

describe('labels and numbers', () => {
  test('status words', () => {
    expect(statusLabel('completedWithWarnings')).toBe('Done with warnings');
    expect(statusLabel('interrupted')).toMatch(/restart/);
    expect(statusLabel('new')).toBe('new');
    expect(isActive({ status: 'pending' })).toBe(true);
    expect(isActive({ status: 'processing' })).toBe(true);
    expect(isActive({ status: 'failed' })).toBe(false);
    expect(isActive(null)).toBe(false);
  });

  test('sizes and scores', () => {
    expect(formatBytes(0)).toBe('0 B');
    expect(formatBytes(512)).toBe('512 B');
    expect(formatBytes(1536)).toBe('1.5 KB');
    expect(formatBytes(1024 ** 6)).toBe('1048576.0 TB');
    expect(percent(0.756)).toBe('75.6%');
  });
});

describe('evaluationView', () => {
  test('nothing ran', () => {
    expect(evaluationView(null)).toBeNull();
  });

  test('before and after side by side', () => {
    const view = evaluationView({
      base: { report: { score: 0.4, metrics: { exact: 0.4, b: 1 } } },
      result: { report: { score: 0.7, metrics: { exact: 0.7, a: 2 }, samples: [1, 2, 3, 4, 5, 6] } },
      delta: 0.3, notes: ['n'],
    });
    expect(view.delta).toBe(0.3);
    expect(view.metrics).toEqual([
      { name: 'a', before: undefined, after: 2 }, { name: 'b', before: 1, after: undefined }, { name: 'exact', before: 0.4, after: 0.7 },
    ]);
    expect(view.samples).toHaveLength(5);
    expect(view.notes).toEqual(['n']);
    expect(view.skipped).toBe('');
  });

  test('failures, a skip, and no reports', () => {
    const view = evaluationView({ base: { error: 'boom' }, result: { error: 'bad report' }, skipped: 'why' });
    expect(view).toMatchObject({ before: null, after: null, delta: null, metrics: [], errors: ['boom', 'bad report'], notes: [], samples: [], skipped: 'why' });
  });
});
