// @vitest-environment jsdom
// DocumentEditor: metadata editing, saving (conditional on the revision the
// edit started from, FE-019) with the refreshed document put in the store,
// a refused save, a 409 revision conflict and the deliberate overwrite,
// Ctrl/Cmd+S from the editor (FE-016), and drafts — autosaved while typing,
// offered for restore or discard on opening, removed after a save.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { addDocument: vi.fn(), getDocument: vi.fn() } }));
// The Markdown editor has its own suite: a stand-in textarea, plus a button
// for its Ctrl/Cmd+S callback and the spelling scope it was given.
vi.mock('./MarkdownEditor', () => ({
  default: ({ value, onChange, onSave, spelling }) => (
    <div>
      <textarea aria-label="markdown" value={value} onChange={(e) => onChange(e.target.value)} />
      <button type="button" onClick={onSave}>editor save</button>
      <span>spelling {spelling.collection}/{spelling.lang}</span>
    </div>
  ),
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { readDraft, writeDraft } = await import('../lib/drafts');
const { DRAFT_DELAY_MS } = await import('../lib/use-draft');
const { default: DocumentEditor } = await import('./DocumentEditor');

const doc = { collection: 'blog', key: 'hello', lang: 'en', contentMd: '# Hi', meta: { author: ['Ann'], tags: 'x' }, revision: 'r1' };

function conflictError(currentRevision) {
  const err = new Error('API Error (409): revision conflict');
  err.status = 409;
  err.body = { error: 'revision conflict', expectedRevision: 'r1', currentRevision };
  return err;
}

beforeEach(() => {
  vi.useFakeTimers();
  localStorage.clear();
  mddbClient.addDocument.mockReset().mockResolvedValue({ revision: 'r2' });
  mddbClient.getDocument.mockReset().mockResolvedValue({ ...doc, contentMd: '# Saved', revision: 'r2' });
  useStore.setState({ currentDocument: null });
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

const markdown = () => screen.getByLabelText('markdown');
const saveButton = () => screen.getByRole('button', { name: /Save Changes|Saving/ });
const type = (text) => fireEvent.change(markdown(), { target: { value: text } });

function setup(props = {}) {
  const onClose = vi.fn();
  const onSave = vi.fn();
  const view = render(<DocumentEditor document={doc} onClose={onClose} onSave={onSave} {...props} />);
  return { onClose, onSave, ...view };
}

describe('DocumentEditor', () => {
  test('nothing without a document', () => {
    const { container } = render(<DocumentEditor document={null} />);
    expect(container.textContent).toBe('');
  });

  test('shows the document, its metadata and the spelling scope; X closes', () => {
    const { onClose } = setup();
    expect(screen.getByText('blog / hello (en)')).toBeTruthy();
    expect(markdown().value).toBe('# Hi');
    expect(screen.getByDisplayValue('Ann')).toBeTruthy();
    expect(screen.getByDisplayValue('x')).toBeTruthy();
    expect(screen.getByText('spelling blog/en')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Close editor' }));
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  test('metadata: add (button or Enter, both fields needed), change, remove', () => {
    setup();
    const keyInput = screen.getByPlaceholderText('Key (e.g., author)');
    const valueInput = screen.getByPlaceholderText('Value');
    fireEvent.click(screen.getByRole('button', { name: 'Add' }));
    fireEvent.change(keyInput, { target: { value: 'topic' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add' }));
    expect(screen.queryByDisplayValue('topic')).toBeTruthy(); // still only typed, not added
    fireEvent.change(valueInput, { target: { value: 'go' } });
    fireEvent.keyPress(valueInput, { key: 'a', code: 'KeyA', charCode: 97 });
    fireEvent.keyPress(valueInput, { key: 'Enter', code: 'Enter', charCode: 13 });
    expect(screen.getByDisplayValue('go')).toBeTruthy();
    expect(keyInput.value).toBe('');
    fireEvent.change(keyInput, { target: { value: 'lvl' } });
    fireEvent.change(valueInput, { target: { value: '2' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add' }));
    fireEvent.change(screen.getByDisplayValue('Ann'), { target: { value: 'Bob' } });
    fireEvent.click(screen.getAllByRole('button', { name: 'Remove' })[1]);
    expect(screen.queryByDisplayValue('x')).toBeNull();
    expect(screen.getByDisplayValue('Bob')).toBeTruthy();
  });

  test('saves conditional on the starting revision, refreshes the store, then calls onSave', async () => {
    let finish;
    mddbClient.addDocument.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
    const { onSave } = setup();
    type('# Changed');
    fireEvent.click(saveButton());
    expect(saveButton().textContent).toBe('Saving...');
    // A second save while one runs is ignored.
    fireEvent.click(screen.getByRole('button', { name: 'editor save' }));
    expect(mddbClient.addDocument).toHaveBeenCalledTimes(1);
    await act(async () => finish({ revision: 'r2' }));
    expect(mddbClient.addDocument).toHaveBeenCalledWith({
      collection: 'blog', key: 'hello', lang: 'en', meta: doc.meta, contentMd: '# Changed', expectedRevision: 'r1',
    });
    expect(useStore.getState().currentDocument).toMatchObject({ contentMd: '# Saved', revision: 'r2', collection: 'blog' });
    expect(screen.getByText('Document saved successfully!')).toBeTruthy();
    act(() => vi.advanceTimersByTime(1500));
    expect(onSave).toHaveBeenCalledTimes(1);
    expect(screen.queryByText('Document saved successfully!')).toBeNull();
    expect(readDraft(doc)).toBeNull();
  });

  test('Ctrl/Cmd+S in the editor saves; works without onSave', async () => {
    render(<DocumentEditor document={{ ...doc, revision: undefined, meta: undefined, contentMd: undefined }} onClose={vi.fn()} />);
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'editor save' })));
    expect(mddbClient.addDocument).toHaveBeenCalledWith(expect.objectContaining({ contentMd: '', meta: {}, expectedRevision: '' }));
    act(() => vi.advanceTimersByTime(1500));
  });

  test('a refused save shows the error', async () => {
    mddbClient.addDocument.mockRejectedValue(new Error('API Error (400): bad'));
    setup();
    await act(async () => fireEvent.click(saveButton()));
    expect(screen.getByText('Error saving document')).toBeTruthy();
    expect(screen.getByText('API Error (400): bad')).toBeTruthy();
    expect(screen.queryByRole('alert')).toBeNull();
  });

  test('a revision conflict is shown, not overwritten; overwriting is a deliberate second save', async () => {
    mddbClient.addDocument.mockRejectedValueOnce(conflictError('r3'));
    setup();
    type('# Mine');
    await act(async () => fireEvent.click(saveButton()));
    expect(screen.getByRole('alert').textContent).toContain('Someone saved revision r3 after you started editing revision r1.');
    expect(screen.queryByText('Error saving document')).toBeNull();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Save my version anyway' })));
    expect(mddbClient.addDocument).toHaveBeenLastCalledWith(expect.objectContaining({ contentMd: '# Mine', expectedRevision: 'r3' }));
    expect(screen.queryByText(/Someone saved/)).toBeNull();
  });

  test('a conflict without a body reads as a deleted document', async () => {
    const err = new Error('conflict');
    err.status = 409;
    mddbClient.addDocument.mockRejectedValueOnce(err);
    setup();
    await act(async () => fireEvent.click(saveButton()));
    expect(screen.getByRole('alert').textContent).toContain('The document was deleted');
  });

  test('typing autosaves a draft; going back to the stored text removes it', () => {
    setup();
    type('# Draft');
    act(() => vi.advanceTimersByTime(DRAFT_DELAY_MS));
    expect(readDraft(doc)).toMatchObject({ contentMd: '# Draft', baseRevision: 'r1', meta: doc.meta });
    type('# Hi');
    expect(readDraft(doc)).toBeNull();
  });

  test('closing keeps the last keystrokes in the draft', () => {
    const { unmount } = setup();
    type('# Typed just now');
    unmount();
    expect(readDraft(doc).contentMd).toBe('# Typed just now');
  });

  test('a draft found on opening: restore it, and the save uses its revision', async () => {
    writeDraft(doc, { contentMd: '# From draft', meta: { author: ['Zoe'] }, baseRevision: 'r0' });
    setup();
    expect(screen.getByText(/revision r0 → r1/)).toBeTruthy();
    // Autosave waits: typing does not overwrite the older draft.
    type('# Other');
    act(() => vi.advanceTimersByTime(DRAFT_DELAY_MS * 2));
    expect(readDraft(doc).contentMd).toBe('# From draft');
    fireEvent.click(screen.getByRole('button', { name: 'Restore draft' }));
    expect(markdown().value).toBe('# From draft');
    expect(screen.getByDisplayValue('Zoe')).toBeTruthy();
    mddbClient.addDocument.mockRejectedValueOnce(conflictError('r1'));
    await act(async () => fireEvent.click(saveButton()));
    expect(mddbClient.addDocument).toHaveBeenCalledWith(expect.objectContaining({ contentMd: '# From draft', expectedRevision: 'r0' }));
    expect(screen.getByText(/Someone saved revision r1/)).toBeTruthy();
    expect(readDraft(doc).contentMd).toBe('# From draft');
  });

  test('a draft found on opening: discard it', () => {
    writeDraft(doc, { contentMd: '# From draft', meta: {}, baseRevision: 'r1' });
    setup();
    fireEvent.click(screen.getByRole('button', { name: 'Discard draft' }));
    expect(readDraft(doc)).toBeNull();
    expect(markdown().value).toBe('# Hi');
    expect(screen.queryByRole('button', { name: 'Restore draft' })).toBeNull();
  });

  test('a stored draft equal to the document is not offered', () => {
    writeDraft(doc, { contentMd: '# Hi', meta: doc.meta, baseRevision: 'r1' });
    setup();
    expect(screen.queryByRole('button', { name: 'Restore draft' })).toBeNull();
  });

  test('a new document object resets the editor to it', () => {
    const { rerender } = setup();
    type('# Local');
    rerender(<DocumentEditor document={{ ...doc, contentMd: '# Newer', revision: 'r5' }} onClose={vi.fn()} />);
    expect(markdown().value).toBe('# Newer');
  });
});
