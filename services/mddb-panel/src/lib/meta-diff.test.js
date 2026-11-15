// meta-diff (FE-013): the metadata keys a revision or another document adds,
// removes or changes.
import { describe, test, expect } from 'vitest';
import { metaDiff, metaText } from './meta-diff';

describe('metaText', () => {
  test('joins arrays, stringifies the rest, empty for nothing', () => {
    expect(metaText(['a', 'b'])).toBe('a, b');
    expect(metaText('x')).toBe('x');
    expect(metaText(3)).toBe('3');
    expect(metaText(undefined)).toBe('');
  });
});

describe('metaDiff', () => {
  test('added, removed and changed keys, sorted; unchanged ones left out', () => {
    const before = { tags: ['a', 'b'], author: ['Ann'], status: ['draft'] };
    const after = { tags: ['a', 'b'], author: ['Bob'], category: ['news'] };
    expect(metaDiff(before, after)).toEqual([
      { key: 'author', type: 'changed', before: 'Ann', after: 'Bob' },
      { key: 'category', type: 'added', before: null, after: 'news' },
      { key: 'status', type: 'removed', before: 'draft', after: null },
    ]);
  });

  test('missing maps are empty', () => {
    expect(metaDiff(undefined, null)).toEqual([]);
    expect(metaDiff(null, { a: 'x' })).toEqual([{ key: 'a', type: 'added', before: null, after: 'x' }]);
  });
});
