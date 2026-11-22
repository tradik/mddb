/**
 * The Model training page's logic, kept out of its components: the dataset
 * form and the request it becomes, a profile's parameters as form values,
 * the words for a job's state, and the before/after of an evaluation.
 */

/** The step-by-step guide the page links to. */
export const TRAINING_GUIDE_URL = 'https://github.com/tradik/mddb/blob/main/docs/MODEL-TRAINING-GUIDE.md';

/** How often the page reads the lists again while a job runs. */
export const TRAINING_POLL_MS = 2000;

/** The validation share a pairs dataset starts with. */
export const DEFAULT_VALIDATION_RATIO = '0.1';

/** A fresh dataset form. Pairs by default, so evaluation has something to score. */
export function emptyDatasetForm() {
  return {
    name: '',
    collections: [],
    format: 'pairs',
    input: '',
    output: '{content}',
    system: '',
    filterKey: '',
    filterValue: '',
    lang: '',
    maxRecords: '',
    validationRatio: DEFAULT_VALIDATION_RATIO,
    shuffle: true,
    seed: '1',
    dedupe: true,
    acknowledgeUnlicensed: false,
  };
}

/** The form after a format change: pairs gets a validation share when it had none. */
export function withFormat(form, format) {
  const noShare = form.validationRatio === '' || Number(form.validationRatio) === 0;
  return { ...form, format, validationRatio: format === 'pairs' && noShare ? DEFAULT_VALIDATION_RATIO : form.validationRatio };
}

/** The request POST /v1/training/datasets takes for a form. */
export function datasetRequest(form) {
  const req = {
    collections: form.collections,
    format: form.format,
    shuffle: form.shuffle,
    seed: Number(form.seed) || 0,
    validationRatio: Number(form.validationRatio) || 0,
    dedupe: form.dedupe,
    acknowledgeUnlicensed: form.acknowledgeUnlicensed,
  };
  if (form.name.trim()) req.name = form.name.trim();
  if (form.lang.trim()) req.lang = form.lang.trim();
  if (Number(form.maxRecords) > 0) req.maxRecords = Number(form.maxRecords);
  if (form.filterKey.trim() && form.filterValue.trim()) {
    req.filterMeta = { [form.filterKey.trim()]: [form.filterValue.trim()] };
  }
  if (form.format === 'pairs') {
    req.pairs = { input: form.input, output: form.output };
    if (form.system.trim()) req.pairs.system = form.system;
  }
  return req;
}

/** A profile's parameters at their defaults, as form values. */
export function parameterDefaults(profile) {
  const values = {};
  for (const p of profile?.parameters || []) {
    if (p.default !== undefined) values[p.name] = p.default;
    else if (p.type === 'bool') values[p.name] = false;
  }
  return values;
}

/** A field's text as the value its type takes; '' leaves the parameter out. */
export function parameterValue(param, text) {
  if (param.type === 'bool') return Boolean(text);
  if (text === '' || text === undefined) return undefined;
  if (param.type === 'int' || param.type === 'float') return Number(text);
  return text;
}

/** The parameters to send: the values given, without empty ones. */
export function parameterRequest(values) {
  return Object.fromEntries(Object.entries(values).filter(([, v]) => v !== undefined && v !== ''));
}

/** Plain words for a job's status. */
export const STATUS_LABELS = {
  pending: 'Waiting',
  processing: 'Running',
  completed: 'Done',
  completedWithWarnings: 'Done with warnings',
  failed: 'Failed',
  cancelled: 'Cancelled',
  interrupted: 'Interrupted by a server restart',
};

export function statusLabel(status) {
  return STATUS_LABELS[status] || status;
}

/** Whether a job is still going. */
export function isActive(job) {
  return job?.status === 'pending' || job?.status === 'processing';
}

/** A size in bytes for people. */
export function formatBytes(n) {
  if (!n) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.min(units.length - 1, Math.floor(Math.log(n) / Math.log(1024)));
  return `${(n / 1024 ** i).toFixed(i ? 1 : 0)} ${units[i]}`;
}

/** A score as a percentage. */
export function percent(score) {
  return `${(score * 100).toFixed(1)}%`;
}

/**
 * The before/after of an evaluation for display: the two scores, their
 * difference, the metrics side by side and the notes. null when nothing ran.
 */
export function evaluationView(evaluation) {
  if (!evaluation) return null;
  const before = evaluation.base?.report || null;
  const after = evaluation.result?.report || null;
  const names = [...new Set([...Object.keys(before?.metrics || {}), ...Object.keys(after?.metrics || {})])].sort();
  return {
    skipped: evaluation.skipped || '',
    before,
    after,
    delta: typeof evaluation.delta === 'number' ? evaluation.delta : null,
    metrics: names.map((name) => ({ name, before: before?.metrics?.[name], after: after?.metrics?.[name] })),
    errors: [evaluation.base?.error, evaluation.result?.error].filter(Boolean),
    notes: evaluation.notes || [],
    samples: (after?.samples || []).slice(0, 5),
  };
}
