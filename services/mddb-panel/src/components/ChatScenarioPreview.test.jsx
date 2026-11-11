// @vitest-environment jsdom
// ChatScenarioPreview (#403): the composed prompt from the server, reloaded
// when the store's version changes or on demand, and the "Try it" placeholder.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { getChatScenarioPrompt: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { default: ChatScenarioPreview, TryItPlaceholder } = await import('./ChatScenarioPreview');

beforeEach(() => {
  vi.mocked(mddbClient.getChatScenarioPrompt).mockResolvedValue({ prompt: 'You are Ada.' });
});
afterEach(cleanup);

describe('ChatScenarioPreview', () => {
  test('shows each external MCP server active or with the reason it is not (MCP-005)', async () => {
    vi.mocked(mddbClient.getChatScenarioPrompt).mockResolvedValue({ prompt: 'p', mcpServers: [
      { name: 'wiki', active: true, tools: [{ name: 'search' }] },
      { name: 'empty', active: true, tools: [] },
      { name: 'crm', active: false, reason: 'feature chatExternalMcp is off', tools: [] },
    ] });
    render(<ChatScenarioPreview name="support" version={1} />);
    expect(await screen.findByText('active — tools: search')).toBeTruthy();
    expect(screen.getByText('active — tools: none allowed')).toBeTruthy();
    expect(screen.getByText('inactive: feature chatExternalMcp is off')).toBeTruthy();
  });

  test('shows the prompt, and asks again on Reload and on a new version', async () => {
    const view = render(<ChatScenarioPreview name="support" version={1} />);
    expect((await screen.findByText('You are Ada.')).tagName).toBe('PRE');
    fireEvent.click(screen.getByRole('button', { name: 'Reload' }));
    view.rerender(<ChatScenarioPreview name="support" version={2} />);
    await act(async () => {});
    expect(mddbClient.getChatScenarioPrompt).toHaveBeenCalledTimes(3);
    expect(mddbClient.getChatScenarioPrompt).toHaveBeenLastCalledWith('support');
  });

  test('a refusal replaces the prompt with the server message, and a later success clears it', async () => {
    vi.mocked(mddbClient.getChatScenarioPrompt).mockRejectedValueOnce(new Error('HTTP 403: forbidden'));
    render(<ChatScenarioPreview name="support" version={1} />);
    expect((await screen.findByRole('alert')).textContent).toBe('HTTP 403: forbidden');
    expect(document.querySelector('pre')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Reload' }));
    expect(await screen.findByText('You are Ada.')).toBeTruthy();
    expect(screen.queryByRole('alert')).toBeNull();
  });

  test('an answer that arrives after the scenario changed is dropped', async () => {
    let answerFirst;
    let failSecond;
    vi.mocked(mddbClient.getChatScenarioPrompt)
      .mockReturnValueOnce(new Promise((resolve) => { answerFirst = resolve; }))
      .mockReturnValueOnce(new Promise((_resolve, reject) => { failSecond = reject; }))
      .mockResolvedValue({ prompt: 'You are Bo.' });
    const view = render(<ChatScenarioPreview name="support" version={1} />);
    view.rerender(<ChatScenarioPreview name="sales" version={1} />);
    view.rerender(<ChatScenarioPreview name="billing" version={1} />);
    expect(await screen.findByText('You are Bo.')).toBeTruthy();
    await act(async () => {
      answerFirst({ prompt: 'stale' });
      failSecond(new Error('stale failure'));
    });
    expect(screen.queryByText('stale')).toBeNull();
    expect(screen.queryByRole('alert')).toBeNull();
  });
});

test('the placeholder names the scenario and what is missing', () => {
  render(<TryItPlaceholder name="support" />);
  const section = screen.getByRole('region', { name: 'Try it' });
  expect(section.textContent).toContain('Talking to support from here needs the chat service (mddb-chat)');
});

describe('the prompt facts (#404)', () => {
  test('size and tokens, the cap warning and the skipped skills', async () => {
    vi.mocked(mddbClient.getChatScenarioPrompt).mockResolvedValue({
      prompt: 'You are Ada.', bytes: 100000, approxTokens: 25000,
      warning: 'the composed prompt is 100000 bytes, 76% of the 131072-byte limit', missingSkills: ['ghost', 'phantom'],
    });
    render(<ChatScenarioPreview name="support" version={1} />);
    expect(await screen.findByText('97.7 KiB · about 25000 tokens (bytes ÷ 4)')).toBeTruthy();
    const notes = screen.getAllByRole('status').map((n) => n.textContent);
    expect(notes).toEqual(['the composed prompt is 100000 bytes, 76% of the 131072-byte limit', 'Skipped skills that are not stored: ghost, phantom']);
  });

  test('an answer without the fields shows zero and no notes', async () => {
    render(<ChatScenarioPreview name="support" version={1} />);
    expect(await screen.findByText('0 B · about 0 tokens (bytes ÷ 4)')).toBeTruthy();
    expect(screen.queryByRole('status')).toBeNull();
  });
});
