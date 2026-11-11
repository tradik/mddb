// @vitest-environment jsdom
// Agent Memory page: sessions filtered by user and scenario, one session's
// history, adding a message, summarizing, starting a session and recall —
// with the server's errors shown as worded, and a 403 as "Not permitted".
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act, within } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: {
    listMemorySessions: vi.fn(),
    getMemoryHistory: vi.fn(),
    addMemoryMessage: vi.fn(),
    summarizeMemorySession: vi.fn(),
    createMemorySession: vi.fn(),
    recallMemory: vi.fn(),
  },
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { default: MemoryPanel } = await import('./MemoryPanel');

const session = { sessionId: 's1', userId: 'u1', scenario: 'support', createdAt: 100, updatedAt: 200, expiresAt: 300, messageCount: 2 };
const message = (id, role, text) => ({ id, key: id, meta: { role: [role] }, contentMd: text, addedAt: 150 });

const apiError = (status, text) => Object.assign(new Error(`API Error (${status}): ${text}`), { status, body: { error: text } });

async function renderPage() {
  render(<MemoryPanel />);
  await act(async () => {});
}

beforeEach(() => {
  for (const fn of Object.values(mddbClient)) fn.mockReset();
  mddbClient.listMemorySessions.mockResolvedValue({ sessions: [session, { sessionId: 's2', userId: 'u2', title: 'Untitled run', createdAt: 50 }], total: 2 });
  mddbClient.getMemoryHistory.mockResolvedValue({ messages: [message('m1', 'user', 'blue green rollout'), { id: 'm2', contentMd: 'no role' }], total: 2 });
});
afterEach(cleanup);

describe('MemoryPanel sessions', () => {
  test('lists sessions, filters on Refresh and opens one with its history', async () => {
    await renderPage();
    expect(mddbClient.listMemorySessions).toHaveBeenCalledWith({ userId: undefined, scenario: undefined });
    const table = screen.getByRole('table');
    expect(within(table).getByText('2 of 2 sessions, most recently active first')).toBeTruthy();
    expect(within(table).getByText('support')).toBeTruthy();
    expect(within(table).getByText('Untitled run')).toBeTruthy();
    expect(within(table).getAllByText('—').length).toBeGreaterThan(0);

    fireEvent.change(screen.getByLabelText('Filter by user ID'), { target: { value: ' u1 ' } });
    fireEvent.change(screen.getByLabelText('Filter by scenario'), { target: { value: 'support' } });
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Refresh' })));
    expect(mddbClient.listMemorySessions).toHaveBeenLastCalledWith({ userId: 'u1', scenario: 'support' });

    await act(async () => fireEvent.click(screen.getByRole('button', { name: 's1' })));
    expect(screen.getByRole('button', { name: 's1' }).getAttribute('aria-current')).toBe('true');
    expect(mddbClient.getMemoryHistory).toHaveBeenCalledWith({ sessionId: 's1' });
    const list = screen.getByRole('list', { name: 'Messages (2)' });
    expect(within(list).getByText('blue green rollout')).toBeTruthy();
    expect(within(list).getByText('unknown')).toBeTruthy();
  });

  test('no sessions, or a refusal shown as not permitted', async () => {
    mddbClient.listMemorySessions.mockResolvedValueOnce({});
    await renderPage();
    expect(screen.getByText(/No sessions match/)).toBeTruthy();
    cleanup();
    mddbClient.listMemorySessions.mockRejectedValueOnce(apiError(403, 'forbidden'));
    await renderPage();
    expect(screen.getByText('Not permitted')).toBeTruthy();
    expect(screen.queryByRole('table')).toBeNull();
  });

  test('starting a session opens it and reloads the list', async () => {
    mddbClient.createMemorySession.mockResolvedValue({ sessionId: 'new1', userId: 'u9', title: 'Trial' });
    mddbClient.getMemoryHistory.mockResolvedValue({});
    await renderPage();
    const form = screen.getByText('Start a session').closest('details');
    const start = within(form).getByRole('button', { name: 'Start session' });
    expect(start.disabled).toBe(true);
    fireEvent.change(within(form).getByLabelText('User ID'), { target: { value: 'u9' } });
    fireEvent.change(within(form).getByLabelText('Title (optional)'), { target: { value: 'Trial' } });
    await act(async () => fireEvent.click(start));
    expect(mddbClient.createMemorySession).toHaveBeenCalledWith({ userId: 'u9', scenario: undefined, title: 'Trial' });
    expect(screen.getByRole('heading', { name: /Trial/ })).toBeTruthy();
    expect(screen.getByText('This session has no messages yet.')).toBeTruthy();
    expect(mddbClient.listMemorySessions).toHaveBeenCalledTimes(2);

    mddbClient.createMemorySession.mockRejectedValueOnce(apiError(400, 'userId is required'));
    fireEvent.change(within(form).getByLabelText('Scenario (optional)'), { target: { value: 'qa' } });
    await act(async () => fireEvent.click(start));
    expect(within(form).getByRole('alert').textContent).toBe('API Error (400): userId is required');
  });
});

describe('MemorySessionView', () => {
  async function openSession() {
    await renderPage();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 's1' })));
  }

  test('adds a message, reloads the history and the list', async () => {
    mddbClient.addMemoryMessage.mockResolvedValue({ messageId: 'x' });
    await openSession();
    const content = screen.getByLabelText('Content');
    fireEvent.change(screen.getByLabelText('Role'), { target: { value: 'assistant' } });
    fireEvent.change(content, { target: { value: ' noted ' } });
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Add message' })));
    expect(mddbClient.addMemoryMessage).toHaveBeenCalledWith({ sessionId: 's1', role: 'assistant', content: 'noted' });
    expect(content.value).toBe('');
    expect(mddbClient.getMemoryHistory).toHaveBeenCalledTimes(2);
    expect(mddbClient.listMemorySessions).toHaveBeenCalledTimes(2);

    mddbClient.addMemoryMessage.mockRejectedValueOnce(apiError(413, 'too large'));
    fireEvent.change(content, { target: { value: 'again' } });
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Add message' })));
    expect(screen.getByText('API Error (413): too large')).toBeTruthy();
  });

  test('summarizes, or shows why it could not', async () => {
    mddbClient.summarizeMemorySession.mockResolvedValueOnce({ summaryId: 'sum1', messages: 2, summary: '# Session Summary' });
    await openSession();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Summarize' })));
    expect(screen.getByText('Summary of 2 messages, stored as sum1')).toBeTruthy();
    expect(screen.getByText('# Session Summary')).toBeTruthy();
    mddbClient.summarizeMemorySession.mockRejectedValueOnce(apiError(503, 'memory unavailable'));
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Summarize' })));
    expect(screen.getByText('API Error (503): memory unavailable')).toBeTruthy();
  });

  test('a history that fails to load says why', async () => {
    mddbClient.getMemoryHistory.mockRejectedValue(apiError(404, 'session not found'));
    await openSession();
    expect(screen.getByText('API Error (404): session not found')).toBeTruthy();
    expect(screen.queryByText('Loading messages…')).toBeNull();
  });

  test('shows a loading line until the history arrives', async () => {
    mddbClient.getMemoryHistory.mockReturnValue(new Promise(() => {}));
    await openSession();
    expect(screen.getByText('Loading messages…')).toBeTruthy();
  });
});

describe('MemoryRecallBox', () => {
  test('recalls with the strategy and the page user, and lists the hits', async () => {
    mddbClient.recallMemory.mockResolvedValueOnce({
      results: [{ document: { id: 'd1', contentMd: 'blue green' }, score: 0.0327, rank: 1, sessionId: 's1', role: 'user' }],
      total: 1,
      strategy: 'keyword',
    });
    await renderPage();
    const recall = screen.getByRole('button', { name: 'Recall' });
    expect(recall.disabled).toBe(true);
    fireEvent.change(screen.getByLabelText('Filter by user ID'), { target: { value: 'u1' } });
    fireEvent.change(screen.getByLabelText('Query'), { target: { value: ' rollout ' } });
    fireEvent.change(screen.getByLabelText('Strategy'), { target: { value: 'keyword' } });
    await act(async () => fireEvent.click(recall));
    expect(mddbClient.recallMemory).toHaveBeenCalledWith({ query: 'rollout', userId: 'u1', strategy: 'keyword' });
    expect(screen.getByText('1 messages recalled (keyword).')).toBeTruthy();
    expect(screen.getByText(/score 0.033/)).toBeTruthy();
    expect(screen.getByText('blue green')).toBeTruthy();

    mddbClient.recallMemory.mockResolvedValueOnce({ strategy: 'hybrid' });
    fireEvent.change(screen.getByLabelText('Filter by user ID'), { target: { value: '' } });
    await act(async () => fireEvent.click(recall));
    expect(mddbClient.recallMemory).toHaveBeenLastCalledWith({ query: 'rollout', userId: undefined, strategy: 'keyword' });
    expect(screen.getByText('0 messages recalled (hybrid).')).toBeTruthy();

    mddbClient.recallMemory.mockRejectedValueOnce(apiError(400, 'query is required'));
    await act(async () => fireEvent.click(recall));
    expect(screen.getByText('API Error (400): query is required')).toBeTruthy();
  });
});
