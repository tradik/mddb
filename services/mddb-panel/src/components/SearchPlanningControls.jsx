// Options of a vector-backed search that change how it runs, not what it
// matches: `explain` (SRCH-013) returns the plan, and `queryDecomposition:
// "auto"` (SRCH-020) searches each part of a question with several needs as
// well as the whole. Cross-collection search has no decomposition, so that
// box shows only when its handler is given.
export default function SearchPlanningControls({ explain, onExplainChange, decomposition, onDecompositionChange }) {
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-sm text-gray-700">
      <label className="flex items-center gap-2">
        <input
          type="checkbox"
          checked={explain}
          onChange={(e) => onExplainChange(e.target.checked)}
          className="rounded border-gray-300 text-primary-600 focus:ring-primary-500"
        />
        <span>Explain plan</span>
      </label>
      {onDecompositionChange && (
        <label className="flex items-center gap-2" title="Splits at sentence punctuation and at “and” between clauses (at most 4 parts); each part costs one embedding call.">
          <input
            type="checkbox"
            checked={decomposition === 'auto'}
            onChange={(e) => onDecompositionChange(e.target.checked ? 'auto' : 'off')}
            className="rounded border-gray-300 text-primary-600 focus:ring-primary-500"
          />
          <span>Split multi-part questions</span>
        </label>
      )}
    </div>
  );
}
