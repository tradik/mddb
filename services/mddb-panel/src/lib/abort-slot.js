/**
 * The abort slot of a search panel: a ref holding the AbortController of the
 * search in flight, so Cancel can stop it.
 *
 * A search that starts while another is in flight aborts that one and takes
 * the slot. The aborted search settles afterwards; if it emptied the slot
 * then, Cancel would find nothing to abort and the newer search could no
 * longer be stopped. A search therefore releases the slot only while it still
 * holds it.
 */

/**
 * Empties `ref` when it still holds `controller`.
 *
 * @param {{ current: AbortController | null }} ref the panel's abort slot
 * @param {AbortController} controller the settling search's controller
 * @returns {boolean} true when a newer search holds the slot: the settling
 *   search must then leave the shared loading state alone.
 */
export function releaseAbort(ref, controller) {
  if (ref.current === controller) {
    ref.current = null;
    return false;
  }
  return ref.current !== null;
}
