// @vitest-environment jsdom
// DocumentActions: the viewer's More menu — opening and closing it (button,
// Escape, a click elsewhere), and each action: Compare with…, Download HTML
// (and its failure), Print / PDF and Save as template.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';

vi.mock('../lib/document-export', () => ({ downloadDocumentHtml: vi.fn() }));
vi.mock('./DocumentCompare', () => ({ default: ({ document, onClose }) => <div>compare {document.key}<button onClick={onClose}>close compare</button></div> }));
vi.mock('./DocumentPrint', () => ({ default: ({ document, onDone }) => <div>print {document.key}<button onClick={onDone}>printed</button></div> }));
vi.mock('./SaveTemplateDialog', () => ({ default: ({ document, onClose }) => <div>template {document.key}<button onClick={onClose}>close template</button></div> }));

const { downloadDocumentHtml } = await import('../lib/document-export');
const { default: DocumentActions } = await import('./DocumentActions');

const doc = { collection: 'docs', key: 'guide', lang: 'en', contentMd: '# G' };

beforeEach(() => {
  vi.mocked(downloadDocumentHtml).mockReset();
});
afterEach(cleanup);

const more = () => screen.getByRole('button', { name: 'More' });
const item = (name) => screen.getByRole('button', { name });

function renderActions() {
  render(<div><DocumentActions document={doc} /><p>outside</p></div>);
}

describe('DocumentActions', () => {
  test('the button opens and closes the menu and says so', () => {
    renderActions();
    expect(more().getAttribute('aria-expanded')).toBe('false');
    fireEvent.click(more());
    expect(more().getAttribute('aria-expanded')).toBe('true');
    expect(document.getElementById(more().getAttribute('aria-controls'))).toBeTruthy();
    expect(screen.getAllByRole('listitem')).toHaveLength(4);
    fireEvent.click(more());
    expect(screen.queryByRole('list')).toBeNull();
  });

  test('Escape and a click elsewhere close it; other keys and clicks inside do not', () => {
    renderActions();
    fireEvent.click(more());
    fireEvent.keyDown(document, { key: 'a' });
    fireEvent.mouseDown(item('Print / PDF'));
    expect(screen.getByRole('list')).toBeTruthy();
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(screen.queryByRole('list')).toBeNull();
    fireEvent.click(more());
    fireEvent.mouseDown(screen.getByText('outside'));
    expect(screen.queryByRole('list')).toBeNull();
  });

  test('Compare with…, Print / PDF and Save as template open their overlays, which close again', () => {
    renderActions();
    for (const [label, shown, close] of [
      ['Compare with…', 'compare guide', 'close compare'],
      ['Print / PDF', 'print guide', 'printed'],
      ['Save as template', 'template guide', 'close template'],
    ]) {
      fireEvent.click(more());
      fireEvent.click(item(label));
      expect(screen.queryByRole('list')).toBeNull();
      expect(screen.getByText(shown)).toBeTruthy();
      fireEvent.click(screen.getByRole('button', { name: close }));
      expect(screen.queryByText(shown)).toBeNull();
    }
  });

  test('Download HTML saves the document; a failure is shown until the menu opens again', async () => {
    renderActions();
    fireEvent.click(more());
    await act(async () => fireEvent.click(item('Download HTML')));
    expect(downloadDocumentHtml).toHaveBeenCalledWith(doc);
    expect(screen.queryByRole('alert')).toBeNull();

    vi.mocked(downloadDocumentHtml).mockRejectedValue(new Error('disk full'));
    fireEvent.click(more());
    await act(async () => fireEvent.click(item('Download HTML')));
    expect(screen.getByRole('alert').textContent).toBe('Download failed: disk full');
    fireEvent.click(more());
    expect(screen.queryByRole('alert')).toBeNull();
  });
});
