// spell-words (FE-020): the plain word list the editor sends to
// /v1/spell-suggest (no code, URLs, markup or punctuation), and replacing a
// word everywhere while keeping its capitalisation.
import { describe, test, expect } from 'vitest';
import { extractWords, matchCase, replaceWord } from './spell-words';

describe('extractWords', () => {
  test('distinct lower-cased words of three letters or more, in order', () => {
    expect(extractWords('The cat, the DOG; a big Cat! Zażółć gęślą.')).toEqual(['the', 'cat', 'dog', 'big', 'zażółć', 'gęślą']);
  });

  test('leaves out code, link targets, tags, URLs and e-mail addresses', () => {
    const md = [
      'Intro text',
      '```js',
      'const varible = 1;',
      '```',
      'Use `codeword` and [link label](https://exmple.com/pathword) <span class="klass">inner</span>',
      'See https://bare.exmple.org/x and mail ann@mailhost.org now',
    ].join('\n');
    expect(extractWords(md)).toEqual(['intro', 'text', 'use', 'and', 'link', 'label', 'inner', 'see', 'mail', 'now']);
  });

  test('an unclosed code fence runs to the end; empty input has no words', () => {
    expect(extractWords('Before\n```\nhidden words')).toEqual(['before']);
    expect(extractWords('')).toEqual([]);
    expect(extractWords(undefined)).toEqual([]);
  });
});

describe('extractWords edge cases', () => {
  test('a "<" without a ">" on its line is text, not a tag', () => {
    expect(extractWords('price < cost\nand > more')).toEqual(['price', 'cost', 'and', 'more']);
    expect(extractWords('open <never closed')).toEqual(['open', 'never', 'closed']);
  });

  test('null input has no words', () => {
    expect(extractWords(null)).toEqual([]);
    expect(extractWords()).toEqual([]);
  });
});

describe('matchCase', () => {
  test('keeps all caps, a capital first letter, or lower case', () => {
    expect(matchCase('TEH', 'the')).toBe('THE');
    expect(matchCase('Teh', 'the')).toBe('The');
    expect(matchCase('teh', 'the')).toBe('the');
    expect(matchCase('T', 'the')).toBe('The');
  });
});

describe('replaceWord', () => {
  test('replaces whole words only, in any case, keeping each one’s case', () => {
    expect(replaceWord('Teh cat; teh tehran TEH', 'teh', 'the')).toBe('The cat; the tehran THE');
  });

  test('letters around a word (any script) keep it whole', () => {
    expect(replaceWord('żteh teh2 teh', 'teh', 'the')).toBe('żteh teh2 the');
  });
});
