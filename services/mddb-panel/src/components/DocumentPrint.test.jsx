// @vitest-environment jsdom
// DocumentPrintView and DocumentPrint (FE-017): the document as a page of
// its own, put under <body> while the print dialog is open.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import { format } from 'date-fns';

vi.mock('./MarkdownView', () => ({ default: ({ children }) => <div data-testid="markdown">{children}</div> }));

const { default: DocumentPrintView } = await import('./DocumentPrintView');
const { default: DocumentPrint } = await import('./DocumentPrint');

afterEach(cleanup);

const doc = { collection: 'docs', key: 'guide', lang: 'en', updatedAt: 1_700_000_000, meta: { tags: ['a', 'b'] }, contentMd: '# Guide' };

describe('DocumentPrintView', () => {
  test('title, collection · language · update, metadata and content', () => {
    render(<DocumentPrintView document={doc} />);
    expect(screen.getByRole('heading', { level: 1, name: 'guide' })).toBeTruthy();
    expect(screen.getByText(`docs · en · updated ${format(new Date(doc.updatedAt * 1000), 'PPpp')}`)).toBeTruthy();
    expect(screen.getByText('tags').tagName).toBe('DT');
    expect(screen.getByText('a, b').tagName).toBe('DD');
    expect(screen.getByTestId('markdown').textContent).toBe('# Guide');
  });

  test('without metadata, dates or content', () => {
    const { container } = render(<DocumentPrintView document={{ key: 'k', lang: 'pl' }} />);
    expect(screen.getByText('pl')).toBeTruthy();
    expect(container.querySelector('dl')).toBeNull();
    expect(screen.getByTestId('markdown').textContent).toBe('');
  });
});

describe('DocumentPrint', () => {
  test('mounts the print view under <body>, opens the dialog and reports when it closes', () => {
    const print = vi.spyOn(window, 'print').mockImplementation(() => {});
    const onDone = vi.fn();
    const { unmount } = render(<DocumentPrint document={doc} onDone={onDone} />);
    const root = document.body.querySelector(':scope > .mddb-print-root');
    expect(root.textContent).toContain('guide');
    expect(print).toHaveBeenCalledTimes(1);
    window.dispatchEvent(new Event('afterprint'));
    expect(onDone).toHaveBeenCalledTimes(1);
    unmount();
    expect(document.body.querySelector('.mddb-print-root')).toBeNull();
  });

  test('unmounting before the dialog closes drops the listener', () => {
    vi.spyOn(window, 'print').mockImplementation(() => {});
    const onDone = vi.fn();
    const { unmount } = render(<DocumentPrint document={doc} onDone={onDone} />);
    unmount();
    window.dispatchEvent(new Event('afterprint'));
    expect(onDone).not.toHaveBeenCalled();
  });
});
