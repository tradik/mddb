import { useState } from 'react';
import mddbClient from '../lib/mddb-client';
import { useAction } from '../lib/use-action';
import { parseJson, jsonObjectError } from '../lib/json-text';
import ApiErrorNotice from './ApiErrorNotice';
import JsonTextarea from './JsonTextarea';

const POLYGON_EXAMPLE = '{\n  "type": "Polygon",\n  "coordinates": [[[13.36, 52.51], [13.42, 52.51], [13.42, 52.53], [13.36, 52.53]]]\n}';
const NOT_OBJECT = 'A GeoJSON Polygon or MultiPolygon object.';
const BOX = [['minLat', 'South (min lat)'], ['maxLat', 'North (max lat)'], ['minLng', 'West (min lng)'], ['maxLng', 'East (max lng)']];

/**
 * GeoAreaSearch finds the documents inside an area instead of a radius: a
 * bounding box (/v1/geo-within) or a GeoJSON Polygon / MultiPolygon with
 * [lng, lat] coordinates (/v1/geo-polygon). Neither crosses the
 * anti-meridian. `onResults` receives the hits for the map and the list.
 */
export default function GeoAreaSearch({ collection, onResults }) {
  const [shape, setShape] = useState('box');
  const [box, setBox] = useState({ minLat: '52.4', maxLat: '52.6', minLng: '13.2', maxLng: '13.6' });
  const [geojson, setGeojson] = useState(POLYGON_EXAMPLE);
  const [total, setTotal] = useState(null);
  const { busy, error, run, setError } = useAction();
  const polygonError = jsonObjectError(geojson, NOT_OBJECT);

  const search = () => run(async () => {
    if (!collection) {
      setError(new Error('Pick a collection first'));
      return;
    }
    let answer;
    if (shape === 'box') {
      answer = await mddbClient.geoWithin({ collection, ...Object.fromEntries(Object.entries(box).map(([k, v]) => [k, Number(v)])) });
    } else {
      const value = parseJson(geojson).value;
      answer = await mddbClient.geoPolygon(value.type === 'MultiPolygon' ? { collection, multiPolygon: value } : { collection, polygon: value });
    }
    setTotal(answer.total ?? (answer.results || []).length);
    onResults(answer.results || []);
  });

  return (
    <div className="space-y-2 text-sm">
      <fieldset className="flex gap-3 text-gray-800">
        <legend className="sr-only">Area shape</legend>
        <label className="inline-flex items-center gap-1"><input type="radio" name="geo-shape" checked={shape === 'box'} onChange={() => setShape('box')} /> Bounding box</label>
        <label className="inline-flex items-center gap-1"><input type="radio" name="geo-shape" checked={shape === 'polygon'} onChange={() => setShape('polygon')} /> Polygon</label>
      </fieldset>
      {shape === 'box' ? (
        <div className="grid grid-cols-2 gap-2">
          {BOX.map(([name, label]) => (
            <label key={name} className="text-xs text-gray-800">
              <span className="block mb-0.5">{label}</span>
              <input type="number" step="0.000001" value={box[name]} onChange={(e) => setBox({ ...box, [name]: e.target.value })} className="w-full border border-gray-300 rounded px-2 py-1" />
            </label>
          ))}
        </div>
      ) : (
        <JsonTextarea label="GeoJSON shape ([lng, lat] pairs)" value={geojson} onChange={setGeojson} error={polygonError} rows={6} />
      )}
      <button type="button" onClick={search} disabled={busy || (shape === 'polygon' && (Boolean(polygonError) || !geojson.trim()))} className="w-full bg-blue-700 hover:bg-blue-800 text-white rounded px-3 py-1.5 disabled:opacity-50">
        Search area
      </button>
      <ApiErrorNotice error={error} collection={collection || '*'} />
      {total !== null && <p role="status" className="text-xs text-gray-800">{total} documents inside the area.</p>}
    </div>
  );
}
