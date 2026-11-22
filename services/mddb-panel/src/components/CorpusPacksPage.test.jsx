// @vitest-environment jsdom
// CorpusPacksPage (FE-022): the page loads the packs, hides what only an
// administrator may do when sign-in is on, takes the intent it was opened
// with, and refreshes the packs and the collections after a change.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, waitFor, act } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { listCorpus: vi.fn(), getStats: vi.fn(), getMe: vi.fn() } }));
let cardProps;
let uploadProps;
let buildProps;
vi.mock('./CorpusPacksCard', () => ({ default: (p) => { cardProps = p; return <div>packs card</div>; } }));
vi.mock('./CorpusUploadCard', () => ({ default: (p) => { uploadProps = p; return <div>upload card</div>; } }));
vi.mock('./CorpusBuildForm', () => ({ default: (p) => { buildProps = p; return <div>build form</div>; } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { openCorpusPage, useCorpusIntent } = await import('../lib/corpus-intent');
const { default: CorpusPacksPage } = await import('./CorpusPacksPage');

const initialStore = useStore.getState();
const entry = { collection: 'rfc', readOnly: true, corpus: { name: 'rfc', license: 'MIT', docCount: 1 } };

beforeEach(() => {
  useStore.setState(initialStore, true);
  useStore.setState({ stats: { collections: [{ name: 'docs' }] }, config: { authEnabled: false } });
  useCorpusIntent.setState({ intent: null });
  for (const fn of Object.values(mddbClient)) vi.mocked(fn).mockReset();
  vi.mocked(mddbClient.listCorpus).mockResolvedValue({ packs: [entry] });
  vi.mocked(mddbClient.getStats).mockResolvedValue({ collections: [{ name: 'docs' }, { name: 'rfc' }] });
  vi.mocked(mddbClient.getMe).mockRejectedValue(new Error('no auth'));
});
afterEach(cleanup);

describe('CorpusPacksPage', () => {
  test('lists the packs and offers upload and create without sign-in', async () => {
    render(<CorpusPacksPage />);
    await waitFor(() => expect(cardProps.packs).toHaveLength(1));
    expect(cardProps.canManage).toBe(true);
    expect(screen.getByText('upload card')).toBeTruthy();
    expect(buildProps.collections).toEqual(['docs']);
    expect(screen.getByText('How packs work')).toBeTruthy();
  });

  test('a signed-in reader sees the list only', async () => {
    useStore.setState({ config: { authEnabled: true } });
    vi.mocked(mddbClient.getMe).mockResolvedValue({ username: 'r', admin: false });
    render(<CorpusPacksPage />);
    expect(await screen.findByRole('note')).toBeTruthy();
    expect(screen.queryByText('upload card')).toBeNull();
    expect(cardProps.canManage).toBe(false);
  });

  test('a failed listing goes to the card; no stats, no collections', async () => {
    useStore.setState({ stats: null });
    const err = new Error('down');
    vi.mocked(mddbClient.listCorpus).mockRejectedValue(err);
    render(<CorpusPacksPage />);
    await waitFor(() => expect(cardProps.loadError).toBe(err));
    expect(buildProps.collections).toEqual([]);
  });

  test('shows the intent while open: collections to build, a pack to show', async () => {
    act(() => openCorpusPage({ build: ['docs'], pack: 'rfc' }));
    expect(useStore.getState().viewMode).toBe('corpus');
    const { unmount } = render(<CorpusPacksPage />);
    await waitFor(() => expect(buildProps.preset).toEqual(['docs']));
    expect(cardProps.highlight).toBe('rfc');
    act(() => openCorpusPage({ build: ['rfc'] }));
    expect(buildProps.preset).toEqual(['rfc']);
    expect(cardProps.highlight).toBeUndefined();
    unmount();
    expect(useCorpusIntent.getState().intent).toBeNull();
  });

  test('a change reloads the packs and the collections', async () => {
    render(<CorpusPacksPage />);
    await waitFor(() => expect(cardProps.packs).toHaveLength(1));
    await act(() => uploadProps.onInstalled());
    expect(mddbClient.listCorpus).toHaveBeenCalledTimes(2);
    expect(useStore.getState().stats.collections).toHaveLength(2);
  });
});
