import { useCallback, useEffect, useMemo, useState } from 'react';
import { ScatterChart, RefreshCw, AlertCircle } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useStore } from '../lib/store';

// WCAG 2.2 AA-checked point palette on white background (contrast >= 3:1
// for graphical objects), cycled per document.
const POINT_COLORS = [
  '#1d4ed8', // blue-700
  '#b91c1c', // red-700
  '#047857', // emerald-700
  '#7c3aed', // violet-600
  '#b45309', // amber-700
  '#0e7490', // cyan-700
  '#be185d', // pink-700
  '#4d7c0f', // lime-700
];

const PLOT_SIZE = 640;
const PLOT_PAD = 24;

export default function VectorSpacePanel() {
  const { currentCollection } = useStore();
  const [collection, setCollection] = useState(currentCollection || '');
  const [sample, setSample] = useState(1000);
  const [query, setQuery] = useState('');
  const [data, setData] = useState(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);
  const [hovered, setHovered] = useState(null);
  const [selected, setSelected] = useState(null);

  const load = useCallback(async () => {
    if (!collection) return;
    setLoading(true);
    setError(null);
    setSelected(null);
    try {
      const result = await mddbClient.vectorProjection({ collection, sample, query: query || undefined });
      setData(result);
    } catch (err) {
      setError(err.message);
      setData(null);
    } finally {
      setLoading(false);
    }
  }, [collection, sample, query]);

  useEffect(() => {
    if (currentCollection && !collection) setCollection(currentCollection);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentCollection]);

  // Map projected coordinates into the SVG viewport.
  const plotted = useMemo(() => {
    if (!data?.points?.length) return { points: [], query: null };
    const all = data.query ? [...data.points, data.query] : data.points;
    const xs = all.map((p) => p.x);
    const ys = all.map((p) => p.y);
    const minX = Math.min(...xs);
    const maxX = Math.max(...xs);
    const minY = Math.min(...ys);
    const maxY = Math.max(...ys);
    const spanX = maxX - minX || 1;
    const spanY = maxY - minY || 1;
    const scale = (p) => ({
      ...p,
      px: PLOT_PAD + ((p.x - minX) / spanX) * (PLOT_SIZE - 2 * PLOT_PAD),
      py: PLOT_PAD + ((p.y - minY) / spanY) * (PLOT_SIZE - 2 * PLOT_PAD),
    });
    // Stable color per parent document
    const docColor = new Map();
    for (const p of data.points) {
      if (!docColor.has(p.docId)) docColor.set(p.docId, POINT_COLORS[docColor.size % POINT_COLORS.length]);
    }
    return {
      points: data.points.map((p) => ({ ...scale(p), color: docColor.get(p.docId) })),
      query: data.query ? scale(data.query) : null,
    };
  }, [data]);

  return (
    <div className="h-full overflow-y-auto p-6">
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-lg font-semibold text-gray-900 flex items-center gap-2">
          <ScatterChart className="w-5 h-5 text-blue-600" aria-hidden="true" />
          Vector Space
        </h2>
        <button
          onClick={load}
          disabled={loading || !collection}
          className="flex items-center gap-2 px-3 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50 text-sm font-medium"
        >
          <RefreshCw className={`w-4 h-4 ${loading ? 'animate-spin' : ''}`} aria-hidden="true" />
          {loading ? 'Projecting…' : 'Project'}
        </button>
      </div>

      <p className="text-sm text-gray-600 mb-4">
        2D PCA projection of the collection&apos;s embeddings. Each dot is a document chunk;
        colors group chunks of the same document. Optionally overlay a search query to see
        why results match.
      </p>

      <div className="flex flex-wrap gap-3 mb-4">
        <label className="flex flex-col text-xs font-medium text-gray-700">
          <span>Collection</span>
          <input
            type="text"
            value={collection}
            onChange={(e) => setCollection(e.target.value)}
            placeholder="collection name"
            className="mt-1 px-3 py-2 border border-gray-300 rounded-lg text-sm w-48"
          />
        </label>
        <label className="flex flex-col text-xs font-medium text-gray-700">
          <span>Sample (max 2000)</span>
          <input
            type="number"
            min="10"
            max="2000"
            value={sample}
            onChange={(e) => setSample(Number(e.target.value))}
            className="mt-1 px-3 py-2 border border-gray-300 rounded-lg text-sm w-32"
          />
        </label>
        <label className="flex flex-col text-xs font-medium text-gray-700 flex-1 min-w-48">
          <span>Query overlay (optional)</span>
          <input
            type="text"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="natural language query to project"
            className="mt-1 px-3 py-2 border border-gray-300 rounded-lg text-sm"
          />
        </label>
      </div>

      {error && (
        <div className="flex items-center gap-2 p-3 mb-4 bg-red-50 border border-red-200 rounded-lg text-sm text-red-800">
          <AlertCircle className="w-4 h-4 flex-shrink-0" aria-hidden="true" />
          {error}
        </div>
      )}

      {data && (
        <div className="text-xs text-gray-600 mb-2">
          {data.sampled} of {data.total} vectors projected · {data.dimensions} dimensions
          {data.sampled < data.total && ' (sampled)'}
        </div>
      )}

      <div className="flex gap-4 items-start flex-wrap">
        <svg
          viewBox={`0 0 ${PLOT_SIZE} ${PLOT_SIZE}`}
          className="border border-gray-200 rounded-lg bg-white max-w-full"
          width={PLOT_SIZE}
          height={PLOT_SIZE}
          role="img"
          aria-label="2D projection of embedding vectors"
        >
          {plotted.points.map((p) => (
            <circle
              key={p.id}
              cx={p.px}
              cy={p.py}
              r={hovered?.id === p.id || selected?.id === p.id ? 7 : 4}
              fill={p.color}
              fillOpacity={0.75}
              stroke={selected?.id === p.id ? '#111827' : 'white'}
              strokeWidth={selected?.id === p.id ? 2 : 1}
              tabIndex={0}
              onMouseEnter={() => setHovered(p)}
              onMouseLeave={() => setHovered(null)}
              onFocus={() => setHovered(p)}
              onBlur={() => setHovered(null)}
              onClick={() => setSelected(p)}
              onKeyDown={(e) => e.key === 'Enter' && setSelected(p)}
              style={{ cursor: 'pointer' }}
            >
              <title>{`${p.key || p.docId} (chunk ${p.chunkIndex})`}</title>
            </circle>
          ))}
          {plotted.query && (
            <g>
              <circle cx={plotted.query.px} cy={plotted.query.py} r={9} fill="none" stroke="#111827" strokeWidth={2} />
              <line x1={plotted.query.px - 12} y1={plotted.query.py} x2={plotted.query.px + 12} y2={plotted.query.py} stroke="#111827" strokeWidth={2} />
              <line x1={plotted.query.px} y1={plotted.query.py - 12} x2={plotted.query.px} y2={plotted.query.py + 12} stroke="#111827" strokeWidth={2} />
              <title>Query position</title>
            </g>
          )}
        </svg>

        <div className="flex-1 min-w-64">
          {(hovered || selected) && (
            <div className="p-4 bg-gray-50 border border-gray-200 rounded-lg text-sm">
              <div className="font-medium text-gray-900 break-all">
                {(hovered || selected).key || (hovered || selected).docId}
              </div>
              <div className="text-gray-600 mt-1">
                Chunk {(hovered || selected).chunkIndex} · doc {(hovered || selected).docId}
              </div>
              <div className="text-gray-500 mt-1 text-xs">
                ({(hovered || selected).x.toFixed(4)}, {(hovered || selected).y.toFixed(4)})
              </div>
            </div>
          )}
          {!hovered && !selected && data && (
            <div className="p-4 text-sm text-gray-500 border border-dashed border-gray-200 rounded-lg">
              Hover or click a point to inspect it. Clustered points are semantically similar;
              isolated points are outliers. The crosshair marks the projected query.
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
