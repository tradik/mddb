// What the placement view of the Cluster tab shows, from GET /v1/placement
// (GO-052): the phase of a collection move, how far it got, and which
// collections are mid-move and so cannot be moved again.

/** Phases after which a move does no more work. */
const FINAL_PHASES = new Set(['done', 'failed']);

/** True while a move still works: copying, catching-up or fenced. */
export function isMoveRunning(move) {
  return Boolean(move) && !FINAL_PHASES.has(move.phase);
}

/** True when any of the moves still works, so the view keeps polling. */
export function anyMoveRunning(placement) {
  return (placement?.moves || []).some(isMoveRunning);
}

/** True while `collection` is being moved: the map says so, or a move of it still runs. */
export function isCollectionMoving(collection, moves = []) {
  if (collection.movingTo) return true;
  return moves.some((m) => m.collection === collection.name && isMoveRunning(m));
}

/** The groups a collection can move to: every group but the one holding it. */
export function targetGroups(groups, current) {
  return (groups || []).map((g) => g.name).filter((name) => name !== current);
}

const phaseBadges = {
  copying: 'bg-info-muted text-info-strong',
  'catching-up': 'bg-info-muted text-info-strong',
  fenced: 'bg-warning-muted text-warning-strong',
  done: 'bg-success-muted text-success-strong',
  failed: 'bg-danger-muted text-danger-strong',
};

/** Badge colours for a move phase, in the theme's tokens. */
export function phaseClass(phase) {
  return phaseBadges[phase] || 'bg-surface-subtle text-fg-soft';
}

/** The applied LSN against the source's as "950 / 999 (95%)", "-" before the source LSN is known. */
export function lsnProgress(move) {
  const source = move.sourceLsn || 0;
  if (source <= 0) return '-';
  const applied = move.appliedLsn || 0;
  const share = Math.min(100, Math.floor((applied / source) * 100));
  return `${applied.toLocaleString()} / ${source.toLocaleString()} (${share}%)`;
}

/** A move's start and end, or how long it took, as short local text. */
export function moveTiming(move) {
  if (move.durationMs > 0) return `took ${(move.durationMs / 1000).toFixed(1)}s`;
  const at = new Date(move.startedAt || '');
  return Number.isNaN(at.getTime()) ? '-' : `started ${at.toLocaleTimeString()}`;
}
