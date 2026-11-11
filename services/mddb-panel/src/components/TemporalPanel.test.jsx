// @vitest-environment jsdom
// Temporal Analytics: no collection hint, the activity histogram, hot
// documents and a document's events (/v1/temporal/query), with a server that
// has temporal tracking off answering 404.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act, within } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { temporalHot: vi.fn(), temporalHistogram: vi.fn(), temporalQuery: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: TemporalPanel } = await import('./TemporalPanel');

const initialStore = useStore.getState();
const apiError = (status, text) => Object.assign(new Error(`API Error (${status}): ${text}`), { status, body: null });
const click = (name) => act(async () => fireEvent.click(screen.getByRole('button', { name })));

async function renderPanel(collection = 'kb') {
  useStore.setState({ currentCollection: collection });
  render(<TemporalPanel />);
  await act(async () => {});
}

beforeEach(() => {
  useStore.setState(initialStore, true);
  for (const fn of Object.values(mddbClient)) fn.mockReset();
  mddbClient.temporalHistogram.mockResolvedValue({ buckets: [{ label: '2026-11-01', count: 2 }, { label: '2026-11-02', count: 4 }] });
  mddbClient.temporalHot.mockResolvedValue({ entries: [{ docId: 'kb|a|en', document: { key: 'a', lang: 'en' }, accessCount: 7, lastAccessAt: 100 }, { docId: 'kb|b|en', accessCount: 1 }] });
});
afterEach(cleanup);

describe('TemporalPanel', () => {
  test('asks for a collection first', async () => {
    await renderPanel(null);
    expect(screen.getByText('Select a collection to view temporal analytics.')).toBeTruthy();
  });

  test('histogram: bars per bucket, reload with another event and interval, empty and errors', async () => {
    await renderPanel();
    expect(screen.getByText('kb', { exact: false })).toBeTruthy();
    expect(screen.getByText('2026-11-02')).toBeTruthy();
    const bars = document.querySelectorAll('.bg-blue-500');
    expect(bars[1].style.width).toBe('100%');
    const [eventSelect, intervalSelect] = screen.getAllByRole('combobox');
    fireEvent.change(eventSelect, { target: { value: 'update' } });
    fireEvent.change(intervalSelect, { target: { value: 'week' } });
    mddbClient.temporalHistogram.mockResolvedValueOnce({}).mockRejectedValueOnce(new Error('API Error (400): temporal not available'));
    await click('Load');
    expect(mddbClient.temporalHistogram).toHaveBeenLastCalledWith(expect.objectContaining({ collection: 'kb', eventType: 'update', interval: 'week' }));
    expect(screen.getByText('No activity data for the selected range.')).toBeTruthy();
    await click('Load');
    expect(screen.getByText('API Error (400): temporal not available')).toBeTruthy();
  });

  test('a pending histogram shows a spinner', async () => {
    mddbClient.temporalHistogram.mockReturnValue(new Promise(() => {}));
    await renderPanel();
    expect(document.querySelector('.animate-spin')).not.toBeNull();
  });

  test('hot documents: ranked list, top N, empty and errors', async () => {
    await renderPanel();
    await click('Hot Documents');
    expect(screen.getByText('a')).toBeTruthy();
    expect(screen.getByText('kb|b|en')).toBeTruthy();
    expect(screen.getByText('7')).toBeTruthy();
    expect(screen.getByText('—')).toBeTruthy();
    fireEvent.change(screen.getByRole('combobox'), { target: { value: '50' } });
    mddbClient.temporalHot.mockResolvedValueOnce({}).mockRejectedValueOnce(new Error('API Error (500): boom'));
    await click('Refresh');
    expect(mddbClient.temporalHot).toHaveBeenLastCalledWith({ collection: 'kb', topN: 50 });
    expect(screen.getByText(/No access data yet/)).toBeTruthy();
    await click('Refresh');
    expect(screen.getByText('API Error (500): boom')).toBeTruthy();
  });

  test('document events: query, table, and a server without temporal tracking', async () => {
    mddbClient.temporalQuery
      .mockResolvedValueOnce({ docId: 'kb|a|en', events: [{ eventType: 'create', timestamp: 100 }, { eventType: 'access', timestamp: 200 }], total: 2 })
      .mockResolvedValueOnce({})
      .mockRejectedValueOnce(apiError(404, '404 page not found'))
      .mockRejectedValueOnce(apiError(400, 'key is required'));
    await renderPanel();
    await click('Document Events');
    expect(screen.getByRole('button', { name: 'Show events' }).disabled).toBe(true);
    fireEvent.change(screen.getByLabelText('Document key'), { target: { value: ' a ' } });
    fireEvent.change(screen.getByLabelText('Event'), { target: { value: 'access' } });
    fireEvent.change(screen.getByLabelText('From'), { target: { value: '2026-11-01' } });
    fireEvent.change(screen.getByLabelText('To'), { target: { value: '2026-11-10' } });
    await click('Show events');
    expect(mddbClient.temporalQuery).toHaveBeenCalledWith({
      collection: 'kb', key: 'a', lang: 'en', eventType: 'access',
      from: Math.floor(new Date('2026-11-01T00:00:00').getTime() / 1000),
      to: Math.floor(new Date('2026-11-10T00:00:00').getTime() / 1000),
      limit: 100,
    });
    expect(screen.getByText('2 events of kb|a|en.')).toBeTruthy();
    expect(within(screen.getByRole('table', { name: 'Document events' })).getByText('create')).toBeTruthy();

    fireEvent.change(screen.getByLabelText('Language'), { target: { value: '' } });
    fireEvent.change(screen.getByLabelText('Event'), { target: { value: '' } });
    fireEvent.change(screen.getByLabelText('From'), { target: { value: '' } });
    fireEvent.change(screen.getByLabelText('To'), { target: { value: '' } });
    await click('Show events');
    expect(mddbClient.temporalQuery).toHaveBeenLastCalledWith({ collection: 'kb', key: 'a', lang: 'en', eventType: undefined, from: undefined, to: undefined, limit: 100 });
    expect(screen.getByText('0 events of a.')).toBeTruthy();
    expect(screen.queryByRole('table')).toBeNull();

    await click('Show events');
    expect(screen.getByRole('alert').textContent).toBe('Temporal tracking is off on this server: start it with MDDB_TEMPORAL=true.');
    await click('Show events');
    expect(screen.getByRole('alert').textContent).toBe('API Error (400): key is required');
    await click('Activity Histogram');
    expect(screen.getByText('2026-11-01')).toBeTruthy();
  });
});
