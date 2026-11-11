// @vitest-environment jsdom
// CodeGraphCard: what depends on a code document and what it depends on
// (/v1/code-graph) — the request options, the edges table with line
// numbers, the truncation warning and the server's errors.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act, within } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { codeGraph: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { default: CodeGraphCard } = await import('./CodeGraphCard');

const apiError = (status, text) => Object.assign(new Error(`API Error (${status}): ${text}`), { status, body: { error: text } });
const show = () => act(async () => fireEvent.click(screen.getByRole('button', { name: 'Show graph' })));

beforeEach(() => mddbClient.codeGraph.mockReset());
afterEach(cleanup);

describe('CodeGraphCard', () => {
  test('asks with the options and lists the edges with their lines', async () => {
    mddbClient.codeGraph.mockResolvedValueOnce({
      root: 'theme/style.css',
      nodes: [{ key: 'theme/style.css' }, { key: 'theme/index.html' }],
      edges: [
        { from: 'theme/index.html', to: 'theme/style.css', kind: 'uses-selector', symbol: '.title', direction: 'in', lines: { fromLine: 1, toLine: 3 } },
        { from: 'theme/index.html', to: 'theme/style.css', kind: 'imports', symbol: 'theme/style.css', direction: 'out' },
      ],
      truncated: true,
    });
    render(<CodeGraphCard collection="theme" />);
    expect(screen.getByRole('button', { name: 'Show graph' }).disabled).toBe(true);
    fireEvent.change(screen.getByLabelText('Document key in theme'), { target: { value: ' theme/style.css ' } });
    fireEvent.change(screen.getByLabelText('Direction'), { target: { value: 'in' } });
    fireEvent.change(screen.getByLabelText('Hops'), { target: { value: '2' } });
    fireEvent.click(screen.getByLabelText('Line numbers'));
    await show();
    expect(mddbClient.codeGraph).toHaveBeenCalledWith({ collection: 'theme', key: 'theme/style.css', direction: 'in', depth: 2, lines: true });
    expect(screen.getByRole('status').textContent).toContain('2 files around theme/style.css, 2 edges.');
    expect(screen.getByText(/Cut at a depth or degree limit/)).toBeTruthy();
    const rows = within(screen.getByRole('table', { name: 'Code graph edges' })).getAllByRole('row');
    expect(rows[1].textContent).toBe('theme/index.html:1theme/style.css:3uses-selector.titlein');
    expect(rows[2].textContent).toBe('theme/index.htmltheme/style.cssimportstheme/style.cssout');
  });

  test('an isolated file, no collection, and a missing document', async () => {
    mddbClient.codeGraph.mockResolvedValueOnce({ root: 'x.go' }).mockRejectedValueOnce(apiError(404, 'document not found'));
    const { rerender } = render(<CodeGraphCard collection="" />);
    expect(screen.getByLabelText('Document key in …')).toBeTruthy();
    rerender(<CodeGraphCard collection="repo" />);
    fireEvent.change(screen.getByLabelText('Document key in repo'), { target: { value: 'x.go' } });
    await show();
    expect(screen.getByRole('status').textContent).toBe('0 files around x.go, 0 edges.');
    expect(screen.queryByRole('table')).toBeNull();
    await show();
    expect(screen.getByRole('alert').textContent).toBe('API Error (404): document not found');
  });
});
