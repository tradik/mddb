/**
 * SpellSuggestionBadge renders an inline spell-correction indicator.
 * Shown in FTSSearchPanel when the server auto-corrected the query.
 */
export default function SpellSuggestionBadge({ original, corrected }) {
  if (!original || !corrected || original === corrected) return null;

  return (
    <div className="flex items-center gap-1.5 text-xs text-warning bg-warning-soft border border-warning-line rounded px-2.5 py-1.5 mb-3">
      <svg className="w-3.5 h-3.5 flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
      </svg>
      <span>
        Searched for <span className="font-medium">&ldquo;{corrected}&rdquo;</span>{' '}
        instead of <span className="line-through opacity-60">&ldquo;{original}&rdquo;</span>
      </span>
    </div>
  );
}
