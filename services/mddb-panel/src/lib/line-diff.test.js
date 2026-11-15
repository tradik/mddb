// line-diff (#404, FE-013): what "Update from source" would change in a
// skill, what a revision changed and how two documents differ.
import { describe, test, expect } from 'vitest';
import { lineDiff, diffStats, maxCells } from './line-diff';

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

describe('lineDiff on documents (FE-013)', () => {
  test('a shared start and end keep their line numbers around a change', () => {
    const diff = lineDiff('h\na\nb\nt', 'h\nx\nb\ny\nt');
    expect(compact(diff)).toEqual([' h', '-a', '+x', ' b', '+y', ' t']);
    expect(diff.at(-1)).toEqual({ type: 'same', text: 't', oldLine: 4, newLine: 5 });
  });

  test('a middle part too big for the table is shown as a replacement', () => {
    const side = Math.ceil(Math.sqrt(maxCells)) + 1;
    const before = Array.from({ length: side }, (_, i) => `old ${i}`).join('\n');
    const after = Array.from({ length: side }, (_, i) => `new ${i}`).join('\n');
    const diff = lineDiff(`top\n${before}\nend`, `top\n${after}\nend`);
    expect(diffStats(diff)).toEqual({ added: side, removed: side });
    expect(diff[1]).toEqual({ type: 'removed', text: 'old 0', oldLine: 2, newLine: null });
    expect(diff[side + 1]).toEqual({ type: 'added', text: 'new 0', oldLine: null, newLine: 2 });
    expect(diff.at(-1)).toEqual({ type: 'same', text: 'end', oldLine: side + 2, newLine: side + 2 });
  });
});
