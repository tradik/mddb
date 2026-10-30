import { Route } from 'lucide-react';

// SRCH-013: with `explain`, a vector-backed search returns how it ran and why:
// the index `algorithm: "auto"` chose (or the one named), the filter strategy,
// the candidates asked for, relevance feedback (SRCH-015) and how the query
// was split (SRCH-020). A cross-collection search has one plan per target.

function PlanBody({ plan }) {
  const { feedback, decomposition } = plan;
  return (
    <>
      <dl className="grid grid-cols-[auto,1fr] gap-x-3 gap-y-0.5 text-xs">
        <dt className="text-gray-600">Index</dt>
        <dd className="font-mono font-semibold text-gray-900">{plan.algorithm}</dd>
        <dt className="text-gray-600">Filter</dt>
        <dd className="text-gray-900">{plan.prefilter ? 'applied first, exact scan' : 'after the index search'}</dd>
        <dt className="text-gray-600">Candidates</dt>
        <dd className="text-gray-900">
          {plan.candidates}
          {plan.iterativeScan && ' (scan widened: the filtered index search came back short)'}
        </dd>
        {feedback && (
          <>
            <dt className="text-gray-600">Feedback</dt>
            <dd className="text-gray-900">
              {feedback.mode}: {feedback.positive} positive, {feedback.negative} negative
              {feedback.mode === 'query'
                ? `, query moved ${(feedback.queryShift ?? 0).toFixed(3)}`
                : `, ${feedback.rescored ?? 0} rescored`}
            </dd>
          </>
        )}
        {decomposition && (
          <>
            <dt className="text-gray-600">Query parts</dt>
            <dd className="text-gray-900">
              {decomposition.parts > 1 ? decomposition.texts.join(' · ') : 'not split'}
            </dd>
          </>
        )}
      </dl>
      {plan.reasons?.length > 0 && (
        <ul className="mt-1.5 list-disc space-y-0.5 pl-4 text-xs text-gray-700">
          {plan.reasons.map((reason) => (
            <li key={reason}>{reason}</li>
          ))}
        </ul>
      )}
    </>
  );
}

export default function SearchPlan({ plan }) {
  if (!plan) return null;
  const targets = Object.entries(plan.collections || {});
  return (
    <section aria-label="Search plan" className="mx-4 mt-3 rounded-lg border border-primary-200 bg-primary-50 p-3">
      <h4 className="mb-1.5 flex items-center gap-1.5 text-xs font-semibold text-primary-900">
        <Route className="h-3.5 w-3.5" aria-hidden="true" />
        How this search ran
      </h4>
      {plan.algorithm && <PlanBody plan={plan} />}
      {targets.map(([name, target]) => (
        <div key={name} className="mt-2 border-t border-primary-200 pt-2">
          <div className="mb-1 text-xs font-semibold text-gray-900">{name}</div>
          <PlanBody plan={target} />
        </div>
      ))}
    </section>
  );
}
