import { isCollectionMoving, lsnProgress, moveTiming, phaseClass, targetGroups } from '../lib/placement-format';

// The tables of the placement view (GO-052): the groups of nodes, which group
// holds each collection (with its Move action), and the collection moves.

const th = 'py-2 px-3 text-xs font-semibold text-fg-faint uppercase';
const td = 'py-2.5 px-3';
const subTitle = 'text-xs font-semibold text-fg-faint uppercase tracking-wider mb-2';

/** The groups: name, meta badge, leader, collections and nodes. */
export function PlacementGroups({ groups, ownGroup }) {
  return (
    <div className="overflow-x-auto mb-6">
      <h4 className={subTitle}>Groups</h4>
      <table className="w-full text-sm">
        <caption className="sr-only">Placement groups</caption>
        <thead>
          <tr className="border-b border-line-soft">
            <th scope="col" className={`text-left ${th}`}>Group</th>
            <th scope="col" className={`text-left ${th}`}>Leader</th>
            <th scope="col" className={`text-right ${th}`}>Collections</th>
            <th scope="col" className={`text-left ${th}`}>Nodes</th>
          </tr>
        </thead>
        <tbody>
          {groups.map((g) => (
            <tr key={g.name} className="border-b border-line-soft">
              <td className={`${td} font-mono text-fg`}>
                {g.name}
                {g.meta && <span className="ml-2 inline-flex px-2 py-0.5 rounded-full text-xs font-sans font-medium bg-note-muted text-note-strong">meta</span>}
                {g.name === ownGroup && <span className="ml-2 text-xs font-sans text-fg-faint">(this node)</span>}
              </td>
              <td className={`${td} text-fg-subtle`}>
                <div className="font-mono text-fg">{g.leader || 'unknown'}</div>
                {g.leaderHttp && <div className="text-xs text-fg-faint">{g.leaderHttp}</div>}
              </td>
              <td className={`${td} text-right font-mono text-fg`}>{(g.collections || 0).toLocaleString()}</td>
              <td className={`${td} text-fg-subtle`}>
                {(g.nodes || []).map((n) => (
                  <div key={n.id}>
                    <span className="font-mono text-fg">{n.id}</span>
                    <span className="text-xs text-fg-faint"> {n.http || '-'}</span>
                  </div>
                ))}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** Which group holds each collection, and a Move button per row. */
export function PlacementCollections({ collections, groups, moves, onMove }) {
  return (
    <div className="overflow-x-auto mb-6">
      <h4 className={subTitle}>Collections</h4>
      {collections.length === 0 ? (
        <p className="text-sm text-fg-faint">No collections placed yet.</p>
      ) : (
        <table className="w-full text-sm">
          <caption className="sr-only">Collection placement</caption>
          <thead>
            <tr className="border-b border-line-soft">
              <th scope="col" className={`text-left ${th}`}>Collection</th>
              <th scope="col" className={`text-left ${th}`}>Group</th>
              <th scope="col" className={`text-right ${th}`}>Action</th>
            </tr>
          </thead>
          <tbody>
            {collections.map((c) => {
              const moving = isCollectionMoving(c, moves);
              const noTarget = targetGroups(groups, c.group).length === 0;
              return (
                <tr key={c.name} className="border-b border-line-soft">
                  <td className={`${td} font-mono text-fg`}>{c.name}</td>
                  <td className={`${td} font-mono text-fg`}>
                    {c.group}
                    {c.movingTo && <span className="ml-2 text-xs font-sans text-info-strong">moving to {c.movingTo}</span>}
                  </td>
                  <td className={`${td} text-right`}>
                    <button
                      type="button"
                      onClick={() => onMove(c)}
                      disabled={moving || noTarget}
                      aria-label={`Move ${c.name}`}
                      className="px-3 py-1 text-sm text-white bg-primary-700 rounded-lg hover:bg-primary-800 focus-visible:bg-primary-800 disabled:opacity-50"
                    >
                      Move
                    </button>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </div>
  );
}

/** The collection moves: phase, keys copied, LSN progress, timing and error. */
export function PlacementMoves({ moves }) {
  return (
    <div>
      <h4 className={subTitle}>Moves</h4>
      <ul className="space-y-2">
        {moves.map((m) => (
          <li key={`${m.collection}-${m.startedAt}`} className="rounded-lg border border-line-soft p-3 text-sm">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-mono text-fg">{m.collection}</span>
              <span className="text-fg-subtle">{m.from} → {m.to}</span>
              <span className={`inline-flex px-2 py-0.5 rounded-full text-xs font-medium ${phaseClass(m.phase)}`}>{m.phase || 'unknown'}</span>
            </div>
            <div className="mt-1 text-xs text-fg-faint">
              <span>Keys copied {(m.keysCopied || 0).toLocaleString()}</span>
              <span> · Applied / source LSN {lsnProgress(m)}</span>
              <span> · {moveTiming(m)}</span>
            </div>
            {m.error && <p className="mt-2 text-sm text-danger-strong bg-danger-soft border border-danger-line rounded p-2">{m.error}</p>}
          </li>
        ))}
      </ul>
    </div>
  );
}
