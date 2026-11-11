/**
 * Formatting of the Unix timestamps (seconds) the server returns on
 * sessions, jobs, keys and events, in the viewer's locale.
 */

/** A local date and time, or "—" for 0/absent. */
export function formatUnix(seconds) {
  if (!seconds) return '—';
  return new Date(seconds * 1000).toLocaleString();
}

/** Unix seconds for a `<input type="date">` value (local midnight), or 0 for empty. */
export function dateInputToUnix(value) {
  if (!value) return 0;
  return Math.floor(new Date(`${value}T00:00:00`).getTime() / 1000);
}
