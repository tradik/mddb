// @vitest-environment jsdom
// FE-019 notices: DraftNotice in the viewer (a stored draft that differs
// from the document: open it in the editor or discard it here),
// DraftRestoreBanner in the editor (restore / discard, a changed server
// revision, the draft's changes as a diff) and RevisionConflictNotice after
// a save refused with 409.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import DraftNotice from './DraftNotice';
import DraftRestoreBanner from './DraftRestoreBanner';
import RevisionConflictNotice from './RevisionConflictNotice';
import { readDraft, writeDraft } from '../lib/drafts';

const doc = { collection: 'blog', key: 'hello', lang: 'en', contentMd: 'line one', meta: {}, revision: 'r2' };

beforeEach(() => localStorage.clear());
afterEach(cleanup);

describe('DraftNotice', () => {
  test('nothing without a draft, or with a draft equal to the document', () => {
    const { container, rerender } = render(<DraftNotice document={doc} onOpen={vi.fn()} />);
    expect(container.textContent).toBe('');
    writeDraft(doc, { contentMd: 'line one', meta: {}, baseRevision: 'r2' });
    rerender(<DraftNotice document={doc} onOpen={vi.fn()} />);
    expect(container.textContent).toBe('');
  });

  test('a differing draft: open it in the editor, or discard it', () => {
    writeDraft(doc, { contentMd: 'edited', meta: {}, baseRevision: 'r2' }, 1_700_000_000_000);
    const onOpen = vi.fn();
    render(<DraftNotice document={doc} onOpen={onOpen} />);
    expect(screen.getByRole('status').textContent).toContain(new Date(1_700_000_000_000).toLocaleString());
    fireEvent.click(screen.getByRole('button', { name: 'Open in editor' }));
    expect(onOpen).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole('button', { name: 'Discard draft' }));
    expect(readDraft(doc)).toBeNull();
    expect(screen.queryByRole('status')).toBeNull();
  });
});

describe('DraftRestoreBanner', () => {
  const draft = { contentMd: 'line one\nline two', meta: {}, baseRevision: 'r2', savedAt: 1_700_000_000_000 };

  test('restore and discard; autosave is said to wait', () => {
    const onRestore = vi.fn();
    const onDiscard = vi.fn();
    render(<DraftRestoreBanner draft={draft} document={doc} onRestore={onRestore} onDiscard={onDiscard} />);
    expect(screen.getByRole('alert').textContent).toContain('Autosave is paused');
    expect(screen.queryByText(/changed on the server/)).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Restore draft' }));
    fireEvent.click(screen.getByRole('button', { name: 'Discard draft' }));
    expect(onRestore).toHaveBeenCalledTimes(1);
    expect(onDiscard).toHaveBeenCalledTimes(1);
  });

  test('warns when the document moved on since the draft began', () => {
    render(<DraftRestoreBanner draft={{ ...draft, baseRevision: 'r1' }} document={doc} onRestore={vi.fn()} onDiscard={vi.fn()} />);
    expect(screen.getByText(/revision r1 → r2/)).toBeTruthy();
  });

  test('no warning when either revision is unknown', () => {
    render(<DraftRestoreBanner draft={{ ...draft, baseRevision: '' }} document={{ ...doc, revision: undefined }} onRestore={vi.fn()} onDiscard={vi.fn()} />);
    expect(screen.queryByText(/changed on the server/)).toBeNull();
  });

  test('shows and hides the draft’s changes against the document', () => {
    render(<DraftRestoreBanner draft={draft} document={{ ...doc, contentMd: undefined }} onRestore={vi.fn()} onDiscard={vi.fn()} />);
    const toggle = screen.getByRole('button', { name: 'Show changes' });
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    fireEvent.click(toggle);
    expect(screen.getByText(/Draft against the document: 2 lines added/)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Hide changes' }));
    expect(screen.queryByText(/Draft against the document/)).toBeNull();
  });
});

describe('RevisionConflictNotice', () => {
  test('names both revisions and offers to overwrite', () => {
    const onOverwrite = vi.fn();
    render(<RevisionConflictNotice expected="r1" current="r3" busy={false} onOverwrite={onOverwrite} />);
    expect(screen.getByRole('alert').textContent).toContain('Someone saved revision r3 after you started editing revision r1.');
    fireEvent.click(screen.getByRole('button', { name: 'Save my version anyway' }));
    expect(onOverwrite).toHaveBeenCalledTimes(1);
  });

  test('a deleted document, and the busy state', () => {
    render(<RevisionConflictNotice expected="r1" current="" busy onOverwrite={vi.fn()} />);
    expect(screen.getByRole('alert').textContent).toContain('The document was deleted after you started editing revision r1.');
    expect(screen.getByRole('button', { name: 'Save my version anyway' }).disabled).toBe(true);
  });
});
