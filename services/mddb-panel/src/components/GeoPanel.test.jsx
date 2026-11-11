// @vitest-environment jsdom
// GeoPanel with Leaflet replaced by a recorder: the collection list from the
// stats, the map click setting the centre, radius search drawing pins and
// listing hits, errors, index stats, and the tools (area search) feeding the
// same map and list.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { StrictMode } from 'react';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';

vi.mock('leaflet/dist/leaflet.css', () => ({}));
vi.mock('leaflet', () => {
  const made = { handlers: {}, pins: [] };
  const layer = () => {
    const o = { addTo: () => o, remove: vi.fn(), clearLayers: vi.fn(), bindTooltip: vi.fn((t) => { o.tooltip = t; }), on: vi.fn((ev, fn) => { o[ev] = fn; }) };
    return o;
  };
  const map = { setView: () => map, on: (ev, fn) => { made.handlers[ev] = fn; } };
  return {
    default: {
      made,
      map: () => map,
      tileLayer: layer,
      marker: layer,
      circle: layer,
      layerGroup: () => { made.group = layer(); return made.group; },
      circleMarker: (latlng) => { const pin = layer(); pin.latlng = latlng; made.pins.push(pin); return pin; },
      Icon: { Default: { prototype: { _getIconUrl: () => '' }, mergeOptions: () => {} } },
    },
  };
});
vi.mock('../lib/mddb-client', () => ({ default: { geoStats: vi.fn(), geoSearch: vi.fn(), geoWithin: vi.fn(), geoReindex: vi.fn() } }));

const { default: L } = await import('leaflet');
const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: GeoPanel } = await import('./GeoPanel');

const initialStore = useStore.getState();
const hit = (key, lat, lng, distanceMeters, rank) => ({ document: { id: `geo|${key}|en`, key, meta: { geo_lat: [lat], geo_lng: [lng] } }, distanceMeters, rank });

async function renderPanel() {
  // StrictMode runs the map set-up twice; the second run must do nothing.
  render(<StrictMode><GeoPanel /></StrictMode>);
  await act(async () => {});
}

beforeEach(() => {
  useStore.setState(initialStore, true);
  useStore.setState({ stats: { collections: [{ name: 'venues' }, { name: 'agenttest-geo' }] } });
  L.made.pins = [];
  for (const fn of Object.values(mddbClient)) fn.mockReset();
  mddbClient.geoStats.mockResolvedValue({ collections: { venues: { points: 2 } }, ready: true });
});
afterEach(cleanup);

describe('GeoPanel', () => {
  test('lists the collections by name and the geo stats; a map click moves the centre', async () => {
    await renderPanel();
    const select = screen.getByLabelText('Collection');
    expect([...select.options].map((o) => o.value)).toEqual(['', 'agenttest-geo', 'venues']);
    expect(screen.getByText('1 geo-enabled collections')).toBeTruthy();
    act(() => L.made.handlers.click({ latlng: { lat: 1.5, lng: 2.5 } }));
    expect(screen.getByDisplayValue('1.5')).toBeTruthy();
    expect(screen.getByDisplayValue('2.5')).toBeTruthy();
  });

  test('radius search needs a collection, then draws pins and lists hits that open the document', async () => {
    mddbClient.geoSearch.mockResolvedValueOnce({ results: [hit('cafe', '52.52', '13.40', 342.7, 1), hit('far', '52.6', '13.5', 2500, 2), { document: { key: 'nowhere', meta: {} }, rank: 3 }, { rank: 4 }] });
    await renderPanel();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Search' })));
    expect(screen.getByText('Pick a collection first')).toBeTruthy();

    fireEvent.change(screen.getByLabelText('Collection'), { target: { value: 'venues' } });
    fireEvent.change(screen.getByLabelText('Lat'), { target: { value: '52.5' } });
    fireEvent.change(screen.getByLabelText('Lng'), { target: { value: '13.4' } });
    fireEvent.change(screen.getByRole('slider'), { target: { value: '800' } });
    expect(screen.getByText('Radius: 800 m')).toBeTruthy();
    fireEvent.change(screen.getByLabelText('Algorithm'), { target: { value: 'geohash' } });
    fireEvent.change(screen.getByLabelText('Top K'), { target: { value: '5' } });
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Search' })));
    expect(mddbClient.geoSearch).toHaveBeenCalledWith({ collection: 'venues', lat: 52.5, lng: 13.4, radiusMeters: 800, topK: 5, algorithm: 'geohash' });
    expect(L.made.pins.map((p) => p.latlng)).toEqual([[52.52, 13.4], [52.6, 13.5]]);
    expect(L.made.pins[0].tooltip).toBe('cafe · 343 m');
    act(() => L.made.pins[0].click());
    expect(useStore.getState().currentDocument.key).toBe('cafe');
    expect(screen.getByText('343 m')).toBeTruthy();
    expect(screen.getByText('2.50 km')).toBeTruthy();
    fireEvent.click(screen.getByText('far'));
    expect(useStore.getState().currentDocument.key).toBe('far');
  });

  test('a failed search empties the list; a pin without a key shows its id', async () => {
    mddbClient.geoSearch
      .mockResolvedValueOnce({ results: [{ document: { id: 'geo|x|en', meta: { geo_lat: ['1'], geo_lng: ['2'] } }, rank: 1 }] })
      .mockResolvedValueOnce({})
      .mockRejectedValueOnce(new Error('API Error (503): geo index loading'))
      .mockRejectedValueOnce('plain failure');
    await renderPanel();
    fireEvent.change(screen.getByLabelText('Collection'), { target: { value: 'venues' } });
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Search' })));
    expect(L.made.pins[0].tooltip).toBe('geo|x|en · 0 m');
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Search' })));
    expect(screen.getByText(/No results yet/)).toBeTruthy();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Search' })));
    expect(screen.getByText('API Error (503): geo index loading')).toBeTruthy();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Search' })));
    expect(screen.getByText('plain failure')).toBeTruthy();
  });

  test('refreshes the stats, shows a loading index and survives failed stats; radius in km', async () => {
    mddbClient.geoStats.mockReset().mockResolvedValue({ ready: false });
    useStore.setState({ stats: null, currentCollection: 'venues' });
    await renderPanel();
    expect(screen.getByText('Index loading…')).toBeTruthy();
    expect(screen.getByText('0 geo-enabled collections')).toBeTruthy();
    mddbClient.geoStats.mockRejectedValueOnce(new Error('down'));
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Refresh stats' })));
    expect(screen.getByText('Index loading…')).toBeTruthy();
    expect(screen.getByText('Radius: 5.0 km')).toBeTruthy();
  });

  test('a geo index rebuild from the tools reloads the stats', async () => {
    mddbClient.geoReindex.mockResolvedValueOnce({ points: 2, collection: '', durationMs: 0 });
    await renderPanel();
    const before = mddbClient.geoStats.mock.calls.length;
    fireEvent.click(screen.getByRole('button', { name: 'Reindex' }));
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Rebuild geo index of all collections' })));
    expect(mddbClient.geoStats).toHaveBeenCalledTimes(before + 1);
  });

  test('area search results land on the same map and list', async () => {
    mddbClient.geoWithin.mockResolvedValueOnce({ results: [hit('berlin', '52.52', '13.405', undefined, 1)], total: 1 });
    await renderPanel();
    fireEvent.change(screen.getByLabelText('Collection'), { target: { value: 'venues' } });
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Search area' })));
    expect(mddbClient.geoWithin).toHaveBeenCalledWith({ collection: 'venues', minLat: 52.4, maxLat: 52.6, minLng: 13.2, maxLng: 13.6 });
    expect(screen.getByText('berlin')).toBeTruthy();
    expect(L.made.pins).toHaveLength(1);
  });
});
