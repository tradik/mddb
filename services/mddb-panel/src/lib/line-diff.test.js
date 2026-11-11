// line-diff (#404): what "Update from source" would change in a skill.
import { describe, test, expect } from 'vitest';
import { lineDiff, diffStats } from './line-diff';

const compact = (diff) => diff.map((d) => `${{ same: ' ', added: '+', removed: '-' }[d.type]}${d.text}`);

describe('lineDiff', () => {
  test('equal texts are all same, numbered on both sides', () => {
    expect(lineDiff('a\nb', 'a\nb')).toEqual([
      { type: 'same', text: 'a', oldLine: 1, newLine: 1 },
      { type: 'same', text: 'b', oldLine: 2, newLine: 2 },
    ]);
  });

  test('a changed line is a removal then an addition, the rest kept', () => {
    const diff = lineDiff('# Tone\nBe kind.\nCite sources.', '# Tone\nBe kind and brief.\nCite sources.');
    expect(compact(diff)).toEqual([' # Tone', '-Be kind.', '+Be kind and brief.', ' Cite sources.']);
    expect(diff[1]).toEqual({ type: 'removed', text: 'Be kind.', oldLine: 2, newLine: null });
    expect(diff[2]).toEqual({ type: 'added', text: 'Be kind and brief.', oldLine: null, newLine: 2 });
    expect(diffStats(diff)).toEqual({ added: 1, removed: 1 });
  });

  test('lines added at the end and removed at the start', () => {
    expect(compact(lineDiff('a\nb\nc', 'b\nc\nd\ne'))).toEqual(['-a', ' b', ' c', '+d', '+e']);
  });

  test('empty and missing texts, and Windows line ends', () => {
    expect(lineDiff('', '')).toEqual([]);
    expect(compact(lineDiff(undefined, 'x'))).toEqual(['+x']);
    expect(compact(lineDiff('x', null))).toEqual(['-x']);
    expect(compact(lineDiff('a\r\nb', 'a\nb'))).toEqual([' a', ' b']);
    expect(diffStats([])).toEqual({ added: 0, removed: 0 });
  });
});
