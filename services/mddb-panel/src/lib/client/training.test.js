// client/training: the model training endpoints, reached through the default
// client the way the panel calls them.
import { describe, test, expect, vi, beforeEach } from 'vitest';
import { expectEndpoint, stubFetch, lastRequest, textResponse } from '../../../test/fetch-mock.js';

vi.mock('../auth', () => ({ authManager: { getToken: vi.fn() } }));

const { authManager } = await import('../auth');
const { default: client } = await import('../mddb-client');

beforeEach(() => {
  vi.mocked(authManager.getToken).mockReturnValue(null);
  vi.spyOn(console, 'error').mockImplementation(() => {});
});

describe('endpoints', () => {
  test.each([
    ['getTrainingProfiles', (c) => c.getTrainingProfiles(), '/v1/training/profiles', 'GET'],
    ['listTrainingDatasets', (c) => c.listTrainingDatasets(), '/v1/training/datasets', 'GET'],
    ['buildTrainingDataset', (c) => c.buildTrainingDataset({ collections: ['a'] }), '/v1/training/datasets', 'POST', { collections: ['a'] }],
    ['getTrainingDataset', (c) => c.getTrainingDataset('ds_1'), '/v1/training/datasets/ds_1', 'GET'],
    ['deleteTrainingDataset', (c) => c.deleteTrainingDataset('ds 1'), '/v1/training/datasets/ds%201', 'DELETE'],
    ['listTrainingRuns', (c) => c.listTrainingRuns(), '/v1/training/jobs', 'GET'],
    ['startTrainingRun', (c) => c.startTrainingRun({ profile: 'p', trial: true }), '/v1/training/jobs', 'POST', { profile: 'p', trial: true }],
    ['getTrainingRun', (c) => c.getTrainingRun('run_1'), '/v1/training/jobs/run_1', 'GET'],
    ['cancelTrainingRun', (c) => c.cancelTrainingRun('run_1'), '/v1/training/jobs/run_1/cancel', 'POST'],
    ['evaluateTrainingRun', (c) => c.evaluateTrainingRun('run_1'), '/v1/training/jobs/run_1/evaluate', 'POST'],
    ['deleteTrainingRun', (c) => c.deleteTrainingRun('run_1'), '/v1/training/jobs/run_1', 'DELETE'],
  ])('%s', (_label, ...row) => expectEndpoint(client, ...row));

  test.each([
    [(c) => c.downloadTrainingDataset('ds_1', 'validation'), '/v1/training/datasets/ds_1/download?part=validation'],
    [(c) => c.downloadTrainingResult('run_1'), '/v1/training/jobs/run_1/download'],
  ])('downloads %#', async (call, url) => {
    const fetchMock = stubFetch(textResponse('data', { headers: { 'Content-Disposition': 'attachment; filename="model.gguf"' } }));
    const res = await call(client);
    expect(res.filename).toBe('model.gguf');
    expect(lastRequest(fetchMock).url).toBe(url);
  });
});
