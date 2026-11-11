// @vitest-environment jsdom
// AdminToolViews / AdminToolNav: the pages added in 2.19.0 — each view mode
// shows its page, any other mode nothing; the sidebar entries switch to them.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';

vi.mock('./MemoryPanel', () => ({ default: () => 'memory page' }));
vi.mock('./SchemasPanel', () => ({ default: () => 'schemas page' }));
vi.mock('./ImportPanel', () => ({ default: () => 'import page' }));
vi.mock('./DuplicatesPanel', () => ({ default: () => 'duplicates page' }));
vi.mock('./AccountPanel', () => ({ default: () => 'account page' }));
vi.mock('./DocumentViewer', () => ({ default: () => ' + viewer' }));

const { default: AdminToolViews } = await import('./AdminToolViews');
const { default: AdminToolNav } = await import('./AdminToolNav');

afterEach(cleanup);

describe('AdminToolViews', () => {
  test.each([
    ['memory', 'memory page'],
    ['schemas', 'schemas page'],
    ['import', 'import page'],
    ['duplicates', 'duplicates page'],
    ['account', 'account page'],
  ])('%s shows %s', (viewMode, label) => {
    const { container } = render(<AdminToolViews viewMode={viewMode} />);
    expect(container.textContent).toBe(label);
  });

  test('the duplicates page shows the viewer beside it once a document is open; others do not', () => {
    const { container, rerender } = render(<AdminToolViews viewMode="duplicates" currentDocument={{ key: 'a' }} />);
    expect(container.textContent).toBe('duplicates page + viewer');
    rerender(<AdminToolViews viewMode="import" currentDocument={{ key: 'a' }} />);
    expect(container.textContent).toBe('import page');
  });

  test('another view mode shows nothing', () => {
    const { container } = render(<AdminToolViews viewMode="system" />);
    expect(container.textContent).toBe('');
  });
});

describe('AdminToolNav', () => {
  const NavButton = ({ mode, label }) => <button type="button" data-mode={mode}>{label}</button>;

  test('lists the pages', () => {
    render(<AdminToolNav NavButton={NavButton} />);
    expect(screen.getByRole('button', { name: 'Agent Memory' }).dataset.mode).toBe('memory');
    expect(screen.getByRole('button', { name: 'Collection Schemas' }).dataset.mode).toBe('schemas');
    expect(screen.getByRole('button', { name: 'Import' }).dataset.mode).toBe('import');
    expect(screen.getByRole('button', { name: 'Duplicates' }).dataset.mode).toBe('duplicates');
    expect(screen.queryByRole('button', { name: 'My Account' })).toBeNull();
  });

  test('My Account appears with authentication', () => {
    render(<AdminToolNav NavButton={NavButton} authEnabled />);
    expect(screen.getByRole('button', { name: 'My Account' }).dataset.mode).toBe('account');
  });
});
