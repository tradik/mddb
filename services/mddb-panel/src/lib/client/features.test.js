// client/features (GO-049): the module switch endpoints, reached through the
// default client the way the panel calls them.
import { describe, test, expect, vi, beforeEach } from 'vitest';
import { stubFetch, jsonResponse, expectEndpoint } from '../../../test/fetch-mock.js';

vi.mock('../auth', () => ({ authManager: { getToken: vi.fn() } }));

const { authManager } = await import('../auth');
const { default: client } = await import('../mddb-client');

beforeEach(() => {
  vi.mocked(authManager.getToken).mockReturnValue(null);
  vi.spyOn(console, 'error').mockImplementation(() => {});
});

describe('endpoints', () => {
  test.each([
    ['listFeatures', (c) => c.listFeatures(), '/v1/features', 'GET'],
    ['setFeature on', (c) => c.setFeature('geo', true), '/v1/features/geo', 'PUT', { enabled: true }],
    ['setFeature off, name escaped', (c) => c.setFeature('a/b', 0), '/v1/features/a%2Fb', 'PUT', { enabled: false }],
  ])('%s', (_label, ...row) => expectEndpoint(client, ...row));
});

test('a 409 carries the lock in the error body', async () => {
  stubFetch(jsonResponse({ error: 'feature locked', feature: 'geo', lockedBy: 'MDDB_FEATURES_FORCE_OFF' }, { status: 409 }));
  const err = await client.setFeature('geo', true).catch((e) => e);
  expect(err.status).toBe(409);
  expect(err.body.lockedBy).toBe('MDDB_FEATURES_FORCE_OFF');
  expect(err.message).toBe('API Error (409): feature locked');
});
