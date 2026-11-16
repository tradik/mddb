/**
 * Collection placement (GO-052): with MDDB_PLACEMENT_GROUPS each whole
 * collection lives in one group of nodes, and one group (the meta group)
 * holds users, keys, global settings and the placement map. Both calls need
 * admin; a refused move is a 400 or 409 whose `error` the client's error keeps.
 */
import { postJson } from './http';

export const placementMethods = {
  /** The groups, which group holds each collection, and the moves; `{ enabled: false }` when not configured. */
  getPlacement() {
    return this.request('/placement', { method: 'GET' });
  },

  /** Starts moving `collection` to `group`; answers `{ move }` (202). */
  moveCollection({ collection, group }) {
    return postJson(this, '/placement/move', { collection, group });
  },
};
