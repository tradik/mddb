// @vitest-environment jsdom
// AdminToolViews / AdminToolNav: the pages added in 2.19.0 — each view mode
// shows its page, any other mode nothing; the sidebar entries switch to them.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';

vi.mock('./MemoryPanel', () => ({ default: () => 'memory page' }));
vi.mock('./SchemasPanel', () => ({ default: () => 'schemas page' }));
vi.mock('./ImportPanel', () => ({ default: () => 'import page' }));

const { default: AdminToolViews } = await import('./AdminToolViews');
const { default: AdminToolNav } = await import('./AdminToolNav');

afterEach(cleanup);

describe('AdminToolViews', () => {
  test.each([
    ['memory', 'memory page'],
    ['schemas', 'schemas page'],
    ['import', 'import page'],
  ])('%s shows %s', (viewMode, label) => {
    const { container } = render(<AdminToolViews viewMode={viewMode} />);
    expect(container.textContent).toBe(label);
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
  });
});
