// @vitest-environment jsdom
// Geo tools: area search by bounding box (/v1/geo-within) or GeoJSON
// polygon / multipolygon (/v1/geo-polygon), geohash encode/decode and the
// geo index rebuild with postcode files.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';
import { parsePostcodeLines } from '../lib/postcode-lines';

vi.mock('../lib/mddb-client', () => ({
  default: { geoWithin: vi.fn(), geoPolygon: vi.fn(), geoEncode: vi.fn(), geoDecode: vi.fn(), geoReindex: vi.fn() },
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { default: GeoToolsPanel } = await import('./GeoToolsPanel');

const apiError = (status, text) => Object.assign(new Error(`API Error (${status}): ${text}`), { status, body: { error: text } });
const click = (name) => act(async () => fireEvent.click(screen.getByRole('button', { name })));
const props = { collection: 'venues', lat: 52.23, lng: 21.01, onResults: vi.fn(), onReindexed: vi.fn() };

beforeEach(() => {
  for (const fn of Object.values(mddbClient)) fn.mockReset();
  props.onResults.mockReset();
  props.onReindexed.mockReset();
});
afterEach(cleanup);

describe('parsePostcodeLines', () => {
  test('country and path per line, commas allowed in the path', () => {
    expect(parsePostcodeLines(' PL, /a.csv \n\nGB,/b,c.csv')).toEqual([{ country: 'PL', csvPath: '/a.csv' }, { country: 'GB', csvPath: '/b,c.csv' }]);
  });
});

describe('GeoAreaSearch', () => {
  test('a bounding box search hands the hits on', async () => {
    const results = [{ document: { key: 'berlin' }, rank: 1 }];
    mddbClient.geoWithin.mockResolvedValueOnce({ results, total: 1 }).mockResolvedValueOnce({});
    render(<GeoToolsPanel {...props} />);
    fireEvent.change(screen.getByLabelText('North (max lat)'), { target: { value: '53' } });
    await click('Search area');
    expect(mddbClient.geoWithin).toHaveBeenCalledWith({ collection: 'venues', minLat: 52.4, maxLat: 53, minLng: 13.2, maxLng: 13.6 });
    expect(props.onResults).toHaveBeenCalledWith(results);
    expect(screen.getByRole('status').textContent).toBe('1 documents inside the area.');
    await click('Search area');
    expect(props.onResults).toHaveBeenLastCalledWith([]);
    expect(screen.getByRole('status').textContent).toBe('0 documents inside the area.');
  });

  test('polygon and multipolygon, invalid GeoJSON, no collection and server errors', async () => {
    mddbClient.geoPolygon.mockResolvedValueOnce({ results: [{ rank: 1 }] }).mockResolvedValueOnce({ results: [] }).mockRejectedValueOnce(apiError(400, 'invalid polygon'));
    const { rerender } = render(<GeoToolsPanel {...props} />);
    fireEvent.click(screen.getByLabelText('Polygon'));
    const field = screen.getByLabelText('GeoJSON shape ([lng, lat] pairs)');
    await click('Search area');
    expect(mddbClient.geoPolygon.mock.lastCall[0]).toEqual({ collection: 'venues', polygon: expect.objectContaining({ type: 'Polygon' }) });
    expect(screen.getByRole('status').textContent).toBe('1 documents inside the area.');

    const multi = { type: 'MultiPolygon', coordinates: [[[[0, 0], [1, 0], [1, 1]]]] };
    fireEvent.change(field, { target: { value: JSON.stringify(multi) } });
    await click('Search area');
    expect(mddbClient.geoPolygon).toHaveBeenLastCalledWith({ collection: 'venues', multiPolygon: multi });

    fireEvent.change(field, { target: { value: '[1]' } });
    expect(screen.getByText('A GeoJSON Polygon or MultiPolygon object.')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Search area' }).disabled).toBe(true);
    fireEvent.change(field, { target: { value: '' } });
    expect(screen.getByRole('button', { name: 'Search area' }).disabled).toBe(true);
    fireEvent.change(field, { target: { value: '{"coordinates":[]}' } });
    await click('Search area');
    expect(screen.getByRole('alert').textContent).toBe('API Error (400): invalid polygon');

    rerender(<GeoToolsPanel {...props} collection="" />);
    fireEvent.click(screen.getByLabelText('Bounding box'));
    await click('Search area');
    expect(screen.getByRole('alert').textContent).toBe('Pick a collection first');
  });
});

describe('GeohashTools', () => {
  test('encodes the map point and decodes a geohash', async () => {
    mddbClient.geoEncode.mockResolvedValueOnce({ geohash: 'u3qcnh5j', precision: 6 });
    mddbClient.geoDecode.mockResolvedValueOnce({ lat: 52.2337245, lng: 21.006031, minLat: 52.23363, maxLat: 52.23381, minLng: 21.00585, maxLng: 21.0062 })
      .mockRejectedValueOnce(apiError(400, 'invalid geohash'));
    render(<GeoToolsPanel {...props} />);
    fireEvent.click(screen.getByRole('button', { name: 'Geohash' }));
    expect(screen.getByRole('button', { name: 'Geohash' }).getAttribute('aria-pressed')).toBe('true');
    expect(screen.getByRole('button', { name: 'Decode' }).disabled).toBe(true);
    fireEvent.change(screen.getByLabelText('Precision (1–12)'), { target: { value: '6' } });
    await click('Encode map point');
    expect(mddbClient.geoEncode).toHaveBeenCalledWith({ lat: 52.23, lng: 21.01, precision: 6 });
    expect(screen.getByRole('status').textContent).toBe('52.230000, 21.010000 → u3qcnh5j (precision 6)');
    expect(screen.getByLabelText('Geohash').value).toBe('u3qcnh5j');
    await click('Decode');
    expect(mddbClient.geoDecode).toHaveBeenCalledWith({ geohash: 'u3qcnh5j' });
    expect(screen.getByRole('status').textContent).toBe('u3qcnh5j → 52.233725, 21.006031; cell 52.23363–52.23381 lat, 21.00585–21.00620 lng');
    fireEvent.change(screen.getByLabelText('Geohash'), { target: { value: 'zz!' } });
    await click('Decode');
    expect(screen.getByRole('alert').textContent).toBe('API Error (400): invalid geohash');
  });
});

describe('GeoReindexForm', () => {
  test('rebuilds one collection or all, with postcode files', async () => {
    mddbClient.geoReindex.mockResolvedValueOnce({ points: 2, collection: 'venues', durationMs: 3 }).mockResolvedValueOnce({ points: 9, durationMs: 1 })
      .mockRejectedValueOnce(apiError(403, 'forbidden'));
    const { rerender } = render(<GeoToolsPanel {...props} />);
    fireEvent.click(screen.getByRole('button', { name: 'Reindex' }));
    fireEvent.change(screen.getByLabelText(/Postcode files to load first/), { target: { value: 'PL, /srv/pl.csv' } });
    await click('Rebuild geo index of venues');
    expect(mddbClient.geoReindex).toHaveBeenCalledWith({ collection: 'venues', loadPostcodes: [{ country: 'PL', csvPath: '/srv/pl.csv' }] });
    expect(screen.getByRole('status').textContent).toBe('Rebuilt venues: 2 points in 3 ms.');
    expect(props.onReindexed).toHaveBeenCalledTimes(1);
    rerender(<GeoToolsPanel {...props} collection="" />);
    await click('Rebuild geo index of all collections');
    expect(screen.getByRole('status').textContent).toBe('Rebuilt all collections: 9 points in 1 ms.');
    await click('Rebuild geo index of all collections');
    expect(screen.getByText('Not permitted')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Area' }));
    expect(screen.getByRole('button', { name: 'Search area' })).toBeTruthy();
  });
});
