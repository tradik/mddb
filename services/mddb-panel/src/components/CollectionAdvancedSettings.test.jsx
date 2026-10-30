// @vitest-environment jsdom
// CollectionAdvancedSettings: the 2.18.9/2.18.10 fields of the collection
// settings dialog — each control shows its value and reports a change through
// onChange with the rest of the state untouched.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import { VECTOR_ALGORITHMS } from '../lib/search-options';

const { default: CollectionAdvancedSettings } = await import('./CollectionAdvancedSettings');
const { advancedFromConfig } = await import('../lib/collection-config');

const value = advancedFromConfig({
  retrieval: { vectorAlgorithm: 'hnsw', summaryMode: 'blend', summaryWeight: 2, maxTopK: 40 },
  fts: { excludeMetaKeys: ['url'] },
  quota: { maxDocuments: 1000 },
});

afterEach(cleanup);

/** Renders the settings with `value` and returns the onChange spy. */
function renderSettings() {
  const onChange = vi.fn();
  render(<CollectionAdvancedSettings value={value} onChange={onChange} />);
  return onChange;
}

describe('CollectionAdvancedSettings', () => {
  test('shows the stored values', () => {
    renderSettings();
    expect(screen.getByLabelText('Vector index').value).toBe('hnsw');
    expect(screen.getByLabelText('Result cap (maxTopK)').value).toBe('40');
    expect(screen.getByLabelText('Document summaries').value).toBe('blend');
    expect(screen.getByLabelText('Summary weight').value).toBe('2');
    expect(screen.getByLabelText('Meta keys kept out of full-text search').value).toBe('url');
    expect(screen.getByLabelText('Max documents').value).toBe('1000');
    expect(screen.getByLabelText('Max bytes').value).toBe('');
  });

  test('offers the MDDB default and every vector algorithm', () => {
    renderSettings();
    const options = [...screen.getByLabelText('Vector index').options].map((o) => o.value);
    expect(options).toEqual(['', ...VECTOR_ALGORITHMS.map((a) => a.value)]);
  });

  test.each([
    ['Vector index', 'auto', 'vectorAlgorithm'],
    ['Result cap (maxTopK)', '10', 'maxTopK'],
    ['Document summaries', 'expand', 'summaryMode'],
    ['Summary weight', '0.5', 'summaryWeight'],
    ['Meta keys kept out of full-text search', 'url, sha', 'excludeMetaKeys'],
  ])('changing %s reports %s', (label, next, key) => {
    const onChange = renderSettings();
    fireEvent.change(screen.getByLabelText(label), { target: { value: next } });
    expect(onChange).toHaveBeenCalledWith({ ...value, [key]: next });
  });

  test.each([
    ['Max documents', 'maxDocuments'],
    ['Max bytes', 'maxBytes'],
    ['Max vectors', 'maxVectors'],
    ['Embedding calls / day', 'embeddingCallsPerDay'],
    ['Embedding tokens / day', 'embeddingTokensPerDay'],
  ])('changing quota %s keeps the other limits', (label, key) => {
    const onChange = renderSettings();
    fireEvent.change(screen.getByLabelText(label), { target: { value: '7' } });
    expect(onChange).toHaveBeenCalledWith({ ...value, quota: { ...value.quota, [key]: '7' } });
  });
});
