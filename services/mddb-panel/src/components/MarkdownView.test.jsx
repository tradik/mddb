// @vitest-environment jsdom
// MarkdownView (2.19.6): the one Markdown renderer of the editor preview and
// the document viewer — styled headings and blocks, GFM tables, the three
// shapes of code, and raw HTML through the sanitizer.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';

vi.mock('react-syntax-highlighter', () => ({
  Prism: ({ children, language, className }) => <pre data-language={language} className={className}>{children}</pre>,
}));
vi.mock('react-syntax-highlighter/dist/esm/styles/prism', () => ({ vscDarkPlus: {} }));

const { default: MarkdownView } = await import('./MarkdownView');

afterEach(cleanup);

const markdown = [
  '# One', '## Two', '### Three', '#### Four',
  'A paragraph with `inline code` and a [link](https://example.com).',
  '- item', '1. first', '> quoted', '---',
  '| a | b |', '| - | - |', '| 1 | 2 |', '',
  '```js', 'const a = 1;', '```', '',
  '```', 'plain', 'block', '```', '',
  '![alt text](pic.png)', '',
  '<span onclick="alert(1)">raw html</span>',
].join('\n');

describe('MarkdownView', () => {
  test('headings carry the preview styling, not plain text', () => {
    render(<MarkdownView>{markdown}</MarkdownView>);
    expect(screen.getByRole('heading', { level: 1, name: 'One' }).className).toContain('text-3xl');
    expect(screen.getByRole('heading', { level: 2, name: 'Two' }).className).toContain('text-2xl');
    expect(screen.getByRole('heading', { level: 3, name: 'Three' }).className).toContain('text-xl');
    expect(screen.getByRole('heading', { level: 4, name: 'Four' }).className).toContain('text-lg');
  });

  test('blocks, lists, tables, links and images are styled', () => {
    const { container } = render(<MarkdownView>{markdown}</MarkdownView>);
    expect(screen.getByRole('link', { name: 'link' }).className).toContain('underline');
    expect(container.querySelector('ul').className).toContain('list-disc');
    expect(container.querySelector('ol').className).toContain('list-decimal');
    expect(container.querySelector('li').className).toContain('text-gray-700');
    expect(container.querySelector('blockquote').className).toContain('border-l-4');
    expect(container.querySelector('hr').className).toContain('my-6');
    expect(container.querySelector('table').parentElement.className).toContain('overflow-x-auto');
    expect(container.querySelector('thead').className).toContain('bg-gray-50');
    expect(container.querySelector('th').className).toContain('uppercase');
    expect(container.querySelector('td').className).toContain('border-t');
    expect(screen.getByAltText('alt text').className).toContain('max-w-full');
  });

  test('fenced code with a language is highlighted; without one it is a dark block; inline code stays inline', () => {
    const { container } = render(<MarkdownView>{markdown}</MarkdownView>);
    const highlighted = container.querySelector('pre[data-language="js"]');
    expect(highlighted.textContent).toBe('const a = 1;');
    const block = screen.getByText(/plain\s+block/);
    expect(block.tagName).toBe('CODE');
    expect(block.className).toContain('block');
    const inline = screen.getByText('inline code');
    expect(inline.className).toContain('bg-gray-100');
    expect(inline.className).not.toContain('block');
  });

  test('raw HTML is sanitized', () => {
    render(<MarkdownView>{markdown}</MarkdownView>);
    expect(screen.getByText('raw html').getAttribute('onclick')).toBeNull();
  });
});
