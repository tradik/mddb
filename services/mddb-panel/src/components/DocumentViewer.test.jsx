// @vitest-environment jsdom
// DocumentViewer: nothing without a document, the loading state before its
// content arrives, header/metadata/content/footer with their fallbacks, long
// values that must not widen the pane, Copy, the editor and revision overlays, the
// delete dialog, hover colours, the More menu (DocumentActions) and the
// Markdown error boundary's raw view.
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
vi.mock('./DocumentTools', () => ({ default: ({ document }) => <div>document tools {document.key}</div> }));
vi.mock('./DocumentActions', () => ({ default: ({ document }) => <div>document actions {document.key}</div> }));
// FE-019: the draft notice has its own suite; a stand-in offers its Open button.
vi.mock('./DraftNotice', () => ({ default: ({ document, onOpen }) => <button onClick={onOpen}>open draft of {document.key}</button> }));
// The Markdown itself renders through MarkdownView (its own suite); a
// stand-in throws on a marker so the error boundary can be exercised.
vi.mock('./MarkdownView', () => ({
  default: ({ children }) => {
    if (children.includes('<<throw>>')) throw new Error('bad markdown');
    return <p>{children}</p>;
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
    expect(screen.getByText('document tools guide')).toBeTruthy();
    expect(screen.getByText('document actions guide')).toBeTruthy();
    expect(screen.getByText('# Guide')).toBeTruthy();
    expect(screen.getByText('docs')).toBeTruthy();
    expect(screen.getByText(format(new Date(doc.addedAt * 1000), 'PP'))).toBeTruthy();
    expect(screen.getByRole('button', { name: /Revision:\s*3/ })).toBeTruthy();
  });

  test('long keys, meta values and collection names wrap or truncate instead of widening the pane (2.19.6)', () => {
    const long = 'https://example.com/' + 'a'.repeat(300);
    renderWith({ ...doc, key: 'k'.repeat(200), collection: 'c'.repeat(120), meta: { url: [long] } });
    expect(screen.getByRole('heading', { level: 3 }).style.overflowWrap).toBe('anywhere');
    expect(screen.getByText(long).style.overflowWrap).toBe('anywhere');
    expect(screen.getByText(long).style.minWidth).toBe('0px');
    expect(screen.getByRole('button', { name: 'Edit' }).parentElement.style.flexShrink).toBe('0');
    const collection = screen.getByText('c'.repeat(120)).parentElement;
    expect(collection.getAttribute('title')).toBe('c'.repeat(120));
    expect(collection.style.textOverflow).toBe('ellipsis');
    expect(screen.getByRole('button', { name: 'Close document' })).toBeTruthy();
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

  test('the draft notice opens the editor (FE-019)', () => {
    renderWith(doc);
    fireEvent.click(screen.getByRole('button', { name: 'open draft of guide' }));
    expect(screen.getByText('editor guide')).toBeTruthy();
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
