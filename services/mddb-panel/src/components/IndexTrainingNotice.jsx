import { Hourglass } from 'lucide-react';

// GO-047: after a restart a collection answers flat, hnsw and bq searches as
// soon as its vectors are loaded; ivf, pq, opq, sq and sq4 train in the
// background, one collection at a time. `/v1/vector-stats` reports the queue
// as `indexTraining: { current, pending, indexes }`; until a collection is
// trained, a search naming one of those indexes gets 503.
export default function IndexTrainingNotice({ training }) {
  if (!training?.pending?.length) return null;
  const indexes = (training.indexes || []).join(', ') || 'trainable indexes';
  return (
    <div role="status" className="rounded-lg border border-amber-300 bg-amber-50 p-4">
      <div className="flex items-start gap-2">
        <Hourglass className="mt-0.5 h-4 w-4 shrink-0 text-amber-700" aria-hidden="true" />
        <div className="text-sm text-amber-900">
          <p className="font-medium">
            Training {indexes} for {training.pending.length} collection{training.pending.length === 1 ? '' : 's'}
            {training.current ? ` (now: ${training.current})` : ''}
          </p>
          <p className="mt-0.5">
            Flat, HNSW, BQ and Auto answer now. A search naming {indexes} on {training.pending.join(', ')} is
            refused with 503 until its training finishes.
          </p>
        </div>
      </div>
    </div>
  );
}
