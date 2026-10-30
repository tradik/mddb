// @vitest-environment jsdom
// DocumentList: the store-driven list of a collection's documents — loading,
// errors (corrupted data has its own message), the range header, pagination,
// the key filter, opening a document (full content loaded in the background),
// the delete confirmation, the upload modal's refresh paths, and a read-only
// corpus-pack collection (no upload, no delete, a notice).
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: { search: vi.fn(), getDocument: vi.fn(), deleteDocument: vi.fn(), getStats: vi.fn() },
}));
// The modals have suites of their own; these stand-ins expose their callbacks.
vi.mock('./UploadModal', () => ({
  default: ({ collection, onClose, onSuccess }) => (
    <div data-testid="upload-modal" data-collection={String(collection)}>
      <button onClick={onClose}>stub close</button>
      <button onClick={() => onSuccess('uploaded')}>stub success</button>
      <button onClick={() => onSuccess()}>stub success without collection</button>
    </div>
  ),
}));
vi.mock('./CommandModal', () => ({
  default: ({ isOpen, onClose, params }) => (isOpen ? (
    <div data-testid="command-modal" data-params={JSON.stringify(params)}>
      <button onClick={onClose}>stub command close</button>
    </div>
  ) : null),
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: DocumentList } = await import('./DocumentList');

const initialStore = useStore.getState();

// 2026-01-15 12:00 UTC.
const updatedAt = 1768478400;

const docs = [
  { key: 'alpha', lang: 'en', updatedAt, meta: { tag: ['a', 'b'], author: 'jo', extra: 'x' }, contentMd: '# Alpha' },
  { key: 'Beta', lang: 'pl', meta: {} },
  { key: 'gamma', lang: 'en' },
];

/** The document rows (outer buttons) in list order. */
function rows() {
  return screen.queryAllByRole('button').filter((b) => b.querySelector('h4'));
}

/** Renders the list for collection `docs` and waits for the documents. */
async function renderList(state = {}, result = { documents: docs, totalCount: docs.length }) {
  useStore.setState({ currentCollection: 'docs', ...state });
  vi.mocked(mddbClient.search).mockResolvedValue(result);
  const view = render(<DocumentList />);
  await waitFor(() => expect(useStore.getState().documentsLoading).toBe(false));
  await waitFor(() => expect(mddbClient.search).toHaveBeenCalled());
  return view;
}

beforeEach(() => {
  useStore.setState(initialStore, true);
  for (const fn of Object.values(mddbClient)) vi.mocked(fn).mockReset();
  vi.spyOn(console, 'error').mockImplementation(() => {});
});
afterEach(cleanup);

describe('DocumentList loading', () => {
  test('shows the loading state from the store', () => {
    useStore.setState({ documentsLoading: true });
    render(<DocumentList />);
    expect(screen.getByText('Loading documents...')).toBeTruthy();
  });

  test('does not search without a collection', () => {
    render(<DocumentList />);
    expect(mddbClient.search).not.toHaveBeenCalled();
    expect(screen.getByText('0 Documents')).toBeTruthy();
    expect(screen.getByText('No documents found')).toBeTruthy();
  });

  test('renders when called without props', () => {
    // React always passes a props object; this covers the `= {}` default.
    function Bare() {
      return DocumentList();
    }
    render(<Bare />);
    expect(screen.getByText('0 Documents')).toBeTruthy();
  });

  test('searches the collection with the store filters and lists its documents', async () => {
    await renderList({ filters: { tag: ['a'] }, sortBy: 'updatedAt', sortAsc: false, limit: 50 });
    expect(mddbClient.search).toHaveBeenLastCalledWith({
      collection: 'docs', filterMeta: { tag: ['a'] }, sort: 'updatedAt', asc: false, limit: 50, offset: 0,
    });
    expect(screen.getByText('1–3 of 3')).toBeTruthy();
    expect(rows().map((r) => r.querySelector('h4').textContent.trim())).toEqual(['alpha', 'Beta', 'gamma']);
    expect(useStore.getState().documents.every((d) => d.collection === 'docs')).toBe(true);

    const [alpha, beta, gamma] = rows();
    expect(alpha.textContent).toContain('Jan 15, 2026');
    expect(alpha.textContent).toContain('3 tags');
    expect(alpha.textContent).toContain('tag: a, b');
    expect(alpha.textContent).toContain('author: jo');
    expect(alpha.textContent).not.toContain('extra: x');
    expect(beta.textContent).toContain('N/A');
    expect(beta.textContent).not.toContain('tags');
    expect(gamma.textContent).not.toContain('tags');
    expect(screen.queryByText(/^Page /)).toBeNull();
  });

  test.each([
    ['any error shows its message', 'API Error (500): down', 'API Error (500): down'],
    ['a JSON parse error says the data is corrupted', 'Unexpected token: invalid character x', 'Collection contains corrupted data. This collection may need to be recreated.'],
  ])('%s', async (_label, message, shown) => {
    useStore.setState({ currentCollection: 'docs', documents: docs });
    vi.mocked(mddbClient.search).mockRejectedValue(new Error(message));
    render(<DocumentList />);
    expect(await screen.findByText('Error loading documents')).toBeTruthy();
    expect(screen.getByText(shown)).toBeTruthy();
    expect(useStore.getState().documents).toEqual([]);
    expect(console.error).toHaveBeenCalledWith('Failed to load documents:', expect.any(Error));
  });

  test('Refresh searches again', async () => {
    await renderList();
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    await waitFor(() => expect(mddbClient.search).toHaveBeenCalledTimes(2));
  });

  test('a new SSE refresh key searches again', async () => {
    const { rerender } = await renderList();
    rerender(<DocumentList sseRefreshKey={1} />);
    await waitFor(() => expect(mddbClient.search).toHaveBeenCalledTimes(2));
  });
});

describe('DocumentList pagination and filter', () => {
  test('pages forward and back through the results', async () => {
    const page = { documents: docs.slice(0, 2), totalCount: 5 };
    await renderList({ limit: 2 }, page);
    expect(screen.getByText('1–2 of 5')).toBeTruthy();
    expect(screen.getByText('Page 1 of 3')).toBeTruthy();
    const pager = screen.getByText('Page 1 of 3').nextElementSibling;
    const [prev, next] = pager.querySelectorAll('button');
    expect(prev.disabled).toBe(true);
    expect(next.disabled).toBe(false);

    fireEvent.click(next);
    expect(await screen.findByText('Page 2 of 3')).toBeTruthy();
    expect(mddbClient.search).toHaveBeenLastCalledWith(expect.objectContaining({ offset: 2, limit: 2 }));
    expect(screen.getByText('3–4 of 5')).toBeTruthy();

    fireEvent.click(screen.getByText('Page 2 of 3').nextElementSibling.querySelector('button:last-child'));
    expect(await screen.findByText('Page 3 of 3')).toBeTruthy();
    expect(screen.getByText('5–5 of 5')).toBeTruthy();
    const [prev3, next3] = screen.getByText('Page 3 of 3').nextElementSibling.querySelectorAll('button');
    expect(next3.disabled).toBe(true);

    fireEvent.click(prev3);
    expect(await screen.findByText('Page 2 of 3')).toBeTruthy();
    expect(mddbClient.search).toHaveBeenLastCalledWith(expect.objectContaining({ offset: 2 }));
  });

  test('the key filter is case-insensitive and empties to all', async () => {
    await renderList();
    const filter = screen.getByPlaceholderText('Filter by key...');
    fireEvent.change(filter, { target: { value: 'BET' } });
    expect(rows().map((r) => r.querySelector('h4').textContent.trim())).toEqual(['Beta']);
    fireEvent.change(filter, { target: { value: '' } });
    expect(rows()).toHaveLength(3);
  });
});

describe('DocumentList opening a document', () => {
  test('shows the listed content at once, then the full document', async () => {
    let resolve;
    vi.mocked(mddbClient.getDocument).mockReturnValue(new Promise((r) => { resolve = r; }));
    await renderList();
    fireEvent.click(rows()[2]);
    expect(useStore.getState().currentDocument).toEqual({ ...docs[2], collection: 'docs', contentMd: 'Loading content...' });
    expect(mddbClient.getDocument).toHaveBeenCalledWith({ collection: 'docs', key: 'gamma', lang: 'en' });
    expect(rows()[2].closest('li').className).toContain('bg-blue-50');
    expect(rows()[0].closest('li').className).not.toContain('bg-blue-50');

    resolve({ key: 'gamma', lang: 'en', contentMd: '# Gamma full' });
    await waitFor(() => expect(useStore.getState().currentDocument.contentMd).toBe('# Gamma full'));
    expect(useStore.getState().currentDocument.collection).toBe('docs');
  });

  test.each([
    ['the same content', { key: 'alpha', lang: 'en', contentMd: '# Alpha', extra: true }],
    ['no content', { key: 'alpha', lang: 'en' }],
  ])('keeps the listed document when the full one has %s', async (_label, full) => {
    vi.mocked(mddbClient.getDocument).mockResolvedValue(full);
    await renderList();
    fireEvent.click(rows()[0]);
    await waitFor(() => expect(mddbClient.getDocument).toHaveBeenCalled());
    await Promise.resolve();
    expect(useStore.getState().currentDocument).toEqual({ ...docs[0], collection: 'docs' });
  });

  test('a failed load says so in the document content', async () => {
    vi.mocked(mddbClient.getDocument).mockRejectedValue(new Error('timeout'));
    await renderList();
    fireEvent.click(rows()[1]);
    await waitFor(() => expect(useStore.getState().currentDocument.contentMd)
      .toBe('Error loading document content: timeout. Please try again.'));
    expect(console.error).toHaveBeenCalledWith('Failed to load full document content:', expect.any(Error));
  });
});

describe('DocumentList row structure', () => {
  test('no button sits inside another: the row opens, the delete beside it is its own control', async () => {
    vi.mocked(mddbClient.getDocument).mockResolvedValue({ key: 'alpha', lang: 'en' });
    const { container } = await renderList();
    expect(container.querySelectorAll('button button')).toHaveLength(0);
    const del = screen.getByRole('button', { name: 'Delete alpha (en)' });
    expect(del.closest('li')).toBe(rows()[0].closest('li'));
    expect(screen.getAllByRole('listitem')).toHaveLength(3);

    // The row is a native button: focusable, and Enter/Space activate it.
    expect(rows()[0].tagName).toBe('BUTTON');
    expect(rows()[0].getAttribute('aria-current')).toBeNull();
    fireEvent.click(rows()[0]);
    await waitFor(() => expect(rows()[0].getAttribute('aria-current')).toBe('true'));
    expect(rows()[0].closest('li').className).toContain('bg-blue-50');

    // Delete asks first and does not open the document.
    vi.mocked(mddbClient.getDocument).mockClear();
    fireEvent.click(screen.getByRole('button', { name: 'Delete gamma (en)' }));
    expect(mddbClient.getDocument).not.toHaveBeenCalled();
    expect(screen.getByText('Are you sure you want to delete "gamma" (en)? This action cannot be undone.')).toBeTruthy();
  });
});

describe('DocumentList delete', () => {
  test('asks first; Cancel keeps the document', async () => {
    await renderList();
    fireEvent.click(screen.getAllByTitle('Delete document')[0]);
    expect(mddbClient.getDocument).not.toHaveBeenCalled();
    expect(screen.getByText('Are you sure you want to delete "alpha" (en)? This action cannot be undone.')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByText('Delete Document')).toBeNull();
    expect(mddbClient.deleteDocument).not.toHaveBeenCalled();
  });

  test('Delete removes the document and closes the dialog', async () => {
    vi.mocked(mddbClient.deleteDocument).mockResolvedValue({});
    await renderList();
    fireEvent.click(screen.getAllByTitle('Delete document')[1]);
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(screen.queryByText('Delete Document')).toBeNull());
    expect(mddbClient.deleteDocument).toHaveBeenCalledWith({ collection: 'docs', key: 'Beta', lang: 'pl' });
    expect(rows().map((r) => r.querySelector('h4').textContent.trim())).toEqual(['alpha', 'gamma']);
  });

  test('a failed delete keeps the dialog open', async () => {
    vi.mocked(mddbClient.deleteDocument).mockRejectedValue(new Error('403 READ_ONLY'));
    await renderList();
    fireEvent.click(screen.getAllByTitle('Delete document')[0]);
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(console.error).toHaveBeenCalledWith('Failed to delete document:', expect.any(Error)));
    expect(screen.getByText('Delete Document')).toBeTruthy();
    expect(rows()).toHaveLength(3);
  });
});

describe('DocumentList modals', () => {
  test('Command opens the command modal with the search params, and closes it', async () => {
    await renderList({ limit: 10 });
    expect(screen.queryByTestId('command-modal')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Command' }));
    const modal = screen.getByTestId('command-modal');
    expect(JSON.parse(modal.dataset.params)).toEqual({ collection: 'docs', filterMeta: {}, sort: initialStore.sortBy, asc: initialStore.sortAsc, limit: 10, offset: 0 });
    fireEvent.click(screen.getByRole('button', { name: 'stub command close' }));
    expect(screen.queryByTestId('command-modal')).toBeNull();
  });

  test('Upload opens the modal for the collection, and closes it', async () => {
    await renderList();
    fireEvent.click(screen.getByRole('button', { name: 'Upload' }));
    expect(screen.getByTestId('upload-modal').dataset.collection).toBe('docs');
    fireEvent.click(screen.getByRole('button', { name: 'stub close' }));
    expect(screen.queryByTestId('upload-modal')).toBeNull();
  });

  test('a successful upload refreshes stats and the documents', async () => {
    vi.mocked(mddbClient.getStats).mockResolvedValue({ collections: { docs: 4 } });
    await renderList();
    fireEvent.click(screen.getByRole('button', { name: 'Upload' }));
    fireEvent.click(screen.getByRole('button', { name: 'stub success' }));
    expect(screen.queryByTestId('upload-modal')).toBeNull();
    await waitFor(() => expect(mddbClient.search).toHaveBeenCalledTimes(2));
    expect(useStore.getState().stats).toEqual({ collections: { docs: 4 } });
    expect(useStore.getState().currentCollection).toBe('docs');
  });

  test('an upload with no collection selected selects the uploaded one', async () => {
    vi.mocked(mddbClient.getStats).mockResolvedValue({ collections: {} });
    render(<DocumentList />);
    fireEvent.click(screen.getByRole('button', { name: 'Upload' }));
    fireEvent.click(screen.getByRole('button', { name: 'stub success' }));
    await waitFor(() => expect(useStore.getState().currentCollection).toBe('uploaded'));
  });

  test('an upload with neither collection reloads the (empty) list', async () => {
    vi.mocked(mddbClient.getStats).mockResolvedValue({});
    vi.mocked(mddbClient.search).mockResolvedValue({ documents: [], totalCount: 0 });
    render(<DocumentList />);
    fireEvent.click(screen.getByRole('button', { name: 'Upload' }));
    fireEvent.click(screen.getByRole('button', { name: 'stub success without collection' }));
    await waitFor(() => expect(mddbClient.search).toHaveBeenCalledWith(expect.objectContaining({ collection: null })));
    expect(useStore.getState().currentCollection).toBeNull();
  });

  test('a failed stats refresh still reloads the documents', async () => {
    vi.mocked(mddbClient.getStats).mockRejectedValue(new Error('stats down'));
    await renderList();
    fireEvent.click(screen.getByRole('button', { name: 'Upload' }));
    fireEvent.click(screen.getByRole('button', { name: 'stub success' }));
    await waitFor(() => expect(mddbClient.search).toHaveBeenCalledTimes(2));
    expect(console.error).toHaveBeenCalledWith('Failed to refresh stats:', expect.any(Error));
  });
});

describe('DocumentList read-only collection', () => {
  test('disables Upload, hides delete and shows the compact notice', async () => {
    await renderList({
      collectionConfigs: { docs: { readOnly: true, corpus: { name: 'gdpr', version: '2', license: 'MIT' } } },
    });
    const upload = screen.getByRole('button', { name: 'Upload' });
    expect(upload.disabled).toBe(true);
    expect(upload.title).toBe('Read-only collection: uploads are refused');
    expect(screen.queryAllByTitle('Delete document')).toHaveLength(0);
    const note = screen.getByRole('note');
    expect(note.textContent).toContain('installed from corpus pack gdpr v2 (MIT)');
    expect(note.className).toContain('p-2');
  });

  test('a writable configured collection keeps its controls', async () => {
    await renderList({ collectionConfigs: { docs: { readOnly: false } } });
    expect(screen.getByRole('button', { name: 'Upload' }).disabled).toBe(false);
    expect(screen.getByRole('button', { name: 'Upload' }).title).toBe('');
    expect(screen.getAllByTitle('Delete document')).toHaveLength(3);
    expect(screen.queryByRole('note')).toBeNull();
  });

  test('a store without collection configs is writable', async () => {
    await renderList({ collectionConfigs: undefined });
    expect(screen.getByRole('button', { name: 'Upload' }).disabled).toBe(false);
  });
});
