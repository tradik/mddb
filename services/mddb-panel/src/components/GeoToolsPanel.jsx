import { useState } from 'react';
import GeoAreaSearch from './GeoAreaSearch';
import GeohashTools from './GeohashTools';
import GeoReindexForm from './GeoReindexForm';

const TOOLS = [
  { id: 'area', label: 'Area' },
  { id: 'geohash', label: 'Geohash' },
  { id: 'reindex', label: 'Reindex' },
];

/**
 * GeoToolsPanel holds the geo tools beside the radius search: area search
 * (bounding box or polygon), geohash conversion and the index rebuild, one
 * at a time behind a row of toggle buttons.
 */
export default function GeoToolsPanel({ collection, lat, lng, onResults, onReindexed }) {
  const [tool, setTool] = useState('area');

  return (
    <div className="p-4 border-b border-gray-200 space-y-2">
      <div role="group" aria-label="Geo tools" className="flex gap-1">
        {TOOLS.map((t) => (
          <button
            key={t.id}
            type="button"
            aria-pressed={tool === t.id}
            onClick={() => setTool(t.id)}
            className={`px-2 py-1 text-xs rounded ${tool === t.id ? 'bg-blue-100 text-blue-900' : 'text-gray-800 hover:bg-gray-100'}`}
          >
            {t.label}
          </button>
        ))}
      </div>
      {tool === 'area' && <GeoAreaSearch collection={collection} onResults={onResults} />}
      {tool === 'geohash' && <GeohashTools lat={lat} lng={lng} />}
      {tool === 'reindex' && <GeoReindexForm collection={collection} onDone={onReindexed} />}
    </div>
  );
}
