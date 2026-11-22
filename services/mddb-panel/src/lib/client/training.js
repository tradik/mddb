/**
 * Model training: datasets built from collections, and runs of the trainer
 * commands the server's operator configured. Mixed into the MDDBClient
 * prototype through client/index.js, so `this` is the client and paths are
 * relative to /v1. Every endpoint is admin only.
 */
const dataset = (id) => `/training/datasets/${encodeURIComponent(id)}`;
const run = (id) => `/training/jobs/${encodeURIComponent(id)}`;

export const trainingMethods = {
  /** `{ configured, profiles, errors }`: the profiles, never their command lines. */
  async getTrainingProfiles() {
    return this.request('/training/profiles', { method: 'GET' });
  },

  /** Every dataset, newest first: `{ jobs, total }`. */
  async listTrainingDatasets() {
    return this.request('/training/datasets', { method: 'GET' });
  },

  /** Starts building a dataset: `{ jobId, job }`. */
  async buildTrainingDataset(request) {
    return this.request('/training/datasets', { method: 'POST', body: JSON.stringify(request) });
  },

  /** One dataset with its summary and preview: `{ job, log }`. */
  async getTrainingDataset(id) {
    return this.request(dataset(id), { method: 'GET' });
  },

  async deleteTrainingDataset(id) {
    return this.request(dataset(id), { method: 'DELETE' });
  },

  /** A dataset part as a file: `part` is "train" or "validation". */
  async downloadTrainingDataset(id, part) {
    return this.requestBlob(`${dataset(id)}/download?part=${encodeURIComponent(part)}`);
  },

  /** Every run, newest first: `{ jobs, total }`. */
  async listTrainingRuns() {
    return this.request('/training/jobs', { method: 'GET' });
  },

  /** Starts a run (or a trial run): `{ jobId, job }`. */
  async startTrainingRun(request) {
    return this.request('/training/jobs', { method: 'POST', body: JSON.stringify(request) });
  },

  /** One run with the end of its log: `{ job, log }`. */
  async getTrainingRun(id) {
    return this.request(run(id), { method: 'GET' });
  },

  async cancelTrainingRun(id) {
    return this.request(`${run(id)}/cancel`, { method: 'POST' });
  },

  /** Runs the evaluation of a finished run again. */
  async evaluateTrainingRun(id) {
    return this.request(`${run(id)}/evaluate`, { method: 'POST' });
  },

  async deleteTrainingRun(id) {
    return this.request(run(id), { method: 'DELETE' });
  },

  /** The run's result file. */
  async downloadTrainingResult(id) {
    return this.requestBlob(`${run(id)}/download`);
  },
};
