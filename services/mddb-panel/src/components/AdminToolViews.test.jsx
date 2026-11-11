// @vitest-environment jsdom
// AdminToolViews: the pages added in 2.19.0 — each view mode shows its page,
// any other mode nothing. Their menu entries are in nav-groups.js.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, cleanup } from '@testing-library/react';

vi.mock('./MemoryPanel', () => ({ default: () => 'memory page' }));
vi.mock('./SchemasPanel', () => ({ default: () => 'schemas page' }));
vi.mock('./ImportPanel', () => ({ default: () => 'import page' }));
vi.mock('./DuplicatesPanel', () => ({ default: () => 'duplicates page' }));
vi.mock('./AccountPanel', () => ({ default: () => 'account page' }));
vi.mock('./DocumentViewer', () => ({ default: () => ' + viewer' }));

const { default: AdminToolViews } = await import('./AdminToolViews');

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
