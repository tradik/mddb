/**
 * A scenario's repetition-loop guard (#413): llm.loopGuard as the editor's
 * flat form fields, and back.
 *
 * mddb-chat stops an answer once most of what the model writes repeats what
 * it already wrote. Its own [llm.loop_guard] holds the defaults; a scenario
 * overrides them field by field. Every field here is "not set" when empty,
 * and the mode "default" sends no override at all.
 */

export const loopGuardModes = [
  { value: 'default', label: "The chat service's setting" },
  { value: 'on', label: 'On' },
  { value: 'off', label: 'Off' },
];

export const loopGuardUnits = [
  { value: '', label: "The chat service's unit" },
  { value: 'chars', label: 'Characters' },
  { value: 'words', label: 'Words' },
];

/** A count as the string an input shows; 0 and missing are empty. */
function countText(value) {
  return value ? String(value) : '';
}

/** The form fields of a scenario's llm.loopGuard (none: all unset). */
export function toLoopGuardForm(guard = {}) {
  let mode = 'default';
  if (guard.enabled === true) mode = 'on';
  if (guard.enabled === false) mode = 'off';
  return {
    loopGuardMode: mode,
    loopGuardNgram: countText(guard.ngram),
    loopGuardWindow: countText(guard.window),
    loopGuardRatio: countText(guard.ratio),
    loopGuardMinOutput: countText(guard.minOutput),
    loopGuardUnit: guard.unit || '',
  };
}

/** llm.loopGuard for the form, or undefined when it overrides nothing. */
export function fromLoopGuardForm(form) {
  const guard = {};
  if (form.loopGuardMode !== 'default') guard.enabled = form.loopGuardMode === 'on';
  const counts = { ngram: form.loopGuardNgram, window: form.loopGuardWindow, minOutput: form.loopGuardMinOutput };
  Object.entries(counts).forEach(([field, text]) => {
    const n = Number.parseInt(text, 10);
    if (n > 0) guard[field] = n;
  });
  const ratio = Number(form.loopGuardRatio);
  if (form.loopGuardRatio !== '' && ratio > 0) guard.ratio = ratio;
  if (form.loopGuardUnit) guard.unit = form.loopGuardUnit;
  return Object.keys(guard).length > 0 ? guard : undefined;
}

/** Why the guard's fields cannot be saved, or null when they can. */
export function loopGuardProblem(form) {
  const ratio = Number(form.loopGuardRatio);
  if (form.loopGuardRatio !== '' && !(ratio > 0 && ratio <= 1)) return 'The loop guard ratio must be above 0 and at most 1.';
  const ngram = Number(form.loopGuardNgram);
  const window = Number(form.loopGuardWindow);
  if (form.loopGuardNgram !== '' && form.loopGuardWindow !== '' && window < ngram) {
    return 'The loop guard window may not be shorter than its n-gram.';
  }
  return null;
}
