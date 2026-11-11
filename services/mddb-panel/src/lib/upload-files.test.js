// upload-files: which files the upload dialog accepts and their keys (2.19.0:
// every file goes to /v1/upload, which reads Markdown frontmatter).
import { describe, test, expect } from 'vitest';
import { SUPPORTED_EXTENSIONS, MAX_FILE_SIZE, extensionOf, isMarkdown, keyForFile, fileIcon, classifyFiles } from './upload-files';

const file = (name, size = 10) => ({ name, size });

describe('extensionOf()', () => {
  test.each([
    ['a.PDF', '.pdf'],
    ['notes.tar.gz', '.gz'],
    ['README', ''],
    ['.env', ''],
  ])('%s → %s', (name, ext) => expect(extensionOf(name)).toBe(ext));
});

describe('isMarkdown() and keyForFile()', () => {
  test('Markdown keeps the key earlier panels gave it', () => {
    expect(isMarkdown('My Notes.md')).toBe(true);
    expect(keyForFile('My Notes.md')).toBe('My Notes');
    expect(keyForFile('guide.markdown')).toBe('guide');
  });

  test('other formats use the server-style lower-case hyphenated key', () => {
    expect(isMarkdown('Report 2026.pdf')).toBe(false);
    expect(keyForFile('Report  2026.PDF')).toBe('report-2026');
  });
});

describe('fileIcon()', () => {
  test.each([
    ['a.pdf', '📄'], ['a.docx', '📝'], ['a.odt', '📝'], ['a.html', '🌐'], ['a.htm', '🌐'],
    ['a.md', '📋'], ['a.txt', '📃'], ['a.csv', '📎'],
  ])('%s → %s', (name, icon) => expect(fileIcon(name)).toBe(icon));
});

describe('classifyFiles()', () => {
  test('accepts every format the server converts, marking conversions', () => {
    const { accepted, errors } = classifyFiles([file('a.md'), file('b.txt'), file('c.xlsx'), file('d.epub')]);
    expect(errors).toEqual([]);
    expect(accepted.map(({ key, converted }) => [key, converted])).toEqual([
      ['a', false], ['b', false], ['c', true], ['d', true],
    ]);
    expect(SUPPORTED_EXTENSIONS).toEqual(expect.arrayContaining(['.pptx', '.ods', '.eml', '.mbox', '.tsv']));
  });

  test('rejects unknown formats and files over the limit', () => {
    const { accepted, errors } = classifyFiles([file('x.exe'), file('big.pdf', MAX_FILE_SIZE + 1)]);
    expect(accepted).toEqual([]);
    expect(errors[0]).toMatch(/^x\.exe: Unsupported format/);
    expect(errors[1]).toBe('big.pdf: File too large (max 10MB)');
  });

  test('a custom limit and an empty selection', () => {
    expect(classifyFiles([file('a.md', 3 * 1024 * 1024)], 2 * 1024 * 1024).errors).toEqual(['a.md: File too large (max 2MB)']);
    expect(classifyFiles(null)).toEqual({ accepted: [], errors: [] });
  });
});
