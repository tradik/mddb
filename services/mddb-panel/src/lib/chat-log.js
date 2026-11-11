/**
 * The chat conversation log (#406): the Log tab's filter state and its
 * translation to the query of GET /v1/chat/log, /v1/chat/log/export and
 * /v1/chat/gaps, and how a logged turn is shown.
 */

export const outcomeLabels = {
  answered: 'Answered',
  noResults: 'No results',
  refused: 'Refused / off-topic',
  error: 'Error',
};

/** The filters of a fresh Log tab: everything, newest first. */
export const emptyLogFilters = {
  scenario: '',
  from: '',
  to: '',
  thumbsDown: false,
  noResults: false,
  refused: false,
  outcome: '',
};

/**
 * The query of a filter state. "No results" and "Refused" are shortcuts for
 * the outcome, and win over the outcome list in that order (the server
 * filters by one outcome). Dates go as YYYY-MM-DD: the server reads a `to`
 * date as that whole day.
 */
export function logParams(filters) {
  let outcome = filters.outcome;
  if (filters.refused) outcome = 'refused';
  if (filters.noResults) outcome = 'noResults';
  return {
    scenario: filters.scenario,
    from: filters.from,
    to: filters.to,
    rating: filters.thumbsDown ? 'down' : '',
    outcome,
  };
}

/** The query of the docs gaps view: the window, minCount and threshold. */
export function gapParams(filters, { minCount, threshold }) {
  return { scenario: filters.scenario, from: filters.from, to: filters.to, minCount, threshold };
}

/** Who asked: a signed user's name and id, or "anonymous". */
export function whoOf(turn) {
  if (turn.anonymous || !turn.sub) return 'anonymous';
  return turn.displayName ? `${turn.displayName} (${turn.sub})` : turn.sub;
}

/** A rating as text for a table cell and a screen reader. */
export function ratingText(rating) {
  if (rating === 'up') return 'Thumbs up';
  if (rating === 'down') return 'Thumbs down';
  return '—';
}

/** A tool call's best score with two decimals, or "—" when it found nothing. */
export function scoreText(call) {
  return call.resultKeys?.length ? Number(call.topScore || 0).toFixed(2) : '—';
}
