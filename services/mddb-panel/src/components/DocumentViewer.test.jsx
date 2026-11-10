// @vitest-environment jsdom
// DocumentViewer: nothing without a document, the loading state before its
// content arrives, header/metadata/content/footer with their fallbacks, the
// code renderer's three shapes, Copy, the editor and revision overlays, the
// delete dialog, hover colours and the Markdown error boundary's raw view.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';
import { format } from 'date-fns';

vi.mock('../lib/mddb-client', () => ({ default: { deleteDocument: vi.fn() } }));
vi.mock('./DocumentEditor', () => ({
  default: ({ document, onClose, onSave }) => <div>editor {document.key}<button onClick={onClose}>close editor</button><button onClick={onSave}>save</button></div>,
}));
vi.mock('./RevisionHistory', () => ({
  default: ({ document, onClose }) => <div>revisions {document.key}<button onClick={onClose}>close revisions</button></div>,
}));
vi.mock('./ImagePreview', () => ({ default: ({ docKey }) => <div>image preview {docKey}</div> }));
vi.mock('react-syntax-highlighter', () => ({ Prism: ({ children, language }) => <pre data-language={language}>{children}</pre> }));
vi.mock('react-syntax-highlighter/dist/esm/styles/prism', () => ({ vscDarkPlus: {} }));
// A stand-in for react-markdown hands the `code` renderer the three shapes it
// handles (fenced with a language, fenced without one, inline) and throws on
// a marker so the error boundary can be exercised.
vi.mock('react-markdown', () => ({
  default: ({ children, components: { code: Code } }) => {
    if (children.includes('<<throw>>')) throw new Error('bad markdown');
    return (
      <div>
        <Code node={{}} className="language-js">{'const a = 1;\n'}</Code>
        <Code node={{}}>plain block</Code>
        <Code node={{}} inline>inline code</Code>
        <p>{children}</p>
      </div>
    );
  },
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: DocumentViewer } = await import('./DocumentViewer');

const initialStore = useStore.getState();
const doc = {
  collection: 'docs', key: 'guide', lang: 'en', contentMd: '# Guide', revision: 3,
  updatedAt: 1_700_000_000, addedAt: 1_690_000_000, meta: { tags: ['a', 'b'], author: 'Ann' },
};

/** Renders the viewer for `document` in the store. */
function renderWith(document) {
  useStore.setState({ currentDocument: document });
  return render(<DocumentViewer />);
}

/** The header's delete and close buttons. */
const deleteButton = () => screen.getByTitle('Delete document');
const closeButton = () => deleteButton().nextElementSibling;

beforeEach(() => {
  useStore.setState(initialStore, true);
  vi.mocked(mddbClient.deleteDocument).mockReset();
  vi.spyOn(console, 'error').mockImplementation(() => {});
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe('DocumentViewer', () => {
  test('renders nothing without a document', () => {
    expect(renderWith(null).container.firstChild).toBeNull();
  });

  test('shows the loading state until the content arrives', () => {
    renderWith({ key: 'guide', lang: 'pl' });
    expect(screen.getByText('Loading document content...')).toBeTruthy();
    expect(screen.getByText('Document: guide (pl)')).toBeTruthy();
  });

  test('shows the header, metadata, image preview, content and footer', () => {
    renderWith(doc);
    expect(screen.getByRole('heading', { name: 'guide', level: 3 })).toBeTruthy();
    expect(screen.getByText('en')).toBeTruthy();
    expect(screen.getByText(format(new Date(doc.updatedAt * 1000), 'PPpp'))).toBeTruthy();
    expect(screen.getByText('Metadata')).toBeTruthy();
    expect(screen.getByText('a, b')).toBeTruthy();
    expect(screen.getByText('Ann')).toBeTruthy();
    expect(screen.getByText('image preview guide')).toBeTruthy();
    expect(screen.getByText('# Guide')).toBeTruthy();
    expect(screen.getByText('docs')).toBeTruthy();
    expect(screen.getByText(format(new Date(doc.addedAt * 1000), 'PP'))).toBeTruthy();
    expect(screen.getByRole('button', { name: /Revision:\s*3/ })).toBeTruthy();
  });

  test('highlights a fenced block with a language and styles the other two shapes', () => {
    const { container } = renderWith(doc);
    const highlighted = container.querySelector('pre[data-language="js"]');
    expect(highlighted.textContent).toBe('const a = 1;');
    expect(screen.getByText('plain block').style.display).toBe('block');
    expect(screen.getByText('inline code').style.color).toBe('rgb(220, 38, 38)');
  });

  test.each([undefined, {}])('falls back when dates, collection, revision are missing and meta is %o', (meta) => {
    renderWith({ key: 'k', lang: 'en', contentMd: 'x', meta });
    expect(screen.queryByText('Metadata')).toBeNull();
    expect(screen.getAllByText('N/A')).toHaveLength(2);
    expect(screen.getByRole('button', { name: /Revision:\s*0/ })).toBeTruthy();
    expect(screen.queryByText(/,.*at/)).toBeNull();
  });

  test('Copy writes the Markdown and says Copied! for two seconds', () => {
    vi.useFakeTimers();
    const writeText = vi.fn();
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    renderWith(doc);
    fireEvent.click(screen.getByRole('button', { name: 'Copy' }));
    expect(writeText).toHaveBeenCalledWith('# Guide');
    expect(screen.getByRole('button', { name: 'Copied!' })).toBeTruthy();
    act(() => vi.advanceTimersByTime(2000));
    expect(screen.getByRole('button', { name: 'Copy' })).toBeTruthy();
    delete navigator.clipboard;
  });

  test('Edit opens the editor; saving or closing hides it', () => {
    renderWith(doc);
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    expect(screen.getByText('editor guide')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'save' }));
    expect(screen.queryByText('editor guide')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    fireEvent.click(screen.getByRole('button', { name: 'close editor' }));
    expect(screen.queryByText('editor guide')).toBeNull();
  });

  test('the revision link opens the history and its close hides it', () => {
    renderWith(doc);
    fireEvent.click(screen.getByRole('button', { name: /Revision:\s*3/ }));
    expect(screen.getByText('revisions guide')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'close revisions' }));
    expect(screen.queryByText('revisions guide')).toBeNull();
  });

  test('the close button clears the document; hovering recolours the icon buttons', () => {
    renderWith(doc);
    fireEvent.mouseOver(deleteButton());
    expect(deleteButton().style.color).toBe('rgb(220, 38, 38)');
    fireEvent.mouseOut(deleteButton());
    expect(deleteButton().style.color).toBe('rgb(156, 163, 175)');
    fireEvent.mouseOver(closeButton());
    expect(closeButton().style.backgroundColor).toBe('rgb(243, 244, 246)');
    fireEvent.mouseOut(closeButton());
    expect(closeButton().style.backgroundColor).toBe('transparent');
    fireEvent.click(closeButton());
    expect(useStore.getState().currentDocument).toBeNull();
  });

  test('Delete asks first; Cancel keeps the document', () => {
    renderWith(doc);
    fireEvent.click(deleteButton());
    expect(screen.getByText('Delete Document')).toBeTruthy();
    expect(screen.getByText(/Are you sure you want to delete "guide" \(en\)/)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByText('Delete Document')).toBeNull();
    expect(mddbClient.deleteDocument).not.toHaveBeenCalled();
  });

  test('a confirmed delete removes the document and closes the viewer', async () => {
    vi.mocked(mddbClient.deleteDocument).mockResolvedValue({});
    const { container } = renderWith(doc);
    fireEvent.click(deleteButton());
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Delete' })));
    expect(mddbClient.deleteDocument).toHaveBeenCalledWith({ collection: 'docs', key: 'guide', lang: 'en' });
    expect(useStore.getState().currentDocument).toBeNull();
    expect(container.firstChild).toBeNull();
  });

  test('a failed delete logs the error and keeps the dialog open', async () => {
    vi.mocked(mddbClient.deleteDocument).mockRejectedValue(new Error('API Error (403): forbidden'));
    renderWith(doc);
    fireEvent.click(deleteButton());
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Delete' })));
    expect(console.error).toHaveBeenCalledWith('Failed to delete document:', expect.any(Error));
    expect(screen.getByText('Delete Document')).toBeTruthy();
    expect(useStore.getState().currentDocument).toEqual(doc);
  });

  test('a rendering error offers the raw content and a way back', () => {
    renderWith({ ...doc, contentMd: 'broken <<throw>> text' });
    expect(screen.getByText('Markdown Rendering Error')).toBeTruthy();
    expect(screen.getByText('Error: bad markdown')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Show Raw Content' }));
    expect(screen.getByText('Raw Content')).toBeTruthy();
    expect(screen.getByText('broken <<throw>> text').tagName).toBe('PRE');
    fireEvent.click(screen.getByRole('button', { name: 'Back to Markdown' }));
    expect(screen.getByText('Markdown Rendering Error')).toBeTruthy();
  });
});
