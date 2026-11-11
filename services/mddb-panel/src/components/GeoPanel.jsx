import { useEffect, useId, useRef, useState, useMemo } from 'react';
import { MapPin, Search, RefreshCw, Loader } from 'lucide-react';
import L from 'leaflet';
import 'leaflet/dist/leaflet.css';
import { useStore } from '../lib/store';
import mddbClient from '../lib/mddb-client';
import GeoToolsPanel from './GeoToolsPanel';

// Leaflet ships its marker icons as relative assets. Vite bundles them
// but can't auto-resolve them from Leaflet's internal paths — the idiomatic
// fix is to explicitly set the icon URLs up-front.
// Use CDN icons so we don't need to copy binary assets into our build.
delete L.Icon.Default.prototype._getIconUrl;
L.Icon.Default.mergeOptions({
  iconRetinaUrl: 'https://unpkg.com/leaflet@1.9.4/dist/images/marker-icon-2x.png',
  iconUrl: 'https://unpkg.com/leaflet@1.9.4/dist/images/marker-icon.png',
  shadowUrl: 'https://unpkg.com/leaflet@1.9.4/dist/images/marker-shadow.png',
});

// GeoPanel provides a Leaflet-backed UI for the /v1/geo-search and
// /v1/geo-within endpoints. Click the map to set the query center,
// enter a radius, and hit search. Matching documents are drawn as
// pins and listed in the right-hand panel; clicking a result opens
// it in the shared DocumentViewer (via the same currentDocument slot
// used by CrossSearchPanel).
export default function GeoPanel() {
  const { currentCollection, setCurrentDocument, stats } = useStore();
  const [collection, setCollection] = useState(currentCollection || '');
  const [lat, setLat] = useState(52.52);
  const [lng, setLng] = useState(13.405);
  const [radiusMeters, setRadiusMeters] = useState(5000);
  const [algorithm, setAlgorithm] = useState('rtree');
  const [topK, setTopK] = useState(25);
  const [results, setResults] = useState([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);
  const [geoStats, setGeoStats] = useState(null);
  const fieldId = useId();

  const mapEl = useRef(null);
  const mapRef = useRef(null);
  const queryMarker = useRef(null);
  const radiusCircle = useRef(null);
  const resultLayer = useRef(null);

  const collections = useMemo(() => {
    // stats.collections is a list of { name, documentCount }.
    return (stats?.collections || []).map((c) => c.name).sort();
  }, [stats]);

  // One-time map init.
  useEffect(() => {
    // The map div is always rendered; StrictMode runs this twice.
    if (mapRef.current) return;
    const map = L.map(mapEl.current).setView([lat, lng], 11);
    L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', {
      maxZoom: 19,
      attribution: '&copy; OpenStreetMap contributors',
    }).addTo(map);
    map.on('click', (e) => {
      setLat(e.latlng.lat);
      setLng(e.latlng.lng);
    });
    mapRef.current = map;
    resultLayer.current = L.layerGroup().addTo(map);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Sync query marker + radius circle whenever lat/lng/radius change.
  useEffect(() => {
    // Runs after the init effect above, so the map exists.
    const map = mapRef.current;
    if (queryMarker.current) queryMarker.current.remove();
    if (radiusCircle.current) radiusCircle.current.remove();
    queryMarker.current = L.marker([lat, lng], { title: 'query center' }).addTo(map);
    radiusCircle.current = L.circle([lat, lng], {
      radius: radiusMeters,
      color: '#2563eb',
      fillColor: '#3b82f6',
      fillOpacity: 0.1,
    }).addTo(map);
  }, [lat, lng, radiusMeters]);

  // Index stats: on mount, on Refresh stats and after a rebuild. A failure
  // keeps the last stats.
  const loadGeoStats = () => mddbClient.geoStats().then(setGeoStats).catch(() => {});
  useEffect(() => {
    loadGeoStats();
  }, []);

  const runSearch = async () => {
    if (!collection) {
      setError('Pick a collection first');
      return;
    }
    setLoading(true);
    setError(null);
    try {
      const resp = await mddbClient.geoSearch({
        collection,
        lat,
        lng,
        radiusMeters,
        topK,
        algorithm,
      });
      setResults(resp.results || []);
      drawResults(resp.results || []);
    } catch (e) {
      setError(e.message || String(e));
      setResults([]);
    } finally {
      setLoading(false);
    }
  };

  const drawResults = (items) => {
    const layer = resultLayer.current;
    layer.clearLayers();
    items.forEach((item) => {
      const meta = item.document?.meta || {};
      const pLat = parseFloat((meta.geo_lat || [])[0]);
      const pLng = parseFloat((meta.geo_lng || [])[0]);
      if (Number.isNaN(pLat) || Number.isNaN(pLng)) return;
      const marker = L.circleMarker([pLat, pLng], {
        radius: 6,
        color: '#059669',
        fillColor: '#10b981',
        fillOpacity: 0.9,
      });
      marker.bindTooltip(
        `${item.document?.key || item.document?.id} · ${Math.round(item.distanceMeters || 0)} m`
      );
      marker.on('click', () => setCurrentDocument(item.document));
      marker.addTo(layer);
    });
  };

  return (
    <div className="flex flex-col h-full bg-white">
      <div className="px-4 py-3 border-b border-gray-200 flex items-center gap-2">
        <MapPin className="w-5 h-5 text-blue-600" />
        <h2 className="text-lg font-semibold">Geo Search</h2>
        {geoStats && (
          <span className="ml-auto text-xs text-gray-500">
            {Object.keys(geoStats.collections || {}).length} geo-enabled collections
          </span>
        )}
      </div>

      <div className="flex flex-1 overflow-hidden">
        {/* Left: map */}
        <div className="flex-1 flex flex-col border-r border-gray-200">
          <div ref={mapEl} className="flex-1" style={{ minHeight: 400 }} />
          <div className="px-4 py-3 border-t border-gray-200 text-xs text-gray-500">
            Click the map to set the query center. Drag sliders to change the radius.
          </div>
        </div>

        {/* Right: form + results */}
        <div className="w-96 flex flex-col overflow-hidden">
          <div className="p-4 border-b border-gray-200 space-y-3">
            <div>
              <label htmlFor={`${fieldId}-collection`} className="block text-xs font-medium text-gray-600 mb-1">Collection</label>
              <select
                id={`${fieldId}-collection`}
                value={collection}
                onChange={(e) => setCollection(e.target.value)}
                className="w-full border border-gray-300 rounded px-2 py-1.5 text-sm"
              >
                <option value="">— pick one —</option>
                {collections.map((c) => (
                  <option key={c} value={c}>
                    {c}
                  </option>
                ))}
              </select>
            </div>

            <div className="grid grid-cols-2 gap-2">
              <div>
                <label htmlFor={`${fieldId}-lat`} className="block text-xs font-medium text-gray-600 mb-1">Lat</label>
                <input
                  id={`${fieldId}-lat`}
                  type="number"
                  step="0.000001"
                  value={lat}
                  onChange={(e) => setLat(parseFloat(e.target.value))}
                  className="w-full border border-gray-300 rounded px-2 py-1.5 text-sm"
                />
              </div>
              <div>
                <label htmlFor={`${fieldId}-lng`} className="block text-xs font-medium text-gray-600 mb-1">Lng</label>
                <input
                  id={`${fieldId}-lng`}
                  type="number"
                  step="0.000001"
                  value={lng}
                  onChange={(e) => setLng(parseFloat(e.target.value))}
                  className="w-full border border-gray-300 rounded px-2 py-1.5 text-sm"
                />
              </div>
            </div>

            <div>
              <label htmlFor={`${fieldId}-radius`} className="block text-xs font-medium text-gray-600 mb-1">
                Radius: {radiusMeters >= 1000 ? `${(radiusMeters / 1000).toFixed(1)} km` : `${radiusMeters} m`}
              </label>
              <input
                id={`${fieldId}-radius`}
                type="range"
                min={100}
                max={100000}
                step={100}
                value={radiusMeters}
                onChange={(e) => setRadiusMeters(parseInt(e.target.value, 10))}
                className="w-full"
              />
            </div>

            <div className="grid grid-cols-2 gap-2">
              <div>
                <label htmlFor={`${fieldId}-algorithm`} className="block text-xs font-medium text-gray-600 mb-1">Algorithm</label>
                <select
                  id={`${fieldId}-algorithm`}
                  value={algorithm}
                  onChange={(e) => setAlgorithm(e.target.value)}
                  className="w-full border border-gray-300 rounded px-2 py-1.5 text-sm"
                >
                  <option value="rtree">R-tree (default)</option>
                  <option value="geohash">Geohash</option>
                </select>
              </div>
              <div>
                <label htmlFor={`${fieldId}-topk`} className="block text-xs font-medium text-gray-600 mb-1">Top K</label>
                <input
                  id={`${fieldId}-topk`}
                  type="number"
                  min={1}
                  max={500}
                  value={topK}
                  onChange={(e) => setTopK(parseInt(e.target.value, 10))}
                  className="w-full border border-gray-300 rounded px-2 py-1.5 text-sm"
                />
              </div>
            </div>

            <button
              onClick={runSearch}
              disabled={loading}
              className="w-full flex items-center justify-center gap-2 bg-blue-600 hover:bg-blue-700 text-white rounded px-3 py-2 text-sm font-medium disabled:opacity-50"
            >
              {loading ? <Loader className="w-4 h-4 animate-spin" /> : <Search className="w-4 h-4" />}
              Search
            </button>

            {error && (
              <div className="text-xs text-red-600 bg-red-50 border border-red-200 rounded px-2 py-1.5">
                {error}
              </div>
            )}
          </div>

          {/* 2.19.0: area search, geohash conversion and index rebuild */}
          <GeoToolsPanel
            collection={collection}
            lat={lat}
            lng={lng}
            onResults={(items) => { setResults(items); drawResults(items); }}
            onReindexed={loadGeoStats}
          />

          <div className="flex-1 overflow-y-auto">
            {results.length === 0 && !loading && (
              <div className="p-4 text-sm text-gray-500 text-center">
                No results yet. Click the map to set a center and hit Search.
              </div>
            )}
            {results.map((item) => (
              <button
                key={item.document?.id || item.rank}
                onClick={() => setCurrentDocument(item.document)}
                className="w-full text-left px-4 py-2 border-b border-gray-100 hover:bg-gray-50"
              >
                <div className="text-sm font-medium text-gray-900 truncate">
                  {item.document?.key || item.document?.id}
                </div>
                <div className="text-xs text-gray-500 flex items-center justify-between">
                  <span>#{item.rank}</span>
                  {item.distanceMeters != null && (
                    <span>{formatDistance(item.distanceMeters)}</span>
                  )}
                </div>
              </button>
            ))}
          </div>

          <div className="px-4 py-2 border-t border-gray-200 flex items-center gap-2">
            <button
              onClick={loadGeoStats}
              className="text-xs text-gray-500 hover:text-gray-700 flex items-center gap-1"
            >
              <RefreshCw className="w-3 h-3" /> Refresh stats
            </button>
            {geoStats?.ready === false && (
              <span className="text-xs text-orange-600">Index loading…</span>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

function formatDistance(meters) {
  if (meters < 1000) return `${Math.round(meters)} m`;
  return `${(meters / 1000).toFixed(2)} km`;
}
