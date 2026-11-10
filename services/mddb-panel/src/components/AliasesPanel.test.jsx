// @vitest-environment jsdom
// AliasesPanel (GO-044): lists aliases, creates or moves one, deletes one
// after a confirm, and shows the server's refusals.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: { listAliases: vi.fn(), setAlias: vi.fn(), deleteAlias: vi.fn() },
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: AliasesPanel } = await import('./AliasesPanel');

const initialStore = useStore.getState();

beforeEach(() => {
  useStore.setState(initialStore, true);
  useStore.setState({ stats: { collections: [{ name: 'docs-v1' }, { name: 'docs-v2' }] } });
  for (const fn of Object.values(mddbClient)) vi.mocked(fn).mockReset();
  vi.mocked(mddbClient.listAliases).mockResolvedValue({ aliases: [{ alias: 'docs', collection: 'docs-v1' }] });
});
afterEach(cleanup);

const fill = (alias, collection) => {
  fireEvent.change(screen.getByLabelText('Alias'), { target: { value: alias } });
  fireEvent.change(screen.getByLabelText('Collection'), { target: { value: collection } });
};

describe('AliasesPanel', () => {
  test('lists aliases and offers collections', async () => {
    const { container } = render(<AliasesPanel />);
    expect(await screen.findByText('docs')).toBeTruthy();
    expect(screen.getByText('docs-v1')).toBeTruthy();
    expect(container.querySelectorAll('datalist option')).toHaveLength(2);
  });

  test.each([[{ aliases: [] }], [{}]])('no aliases (%o)', async (reply) => {
    vi.mocked(mddbClient.listAliases).mockResolvedValue(reply);
    useStore.setState({ stats: null });
    render(<AliasesPanel />);
    expect(await screen.findByText('No aliases.')).toBeTruthy();
  });

  test('a failed listing is shown', async () => {
    vi.mocked(mddbClient.listAliases).mockRejectedValue(new Error('API Error (403): forbidden'));
    render(<AliasesPanel />);
    expect((await screen.findByRole('alert')).textContent).toBe('API Error (403): forbidden');
  });

  test('creates a new alias, trimmed, and reloads', async () => {
    vi.mocked(mddbClient.setAlias).mockResolvedValue({});
    render(<AliasesPanel />);
    await screen.findByText('docs');
    const save = screen.getByRole('button', { name: 'Save alias' });
    expect(save.disabled).toBe(true);
    fill(' kb ', ' docs-v2 ');
    fireEvent.click(save);
    expect((await screen.findByRole('status')).textContent).toBe('Created alias kb → docs-v2.');
    expect(mddbClient.setAlias).toHaveBeenCalledWith('kb', 'docs-v2');
    expect(mddbClient.listAliases).toHaveBeenCalledTimes(2);
    expect(screen.getByLabelText('Alias').value).toBe('');
  });

  test('moves an existing alias', async () => {
    vi.mocked(mddbClient.setAlias).mockResolvedValue({});
    render(<AliasesPanel />);
    await screen.findByText('docs');
    fill('docs', 'docs-v2');
    fireEvent.click(screen.getByRole('button', { name: 'Save alias' }));
    expect((await screen.findByRole('status')).textContent).toBe('Moved alias docs → docs-v2.');
  });

  test('a refused save is shown', async () => {
    vi.mocked(mddbClient.setAlias).mockRejectedValue(new Error('alias names an existing collection'));
    render(<AliasesPanel />);
    await screen.findByText('docs');
    fill('docs-v1', 'docs-v2');
    fireEvent.click(screen.getByRole('button', { name: 'Save alias' }));
    expect((await screen.findByRole('alert')).textContent).toBe('alias names an existing collection');
  });

  test('deletes after confirmation', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    vi.mocked(mddbClient.deleteAlias).mockResolvedValue({});
    render(<AliasesPanel />);
    await screen.findByText('docs');
    fireEvent.click(screen.getByRole('button', { name: 'Delete alias docs' }));
    expect((await screen.findByRole('status')).textContent).toBe('Deleted alias docs.');
    expect(mddbClient.deleteAlias).toHaveBeenCalledWith('docs');
  });

  test('a declined confirm deletes nothing', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(false);
    render(<AliasesPanel />);
    await screen.findByText('docs');
    fireEvent.click(screen.getByRole('button', { name: 'Delete alias docs' }));
    await waitFor(() => expect(mddbClient.deleteAlias).not.toHaveBeenCalled());
  });
});
