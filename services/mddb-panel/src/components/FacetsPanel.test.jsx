// @vitest-environment jsdom
// FacetsPanel: the metadata keys of the collection, counts per value from
// /v1/aggregate within the list's filters, as one table per key.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act, within } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { getMetaKeys: vi.fn(), aggregate: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { default: FacetsPanel } = await import('./FacetsPanel');

const apiError = (status, text) => Object.assign(new Error(`API Error (${status}): ${text}`), { status, body: { error: text } });

async function renderPanel(filters = {}) {
  render(<FacetsPanel collection="docs" filters={filters} />);
  await act(async () => {});
}

beforeEach(() => {
  mddbClient.getMetaKeys.mockReset().mockResolvedValue({ meta: { tag: ['a'], category: ['x'] } });
  mddbClient.aggregate.mockReset();
});
afterEach(cleanup);

describe('FacetsPanel', () => {
  test('counts the chosen keys in the chosen order and shows a table per key', async () => {
    mddbClient.aggregate.mockResolvedValueOnce({ totalDocs: 68, facets: { category: [{ value: 'tutorial', count: 1 }, { value: 'guide', count: 9 }] } });
    await renderPanel({ status: ['published'] });
    expect(mddbClient.getMetaKeys).toHaveBeenCalledWith('docs');
    const boxes = within(screen.getByRole('group', { name: 'Metadata keys to count' })).getAllByRole('checkbox');
    expect(boxes.map((b) => b.parentElement.textContent.trim())).toEqual(['category', 'tag']);
    const count = screen.getByRole('button', { name: 'Count' });
    expect(count.disabled).toBe(true);
    fireEvent.click(screen.getByLabelText('category'));
    fireEvent.click(screen.getByLabelText('tag'));
    fireEvent.click(screen.getByLabelText('tag'));
    fireEvent.change(screen.getByLabelText('Order'), { target: { value: 'value' } });
    await act(async () => fireEvent.click(count));
    expect(mddbClient.aggregate).toHaveBeenCalledWith({ collection: 'docs', fields: ['category'], filterMeta: { status: ['published'] }, orderBy: 'value' });
    expect(screen.getByText('68 documents match the filters.')).toBeTruthy();
    const table = screen.getByRole('table', { name: 'category' });
    expect(within(table).getAllByRole('row').map((r) => r.textContent)).toEqual(['ValueDocuments', 'tutorial1', 'guide9']);
  });

  test('an answer without facets, a collection without keys and failures', async () => {
    mddbClient.aggregate.mockResolvedValueOnce({ totalDocs: 0 }).mockRejectedValueOnce(apiError(400, 'collection is required'));
    await renderPanel();
    fireEvent.click(screen.getByLabelText('tag'));
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Count' })));
    expect(screen.getByText('0 documents match the filters.')).toBeTruthy();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Count' })));
    expect(screen.getByText('API Error (400): collection is required')).toBeTruthy();
    cleanup();

    mddbClient.getMetaKeys.mockResolvedValueOnce({});
    await renderPanel();
    expect(screen.getByText('This collection has no metadata keys to count.')).toBeTruthy();
    cleanup();

    mddbClient.getMetaKeys.mockRejectedValueOnce(apiError(403, 'forbidden'));
    await renderPanel();
    expect(screen.getByText('Not permitted')).toBeTruthy();
    expect(screen.queryByText('This collection has no metadata keys to count.')).toBeNull();
  });
});
