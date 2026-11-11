// @vitest-environment jsdom
// Import page, bulk side: documents from pasted JSON/NDJSON or a file, added
// at once (/v1/add-batch) or queued (/v1/bulk-ingest-job); the job list with
// live progress until a job stops, its errors, and Cancel for pending jobs.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act, within } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: {
    listBulkIngestJobs: vi.fn(),
    getBulkIngestJob: vi.fn(),
    startBulkIngestJob: vi.fn(),
    cancelBulkIngestJob: vi.fn(),
    addBatch: vi.fn(),
  },
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { POLL_MS } = await import('../lib/use-job-polling');
const { default: ImportPanel } = await import('./ImportPanel');

const initialStore = useStore.getState();
const apiError = (status, text) => Object.assign(new Error(`API Error (${status}): ${text}`), { status, body: { error: text } });
const done = { id: 'j0', collection: 'old', status: 'completed', total: 3, processed: 3, added: 2, failed: 1, errors: ['c: missing key or lang'], submittedAt: 100 };

async function renderPage() {
  render(<ImportPanel />);
  await act(async () => {});
}

const documentsField = () => screen.getByLabelText('Documents (JSON array or one JSON object per line)');

beforeEach(() => {
  useStore.setState(initialStore, true);
  useStore.setState({ stats: { collections: [{ name: 'blog' }] }, currentCollection: 'blog' });
  for (const fn of Object.values(mddbClient)) fn.mockReset();
  mddbClient.listBulkIngestJobs.mockResolvedValue({ jobs: [done], total: 1 });
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe('DocumentsJsonForm', () => {
  test('parses as typed, adds at once and lists per-document failures', async () => {
    mddbClient.addBatch.mockResolvedValueOnce({ added: 1, updated: 1, failed: 1, errors: ['x: missing key or lang'] });
    await renderPage();
    const add = screen.getByRole('button', { name: 'Add now' });
    expect(add.disabled).toBe(true);
    expect(screen.getByLabelText('Collection').value).toBe('blog');

    fireEvent.change(documentsField(), { target: { value: '[{"key":"a"' } });
    expect(documentsField().getAttribute('aria-invalid')).toBe('true');
    fireEvent.change(screen.getByLabelText('Language of documents that name none'), { target: { value: 'pl' } });
    fireEvent.change(documentsField(), { target: { value: '{"key":"a","contentMd":"# A"}\n{"key":"b","lang":"en","contentMd":"# B"}' } });
    expect(screen.getByText('2 documents ready.')).toBeTruthy();
    await act(async () => fireEvent.click(add));
    expect(mddbClient.addBatch).toHaveBeenCalledWith({
      collection: 'blog',
      documents: [{ key: 'a', lang: 'pl', contentMd: '# A' }, { key: 'b', lang: 'en', contentMd: '# B' }],
    });
    const status = screen.getByRole('status');
    expect(within(status).getByText('Added 1, updated 1, failed 1.')).toBeTruthy();
    expect(within(status).getByText('x: missing key or lang')).toBeTruthy();

    mddbClient.addBatch.mockResolvedValueOnce({});
    fireEvent.change(screen.getByLabelText('Language of documents that name none'), { target: { value: '' } });
    await act(async () => fireEvent.click(add));
    expect(mddbClient.addBatch.mock.lastCall[0].documents[0].lang).toBe('en');
    expect(screen.getByText('Added 0, updated 0, failed 0.')).toBeTruthy();

    mddbClient.addBatch.mockRejectedValueOnce(apiError(403, 'collection "blog" is read-only'));
    await act(async () => fireEvent.click(add));
    expect(screen.getByText('Not permitted')).toBeTruthy();
  });

  test('needs a collection; loads a file into the field', async () => {
    useStore.setState({ currentCollection: null, stats: null });
    await renderPage();
    const file = new File(['[{"key":"f","contentMd":"x"}]'], 'docs.json', { type: 'application/json' });
    await act(async () => fireEvent.change(screen.getByLabelText('Or load a .json or .ndjson file'), { target: { files: [file] } }));
    expect(documentsField().value).toBe('[{"key":"f","contentMd":"x"}]');
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Add now' })));
    expect(screen.getByRole('alert').textContent).toBe('Name the collection the documents go to.');
    expect(mddbClient.addBatch).not.toHaveBeenCalled();
    await act(async () => fireEvent.change(screen.getByLabelText('Or load a .json or .ndjson file'), { target: { files: [] } }));
    expect(documentsField().value).toBe('[{"key":"f","contentMd":"x"}]');
  });

  test('queues a job and follows it until it completes', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    mddbClient.startBulkIngestJob.mockResolvedValue({ id: 'j1', collection: 'blog', status: 'pending', total: 2, submittedAt: 200 });
    mddbClient.getBulkIngestJob
      .mockResolvedValueOnce({ id: 'j1', collection: 'blog', status: 'processing', total: 2, processed: 1, added: 1 })
      .mockResolvedValueOnce({ id: 'j1', collection: 'blog', status: 'completed', total: 2, processed: 2, added: 2 });
    await renderPage();
    fireEvent.change(documentsField(), { target: { value: '[{"key":"a"},{"key":"b"}]' } });
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Queue as a job' })));
    expect(mddbClient.startBulkIngestJob).toHaveBeenCalledWith({ collection: 'blog', documents: [{ key: 'a', lang: 'en' }, { key: 'b', lang: 'en' }] });
    expect(screen.getByText('Queued job j1: 2 documents.')).toBeTruthy();
    const table = screen.getByRole('table', { name: 'Bulk import jobs' });
    expect(within(table).getByText('Queued: 2 documents.')).toBeTruthy();

    await act(() => vi.advanceTimersByTimeAsync(POLL_MS));
    expect(mddbClient.getBulkIngestJob).toHaveBeenCalledWith('j1');
    expect(within(table).getByText('1 of 2 processed (1 added, 0 updated, 0 failed).')).toBeTruthy();
    expect(within(table).getByRole('progressbar', { name: 'Progress of blog' })).toBeTruthy();

    mddbClient.listBulkIngestJobs.mockResolvedValue({ jobs: [{ id: 'j1', collection: 'blog', status: 'completed', total: 2, added: 2 }, done] });
    await act(() => vi.advanceTimersByTimeAsync(POLL_MS));
    expect(within(table).getByText('2 added, 0 updated, 0 failed of 2.')).toBeTruthy();
    expect(mddbClient.listBulkIngestJobs).toHaveBeenCalledTimes(2);
  });
});

describe('BulkJobsTable', () => {
  test('lists jobs with their errors; refresh reloads', async () => {
    await renderPage();
    const table = screen.getByRole('table', { name: 'Bulk import jobs' });
    expect(within(table).getByText('2 added, 0 updated, 1 failed of 3.')).toBeTruthy();
    expect(within(table).getByText('c: missing key or lang')).toBeTruthy();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Refresh bulk import jobs' })));
    expect(mddbClient.listBulkIngestJobs).toHaveBeenCalledTimes(2);
  });

  test('picks up unfinished jobs from the list; cancels a pending one; shows failed and cancelled ones', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    mddbClient.listBulkIngestJobs.mockResolvedValue({ jobs: [
      { id: 'p1', collection: 'queued', status: 'pending', total: 5 },
      { id: 'f1', collection: 'broken', status: 'failed', total: 1, error: 'server restarted' },
      { id: 'c1', collection: 'gone', status: 'cancelled', total: 1 },
      { id: 'x1', collection: 'odd', status: 'weird', total: 1 },
    ] });
    mddbClient.getBulkIngestJob.mockRejectedValue(apiError(404, 'job p1 not found'));
    mddbClient.cancelBulkIngestJob.mockResolvedValueOnce({}).mockRejectedValueOnce(apiError(400, 'job p1 is completed, only pending jobs can be cancelled'));
    await renderPage();
    const table = screen.getByRole('table', { name: 'Bulk import jobs' });
    expect(within(table).getByText('Failed: server restarted')).toBeTruthy();
    expect(within(table).getByText('Cancelled before it started.')).toBeTruthy();
    expect(within(table).getByText('weird')).toBeTruthy();

    await act(async () => fireEvent.click(within(table).getByRole('button', { name: 'Cancel job for queued' })));
    expect(mddbClient.cancelBulkIngestJob).toHaveBeenCalledWith('p1');
    await act(async () => fireEvent.click(within(table).getByRole('button', { name: 'Cancel job for queued' })));
    expect(screen.getByText('API Error (400): job p1 is completed, only pending jobs can be cancelled')).toBeTruthy();

    // A poll that fails ends the job as failed with the server's words.
    mddbClient.listBulkIngestJobs.mockReturnValue(new Promise(() => {}));
    await act(() => vi.advanceTimersByTimeAsync(POLL_MS));
    // Both unfinished jobs (pending and the unknown status) were polled.
    expect(within(table).getAllByText('Failed: API Error (404): job p1 not found')).toHaveLength(2);
  });

  test('a job that is already finished is listed, not polled; a late progress answer does not revive a finished job', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    mddbClient.listBulkIngestJobs.mockResolvedValue({ jobs: [{ id: 'r1', collection: 'run', status: 'processing', total: 4 }] });
    const polls = [];
    mddbClient.getBulkIngestJob.mockImplementation(() => new Promise((resolve) => polls.push(resolve)));
    mddbClient.startBulkIngestJob.mockResolvedValue({ id: 'q9', collection: 'other', status: 'completed', total: 1, added: 1 });
    await renderPage();
    const table = screen.getByRole('table', { name: 'Bulk import jobs' });
    expect(within(table).getByText('0 of 4 processed (0 added, 0 updated, 0 failed).')).toBeTruthy();

    fireEvent.change(screen.getByLabelText('Collection'), { target: { value: 'other' } });
    fireEvent.change(documentsField(), { target: { value: '{"key":"z"}' } });
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Queue as a job' })));
    expect(mddbClient.startBulkIngestJob.mock.lastCall[0].collection).toBe('other');
    expect(within(table).getByText('1 added, 0 updated, 0 failed of 1.')).toBeTruthy();

    await act(() => vi.advanceTimersByTimeAsync(POLL_MS));
    await act(() => vi.advanceTimersByTimeAsync(POLL_MS));
    expect(polls).toHaveLength(2);
    mddbClient.listBulkIngestJobs.mockReturnValue(new Promise(() => {}));
    await act(async () => polls[1]({ id: 'r1', collection: 'run', status: 'completed', total: 4, added: 4 }));
    await act(async () => polls[0]({ id: 'r1', collection: 'run', status: 'processing', total: 4, processed: 1 }));
    expect(within(table).getByText('4 added, 0 updated, 0 failed of 4.')).toBeTruthy();
  });

  test('empty, null and refused lists', async () => {
    mddbClient.listBulkIngestJobs.mockResolvedValueOnce({ jobs: null, total: 0 });
    await renderPage();
    expect(screen.getByText('No bulk import jobs since the server started.')).toBeTruthy();
    cleanup();
    mddbClient.listBulkIngestJobs.mockResolvedValueOnce(null);
    await renderPage();
    expect(screen.getByText('No bulk import jobs since the server started.')).toBeTruthy();
    cleanup();
    mddbClient.listBulkIngestJobs.mockRejectedValueOnce(apiError(400, 'bulk ingest is not initialized'));
    await renderPage();
    expect(screen.getByText('API Error (400): bulk ingest is not initialized')).toBeTruthy();
    expect(screen.queryByRole('table')).toBeNull();
  });
});
