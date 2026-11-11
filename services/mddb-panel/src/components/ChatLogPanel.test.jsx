// @vitest-environment jsdom
// ChatLogPanel (#406): the Log tab — filters, the turns with their details,
// paging, export and the docs gaps.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor, within } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: { listChatScenarios: vi.fn(), chatLog: vi.fn(), chatLogExport: vi.fn(), chatGaps: vi.fn() },
}));
vi.mock('../lib/download', () => ({ saveBlob: vi.fn() }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { saveBlob } = await import('../lib/download');
const { default: ChatLogPanel } = await import('./ChatLogPanel');

const signed = {
  conversationId: 'c1', messageId: 'm1', scenario: 'support', sub: 'u1', displayName: 'Ada', anonymous: false, at: 1_800_000_000,
  question: 'Where is the backup guide?', answer: 'Nothing found, sorry.', answerExcerpt: 'Nothing found…', outcome: 'noResults',
  rating: 'down', comment: 'Useless', model: 'gpt-x', provider: 'openai', latencyMs: 812, tokens: { prompt: 120, completion: 30 },
  toolCalls: [
    { name: 'search_docs', collection: 'docs', query: 'backup guide', resultKeys: [], durationMs: 40 },
    { name: 'get_document', query: 'backup', resultKeys: ['backup', 'restore'], topScore: 0.42 },
  ],
};
const anon = { conversationId: 'c2', messageId: 'm2', scenario: 'support', anonymous: true, at: 1_800_000_100, question: '', answer: '', answerExcerpt: 'Hi', outcome: 'mystery' };

beforeEach(() => {
  vi.mocked(mddbClient.listChatScenarios).mockResolvedValue({ scenarios: [{ name: 'support' }, { name: 'sales' }] });
  vi.mocked(mddbClient.chatLog).mockResolvedValue({ turns: [signed], nextCursor: 'next' });
  vi.mocked(mddbClient.chatGaps).mockResolvedValue({ gaps: [], threshold: 0.3 });
});
afterEach(cleanup);

describe('turns', () => {
  test('lists turns, expands one with its tools, and loads the next page', async () => {
    render(<ChatLogPanel />);
    expect(await screen.findByText('Where is the backup guide?')).toBeTruthy();
    expect(screen.getByText('Ada (u1)')).toBeTruthy();
    expect(screen.getAllByText('No results').length).toBeGreaterThan(0);
    const toggle = screen.getByRole('button', { name: /Show the turn/ });
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    fireEvent.click(toggle);
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
    expect(screen.getByText('Nothing found, sorry.')).toBeTruthy();
    expect(screen.getByText('gpt-x (openai)')).toBeTruthy();
    expect(screen.getByText('812 ms · 120 in / 30 out')).toBeTruthy();
    expect(screen.getByText('Thumbs down — Useless')).toBeTruthy();
    expect(screen.getByText('nothing found')).toBeTruthy();
    expect(screen.getByText('backup, restore')).toBeTruthy();
    expect(screen.getByText('0.42')).toBeTruthy();

    vi.mocked(mddbClient.chatLog).mockResolvedValue({ turns: [anon] });
    fireEvent.click(screen.getByRole('button', { name: 'Load more' }));
    expect(await screen.findByText('anonymous')).toBeTruthy();
    expect(mddbClient.chatLog).toHaveBeenLastCalledWith(expect.objectContaining({ cursor: 'next' }));
    expect(screen.queryByRole('button', { name: 'Load more' })).toBeNull();
    expect(screen.getByText('mystery')).toBeTruthy();

    fireEvent.click(screen.getByRole('button', { name: /Show the turn/ })); // the first row is open
    expect(screen.getByText('No tools were called.')).toBeTruthy();
    fireEvent.click(screen.getAllByRole('button', { name: /Hide the turn/ })[0]);
  });

  test('a next page without turns keeps what is shown', async () => {
    render(<ChatLogPanel />);
    await screen.findByText('Where is the backup guide?');
    vi.mocked(mddbClient.chatLog).mockResolvedValue({});
    fireEvent.click(screen.getByRole('button', { name: 'Load more' }));
    await waitFor(() => expect(screen.queryByRole('button', { name: /Load more|Loading/ })).toBeNull());
    expect(screen.getByText('Where is the backup guide?')).toBeTruthy();
  });

  test('a failed next page says why', async () => {
    render(<ChatLogPanel />);
    await screen.findByText('Where is the backup guide?');
    vi.mocked(mddbClient.chatLog).mockRejectedValue(new Error('page refused'));
    fireEvent.click(screen.getByRole('button', { name: 'Load more' }));
    expect((await screen.findByRole('alert')).textContent).toBe('page refused');
  });

  test('a turn without model, provider or tokens shows dashes', async () => {
    vi.mocked(mddbClient.chatLog).mockResolvedValue({ turns: [{ ...anon, model: 'm', toolCalls: [{ name: 't' }] }] });
    render(<ChatLogPanel />);
    fireEvent.click(await screen.findByRole('button', { name: /Show the turn/ }));
    expect(screen.getByText('m')).toBeTruthy();
    expect(screen.getByText('— · —')).toBeTruthy();
  });

  test('the filters reach the query', async () => {
    render(<ChatLogPanel />);
    await screen.findByText('Where is the backup guide?');
    await screen.findByRole('option', { name: 'sales' });
    fireEvent.change(screen.getByLabelText('Scenario'), { target: { value: 'sales' } });
    fireEvent.change(screen.getByLabelText('From'), { target: { value: '2026-11-01' } });
    fireEvent.change(screen.getByLabelText('To'), { target: { value: '2026-11-02' } });
    fireEvent.change(screen.getByLabelText('Outcome'), { target: { value: 'error' } });
    fireEvent.click(screen.getByLabelText('Thumbs-down only'));
    fireEvent.click(screen.getByLabelText('Refused / off-topic'));
    fireEvent.click(screen.getByLabelText('No results'));
    await waitFor(() => expect(mddbClient.chatLog).toHaveBeenLastCalledWith({
      scenario: 'sales', from: '2026-11-01', to: '2026-11-02', rating: 'down', outcome: 'noResults', cursor: '',
    }));
  });

  test('an empty log, a failed load and an unlisted scenario store', async () => {
    vi.mocked(mddbClient.listChatScenarios).mockRejectedValue(new Error('nope'));
    vi.mocked(mddbClient.chatLog).mockResolvedValue({});
    render(<ChatLogPanel />);
    expect(await screen.findByText('No logged turns match.')).toBeTruthy();
    vi.mocked(mddbClient.chatLog).mockRejectedValue(new Error('HTTP 403: admin access required'));
    fireEvent.click(screen.getByLabelText('Thumbs-down only'));
    expect((await screen.findByRole('alert')).textContent).toContain('admin access required');
  });

  test('a scenario store without the list leaves the picker with all', async () => {
    vi.mocked(mddbClient.listChatScenarios).mockResolvedValue({});
    render(<ChatLogPanel />);
    await screen.findByText('Where is the backup guide?');
    expect(within(screen.getByLabelText('Scenario')).getAllByRole('option')).toHaveLength(1);
  });
});

describe('export', () => {
  test('saves CSV and JSON with the filters, and shows a failure', async () => {
    const file = { blob: new Blob(['x']), filename: 'chat-log.csv' };
    vi.mocked(mddbClient.chatLogExport).mockResolvedValue(file);
    render(<ChatLogPanel />);
    await screen.findByText('Where is the backup guide?');
    fireEvent.click(screen.getByRole('button', { name: 'Export CSV' }));
    await waitFor(() => expect(saveBlob).toHaveBeenCalledWith(file, 'chat-log.csv'));
    fireEvent.click(screen.getByRole('button', { name: 'Export JSON' }));
    await waitFor(() => expect(mddbClient.chatLogExport).toHaveBeenLastCalledWith(expect.objectContaining({ rating: '' }), 'json'));
    vi.mocked(mddbClient.chatLogExport).mockRejectedValue(new Error('export refused'));
    fireEvent.click(screen.getByRole('button', { name: 'Export CSV' }));
    expect((await screen.findByRole('alert')).textContent).toBe('export refused');
  });
});

describe('docs gaps', () => {
  test('lists the grouped questions and follows the settings', async () => {
    vi.mocked(mddbClient.chatGaps).mockResolvedValue({
      threshold: 0.3,
      gaps: [{ question: 'backup', example: 'Backup?', count: 4, noResults: 3, lastAskedAt: 1_800_000_000, scenarios: ['a', 'b'] },
        { question: 'x', example: 'X?', count: 1, noResults: 0, lastAskedAt: 0 }],
    });
    render(<ChatLogPanel />);
    fireEvent.click(screen.getByRole('button', { name: 'Docs gaps' }));
    expect(await screen.findByText('Backup?')).toBeTruthy();
    expect(screen.getByText('a, b')).toBeTruthy();
    expect(screen.getByText(/\(0\.3\)/)).toBeTruthy();
    fireEvent.change(screen.getByLabelText('Asked at least'), { target: { value: '2' } });
    fireEvent.change(screen.getByLabelText('Score threshold'), { target: { value: '0.5' } });
    await waitFor(() => expect(mddbClient.chatGaps).toHaveBeenLastCalledWith({ scenario: '', from: '', to: '', minCount: '2', threshold: '0.5' }));
    fireEvent.click(screen.getByRole('button', { name: 'Turns' }));
    expect(await screen.findByText('Where is the backup guide?')).toBeTruthy();
  });

  test('no gaps, a server without the field, and a failure', async () => {
    render(<ChatLogPanel />);
    fireEvent.click(screen.getByRole('button', { name: 'Docs gaps' }));
    expect(await screen.findByText('No gaps in this window.')).toBeTruthy();
    vi.mocked(mddbClient.chatGaps).mockResolvedValue({ threshold: 0.3 });
    fireEvent.change(screen.getByLabelText('Asked at least'), { target: { value: '3' } });
    expect(await screen.findByText('No gaps in this window.')).toBeTruthy();
    vi.mocked(mddbClient.chatGaps).mockRejectedValue(new Error('gaps refused'));
    fireEvent.change(screen.getByLabelText('Asked at least'), { target: { value: '4' } });
    expect((await screen.findByRole('alert')).textContent).toBe('gaps refused');
  });
});
