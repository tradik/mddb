// json-text: JSON typed into a form, and documents pasted as a JSON array
// or NDJSON.
import { describe, test, expect } from 'vitest';
import { parseJson, isPlainObject, parseDocuments } from './json-text';
import { getWithQuery } from './client/http';

describe('parseJson', () => {
  test('blank is no value, valid JSON parses, invalid returns the parser message', () => {
    expect(parseJson('  ')).toEqual({ value: undefined });
    expect(parseJson('{"a":1}')).toEqual({ value: { a: 1 } });
    expect(parseJson('{').error).toMatch(/JSON/);
  });
});

describe('isPlainObject', () => {
  test.each([[{}, true], [[], false], [null, false], ['x', false]])('%o → %s', (v, want) => expect(isPlainObject(v)).toBe(want));
});

describe('parseDocuments', () => {
  test('a JSON array, filling the language of documents without one', () => {
    expect(parseDocuments('[{"key":"a"},{"key":"b","lang":"pl"}]', 'de')).toEqual({
      documents: [{ key: 'a', lang: 'de' }, { key: 'b', lang: 'pl' }],
    });
  });

  test('NDJSON, skipping blank lines; English by default', () => {
    expect(parseDocuments('{"key":"a"}\n\n{"key":"b"}\n')).toEqual({
      documents: [{ key: 'a', lang: 'en' }, { key: 'b', lang: 'en' }],
    });
  });

  test.each([
    ['', 'Paste at least one document.'],
    ['[]', 'Expected a non-empty list of documents.'],
    ['[{"key":"a"},{"title":"x"}]', 'Document 2 has no "key".'],
    ['[1]', 'Document 1 has no "key".'],
  ])('%j → %s', (text, error) => expect(parseDocuments(text)).toEqual({ error }));

  test('a broken array or line names the problem', () => {
    expect(parseDocuments('[{').error).toMatch(/JSON/);
    expect(parseDocuments('{"key":"a"}\n{oops').error).toMatch(/^Line 2: /);
  });
});

describe('getWithQuery', () => {
  test('leaves out empty values', async () => {
    const calls = [];
    const client = { request: (path, init) => calls.push([path, init.method]) };
    getWithQuery(client, '/x', { a: '', b: null, c: undefined, d: 0 });
    getWithQuery(client, '/y');
    expect(calls).toEqual([['/x?d=0', 'GET'], ['/y', 'GET']]);
  });
});
