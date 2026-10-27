import * as os from 'node:os';
import * as path from 'node:path';
import * as fs from 'node:fs/promises';
import {
  basenameKey,
  buildContentMd,
  buildDocument,
  buildMeta,
  deriveKey,
  slugifyPath,
} from '../src/document.js';

describe('slugifyPath', () => {
  it('lowercases and replaces unsafe characters', () => {
    expect(slugifyPath('Docs/Guide V2.md')).toBe('docs/guide-v2.md');
  });

  it('collapses runs of dashes and trims them', () => {
    expect(slugifyPath('a  b   c')).toBe('a-b-c');
    expect(slugifyPath('---hello---')).toBe('hello');
  });

  it('trims leading and trailing slashes', () => {
    expect(slugifyPath('//docs/guide.md//')).toBe('docs/guide.md');
    expect(slugifyPath('///')).toBe('');
  });

  it('normalises Windows separators', () => {
    expect(slugifyPath('docs\\guide.md')).toBe('docs/guide.md');
  });
});

describe('basenameKey', () => {
  it('returns the slugified file basename without extension', () => {
    expect(basenameKey('docs/sub/Hello World.MD')).toBe('hello-world');
  });
});

describe('deriveKey', () => {
  it('hashes content for the hash strategy', () => {
    const k1 = deriveKey('hash', 'a.md', 'hello');
    const k2 = deriveKey('hash', 'a.md', 'hello');
    const k3 = deriveKey('hash', 'a.md', 'world');
    expect(k1).toHaveLength(40);
    expect(k1).toBe(k2);
    expect(k1).not.toBe(k3);
  });

  it('uses path or filename for non-hash strategies', () => {
    expect(deriveKey('path', 'Docs/A.md', '')).toBe('docs/a.md');
    expect(deriveKey('filename', 'Docs/A.md', '')).toBe('a');
  });
});

describe('buildContentMd', () => {
  it('passes markdown content through unchanged', () => {
    const raw = '# Title\n\nBody.';
    expect(buildContentMd('a.md', raw)).toBe(raw);
    expect(buildContentMd('a.markdown', raw)).toBe(raw);
    expect(buildContentMd('a.mdx', raw)).toBe(raw);
  });

  it('wraps known file types in a fenced code block with language', () => {
    const out = buildContentMd('config.yaml', 'foo: 1');
    expect(out.startsWith('```yaml\n')).toBe(true);
    expect(out.trim().endsWith('```')).toBe(true);
  });

  it('falls back to an empty fence language for unknown types', () => {
    const out = buildContentMd('weird.bin', 'data');
    expect(out.startsWith('```\n')).toBe(true);
  });

  it('escapes nested triple backticks to keep the fence valid', () => {
    const out = buildContentMd('a.json', '{"x":"```"}');
    expect(out).not.toContain('"```"');
    expect(out).toContain('` ``');
  });

  it('treats plain text extensions as markdown-equivalent', () => {
    expect(buildContentMd('NOTES.rst', 'hi')).toBe('hi');
  });
});

describe('buildMeta', () => {
  it('always includes source/path/extension/size', () => {
    const meta = buildMeta(
      {
        collection: 'c',
        language: 'en_US',
        keyStrategy: 'path',
        keyPrefix: '',
        relativePath: 'docs/a.md',
        absolutePath: '/tmp/docs/a.md',
      },
      42,
    );
    expect(meta.source).toEqual(['github-action']);
    expect(meta.path).toEqual(['docs/a.md']);
    expect(meta.extension).toEqual(['.md']);
    expect(meta.size).toEqual(['42']);
    expect(meta.repository).toBeUndefined();
  });

  it('records (none) when the file has no extension', () => {
    const meta = buildMeta(
      {
        collection: 'c',
        language: 'en_US',
        keyStrategy: 'path',
        keyPrefix: '',
        relativePath: 'LICENSE',
        absolutePath: '/tmp/LICENSE',
      },
      10,
    );
    expect(meta.extension).toEqual(['(none)']);
  });

  it('records repository / ref when provided', () => {
    const meta = buildMeta(
      {
        collection: 'c',
        language: 'en_US',
        keyStrategy: 'path',
        keyPrefix: '',
        relativePath: 'a.md',
        absolutePath: '/tmp/a.md',
        repository: 'tradik/mddb',
        ref: 'deadbeef',
      },
      1,
    );
    expect(meta.repository).toEqual(['tradik/mddb']);
    expect(meta.ref).toEqual(['deadbeef']);
  });
});

describe('buildDocument', () => {
  let tmpDir: string;

  beforeAll(async () => {
    tmpDir = await fs.mkdtemp(path.join(os.tmpdir(), 'mddb-action-doc-'));
  });

  afterAll(async () => {
    await fs.rm(tmpDir, { recursive: true, force: true });
  });

  it('reads the file and builds a document with prefix applied', async () => {
    const filePath = path.join(tmpDir, 'guide.md');
    await fs.writeFile(filePath, '# Hello', 'utf8');

    const doc = await buildDocument({
      collection: 'docs',
      language: 'en_US',
      keyStrategy: 'path',
      keyPrefix: 'site:',
      relativePath: 'guide.md',
      absolutePath: filePath,
      repository: 'tradik/mddb',
      ref: 'main',
    });

    expect(doc.collection).toBe('docs');
    expect(doc.key).toBe('site:guide.md');
    expect(doc.lang).toBe('en_US');
    expect(doc.contentMd).toBe('# Hello');
    expect(doc.meta.path).toEqual(['guide.md']);
    expect(doc.meta.repository).toEqual(['tradik/mddb']);
  });
});
