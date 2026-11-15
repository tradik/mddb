// @vitest-environment jsdom
// RevisionPreview (FE-013): a revision's changes against the current version
// (the default view), its rendered preview, and Restore.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import { format } from 'date-fns';

vi.mock('./MarkdownView', () => ({ default: ({ children }) => <div data-testid="markdown">{children}</div> }));

const { default: RevisionPreview } = await import('./RevisionPreview');

afterEach(cleanup);

const revision = { timestamp: 1_700_000_000, contentMd: '# T\nold line', meta: { tags: ['a', 'b'], status: ['draft'] } };
const current = { key: 'k', lang: 'en', contentMd: '# T\nnew line', meta: { tags: ['a', 'b'] } };

describe('RevisionPreview', () => {
  test('shows the changes from the revision to the current version first', () => {
    render(<RevisionPreview revision={revision} current={current} restoring={false} onRestore={() => {}} />);
    expect(screen.getByRole('heading', { name: format(new Date(revision.timestamp * 1000), 'PPpp') })).toBeTruthy();
    expect(screen.getByText('Content length: 12 characters')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Changes' }).getAttribute('aria-pressed')).toBe('true');
    expect(screen.getByText('Metadata, this revision → current: 1 key differ')).toBeTruthy();
    expect(screen.getByText('removed')).toBeTruthy();
    expect(screen.getByText('Content, this revision → current: 1 line added, 1 removed')).toBeTruthy();
    expect(screen.getByText('old line')).toBeTruthy();
    expect(screen.getByText('new line')).toBeTruthy();
  });

  test('Preview renders the revision with its metadata', () => {
    render(<RevisionPreview revision={revision} current={current} restoring={false} onRestore={() => {}} />);
    fireEvent.click(screen.getByRole('button', { name: 'Preview' }));
    expect(screen.getByRole('button', { name: 'Preview' }).getAttribute('aria-pressed')).toBe('true');
    expect(screen.getByText('a, b')).toBeTruthy();
    expect(screen.getByText('draft')).toBeTruthy();
    expect(screen.getByTestId('markdown').textContent).toBe('# T\nold line');
  });

  test('an empty revision previews as (empty) and without metadata', () => {
    render(<RevisionPreview revision={{ timestamp: 1 }} current={{}} restoring={false} onRestore={() => {}} />);
    expect(screen.getByText('Content length: 0 characters')).toBeTruthy();
    expect(screen.getByText('No metadata change.')).toBeTruthy();
    expect(screen.getByText('No change.')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Preview' }));
    expect(screen.queryByText('Metadata')).toBeNull();
    expect(screen.getByTestId('markdown').textContent).toBe('(empty)');
  });

  test('an empty metadata map shows no metadata box', () => {
    render(<RevisionPreview revision={{ timestamp: 1, meta: {}, contentMd: 'x' }} current={current} restoring={false} onRestore={() => {}} />);
    fireEvent.click(screen.getByRole('button', { name: 'Preview' }));
    expect(screen.queryByText('Metadata')).toBeNull();
  });

  test('Restore passes the timestamp; it is disabled while restoring', () => {
    const onRestore = vi.fn();
    const { rerender } = render(<RevisionPreview revision={revision} current={current} restoring={false} onRestore={onRestore} />);
    fireEvent.click(screen.getByRole('button', { name: 'Restore this version' }));
    expect(onRestore).toHaveBeenCalledWith(revision.timestamp);
    rerender(<RevisionPreview revision={revision} current={current} restoring onRestore={onRestore} />);
    expect(screen.getByRole('button', { name: 'Restoring...' }).disabled).toBe(true);
  });
});
