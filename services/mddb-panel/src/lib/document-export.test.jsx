// @vitest-environment jsdom
// document-export (FE-017): Download HTML renders through the real
// MarkdownView, so a document's scripts and event handlers never reach the file.
import { describe, test, expect, vi, afterEach } from 'vitest';

vi.mock('./download', () => ({ saveBlob: vi.fn() }));

const { saveBlob } = await import('./download');
const { documentHtml, downloadDocumentHtml } = await import('./document-export');

afterEach(() => vi.mocked(saveBlob).mockReset());

const hostile = {
  collection: 'docs',
  key: 'evil<script>',
  lang: 'en_US',
  meta: { author: ['<b>Ann</b>'] },
  contentMd: [
    '# Title',
    '',
    '<script>alert("content")</script>',
    '',
    '<img src="x.png" onerror="alert(2)">',
    '',
    '[link](javascript:alert(3))',
    '',
    '<iframe src="https://example.com"></iframe>',
    '',
    '**bold** text',
  ].join('\n'),
};

describe('documentHtml', () => {
  test('keeps the rendered content and drops scripts, handlers and javascript: links', async () => {
    const html = await documentHtml(hostile);
    expect(html).toContain('<title>evil&lt;script&gt;</title>');
    expect(html).toContain('<strong>bold</strong>');
    expect(html).toContain('Title</h1>');
    expect(html).not.toMatch(/<script/i);
    expect(html).not.toMatch(/onerror/i);
    expect(html).not.toMatch(/javascript:/i);
    expect(html).not.toMatch(/<iframe/i);
    // The metadata value is text, not markup.
    expect(html).toContain('<dd class="text-gray-700">&lt;b&gt;Ann&lt;/b&gt;</dd>');
  });
});

describe('downloadDocumentHtml', () => {
  test('saves the page as <key>.<lang>.html', async () => {
    await downloadDocumentHtml({ key: 'guide', lang: 'en', contentMd: 'hi' });
    const [{ blob, filename }] = vi.mocked(saveBlob).mock.calls[0];
    expect(filename).toBe('guide.en.html');
    expect(blob.type).toBe('text/html;charset=utf-8');
    expect(await blob.text()).toContain('<p class="mb-4 text-gray-700 leading-relaxed">hi</p>');
  });
});
