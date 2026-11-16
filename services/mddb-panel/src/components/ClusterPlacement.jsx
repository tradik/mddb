import { useCallback, useEffect, useId, useState } from 'react';
import { Boxes } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { anyMoveRunning, targetGroups } from '../lib/placement-format';
import ApiErrorNotice from './ApiErrorNotice';
import ClusterActionDialog from './ClusterActionDialog';
import { PlacementCollections, PlacementGroups, PlacementMoves } from './ClusterPlacementTables';

/** How often the view reloads while a move runs. */
export const MOVE_POLL_INTERVAL = 2000;

/** One labelled figure of the placement card. */
function Fact({ label, children }) {
  return (
    <div>
      <div className="text-xs text-fg-faint mb-1">{label}</div>
      <div className="text-sm font-mono text-fg">{children}</div>
    </div>
  );
}

/**
 * ClusterPlacement is the collection placement part of the Cluster tab
 * (GO-052). With MDDB_PLACEMENT_GROUPS each whole collection lives in one
 * group of nodes (each group a GO-051 cluster) and the meta group holds
 * users, keys, global settings and the placement map. It shows this node's
 * group, the groups, which group holds each collection and the moves, and an
 * admin can move a collection to another group. While a move runs it reloads
 * every two seconds.
 */
export default function ClusterPlacement() {
  const id = useId();
  const [placement, setPlacement] = useState(null);
  const [loadError, setLoadError] = useState(null);
  const [moving, setMoving] = useState(null); // { collection, from, to } while the dialog is open
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(null);
  const [started, setStarted] = useState(null);

  const load = useCallback(async () => {
    try {
      setPlacement(await mddbClient.getPlacement());
      setLoadError(null);
    } catch (err) {
      // A server without placement has no such route: that is "not configured".
      if (err.status === 404) setPlacement({ enabled: false });
      else setLoadError(err);
    }
  }, []);

  useEffect(() => {
    const first = setTimeout(() => void load(), 0);
    return () => clearTimeout(first);
  }, [load]);

  const running = anyMoveRunning(placement);
  useEffect(() => {
    if (!running) return undefined;
    const timer = setInterval(() => void load(), MOVE_POLL_INTERVAL);
    return () => clearInterval(timer);
  }, [running, load]);

  const close = useCallback(() => setMoving(null), []);

  if (!placement) return <ApiErrorNotice error={loadError} admin />;

  if (!placement.enabled) {
    return (
      <p className="mb-6 text-sm text-fg-faint">
        Collection placement is not configured. Set <code className="bg-surface-strong px-1 rounded">MDDB_PLACEMENT_GROUPS</code> to spread collections over groups of nodes.
      </p>
    );
  }

  const groups = placement.groups || [];
  const moves = placement.moves || [];

  const open = (collection) => {
    setMoving({ collection: collection.name, from: collection.group, to: targetGroups(groups, collection.group)[0] });
    setError(null);
    setStarted(null);
  };

  const confirm = async () => {
    setBusy(true);
    setError(null);
    try {
      const answer = await mddbClient.moveCollection({ collection: moving.collection, group: moving.to });
      setStarted(answer.move || { collection: moving.collection, to: moving.to });
      setMoving(null);
      void load();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <section aria-labelledby={`${id}-title`} className="bg-surface rounded-xl border border-line p-5 mb-6">
      <h3 id={`${id}-title`} className="flex items-center gap-2 mb-4 text-sm font-semibold text-fg-faint uppercase tracking-wider">
        <Boxes className="w-4 h-4 text-info" aria-hidden="true" />
        Collection Placement
      </h3>

      <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-4">
        <Fact label="This node">{placement.nodeId || '-'}</Fact>
        <Fact label="Its group">{placement.group || '-'}</Fact>
        <Fact label="Meta group">{placement.metaGroup || '-'}</Fact>
        <Fact label="Map term / route">{placement.term ?? '-'} / {placement.route || '-'}</Fact>
      </div>

      <ApiErrorNotice error={loadError} admin />
      {started && (
        <output className="block mb-4 text-sm text-success-strong bg-success-soft border border-success-line rounded p-2">
          Moving {started.collection} to {started.to}.
        </output>
      )}

      <PlacementGroups groups={groups} ownGroup={placement.group} />
      <PlacementCollections collections={placement.collections || []} groups={groups} moves={moves} onMove={open} />
      {moves.length > 0 && <PlacementMoves moves={moves} />}

      {moving && (
        <ClusterActionDialog title={`Move ${moving.collection}`} confirmLabel="Move" busy={busy} error={error} onConfirm={confirm} onCancel={close}>
          <p>
            The collection is copied to the target group while it keeps taking writes, then fenced briefly while the last writes catch up, and served from the new group.
          </p>
          <div className="flex items-center gap-2">
            <label htmlFor={`${id}-target`} className="font-medium text-fg">Target group</label>
            <select
              id={`${id}-target`}
              value={moving.to}
              onChange={(e) => setMoving({ ...moving, to: e.target.value })}
              className="px-2 py-1 border border-line rounded bg-surface text-fg"
            >
              {targetGroups(groups, moving.from).map((name) => <option key={name} value={name}>{name}</option>)}
            </select>
          </div>
        </ClusterActionDialog>
      )}
    </section>
  );
}
