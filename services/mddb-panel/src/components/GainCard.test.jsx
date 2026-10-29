// @vitest-environment jsdom
// GainCard: the 30-day retrieval gain card on the System page (AGT-004).
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { getGain: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { default: GainCard } = await import('./GainCard');

const report = {
  windowDays: 30,
  totalSearches: 14922,
  tokensWithoutRetrieval: 2000000,
  savedPercent: 87,
  totals: {
    searches: { fts: 3, vector: 1 },
    tokensReturned: 1500,
    fragmentHits: 12,
    documentHits: 3,
    tokensAvoided: 160700000,
    algorithms: { hnsw: 65, flat: 35 },
    fusion: { rrf: 4 },
  },
};

/** The value and note cells next to a row label. */
function row(label) {
  const dt = screen.getByText(label);
  const [value, note] = dt.parentElement.querySelectorAll('dd');
  return [value.textContent, note.textContent];
}

beforeEach(() => {
  vi.mocked(mddbClient.getGain).mockReset();
});
afterEach(cleanup);

describe('GainCard', () => {
  test('asks for the last 30 days and renders nothing until the report arrives', () => {
    vi.mocked(mddbClient.getGain).mockReturnValue(new Promise(() => {}));
    const { container } = render(<GainCard />);
    expect(mddbClient.getGain).toHaveBeenCalledWith({ window: '30d' });
    expect(container.innerHTML).toBe('');
  });

  test('shows why the gain is unavailable when the request fails', async () => {
    vi.mocked(mddbClient.getGain).mockRejectedValue(new Error('API Error (403): forbidden'));
    render(<GainCard />);
    expect(await screen.findByText('Retrieval gain unavailable: API Error (403): forbidden')).toBeTruthy();
    expect(screen.queryByRole('heading')).toBeNull();
  });

  test('renders counts, savings and the largest shares', async () => {
    vi.mocked(mddbClient.getGain).mockResolvedValue(report);
    render(<GainCard />);
    expect((await screen.findByRole('heading')).textContent).toBe('Retrieval gain, last 30 days');
    expect(row('Searches')).toEqual(['14.9k', 'fts 75% · vector 25%']);
    expect(row('Returned')).toEqual(['1.5k tokens', '12 passages, 3 documents']);
    expect(row('Without retrieval')).toEqual(['2.0M tokens', 'the same hits as whole documents']);
    expect(row('Saved')).toEqual(['160.7M tokens', '87%']);
    expect(row('Algorithms')).toEqual(['hnsw 65% · flat 35%', 'fusion rrf 100%']);
  });

  test('empty breakdown maps leave the share cells blank and show a dash', async () => {
    vi.mocked(mddbClient.getGain).mockResolvedValue({
      ...report,
      totalSearches: 0,
      totals: { ...report.totals, searches: {}, algorithms: {}, fusion: {} },
    });
    render(<GainCard />);
    await screen.findByRole('heading');
    expect(row('Searches')).toEqual(['0', '']);
    expect(row('Algorithms')).toEqual(['–', '']);
  });

  test('a report without totals renders zeros instead of crashing', async () => {
    vi.mocked(mddbClient.getGain).mockResolvedValue({ windowDays: 7, totalSearches: 0, tokensWithoutRetrieval: 0, savedPercent: 0 });
    render(<GainCard />);
    expect((await screen.findByRole('heading')).textContent).toBe('Retrieval gain, last 7 days');
    expect(row('Returned')).toEqual(['0 tokens', '0 passages, 0 documents']);
    expect(row('Saved')).toEqual(['0 tokens', '0%']);
  });
});
