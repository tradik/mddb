// client/tenants (GO-054): the tenant endpoints, reached through the default
// client the way the panel calls them.
import { describe, test, vi, beforeEach } from 'vitest';
import { expectEndpoint } from '../../../test/fetch-mock.js';

vi.mock('../auth', () => ({ authManager: { getToken: vi.fn() } }));

const { authManager } = await import('../auth');
const { default: client } = await import('../mddb-client');

beforeEach(() => {
  vi.mocked(authManager.getToken).mockReturnValue(null);
  vi.spyOn(console, 'error').mockImplementation(() => {});
});

describe('endpoints', () => {
  test.each([
    ['listTenants', (c) => c.listTenants(), '/v1/tenants', 'GET'],
    ['getTenantUsage', (c) => c.getTenantUsage('acme'), '/v1/tenants/acme/usage', 'GET'],
    ['getTenantUsage escapes the name', (c) => c.getTenantUsage('a b'), '/v1/tenants/a%20b/usage', 'GET'],
    ['setTenant', (c) => c.setTenant('acme', { quota: { maxDocuments: 5 }, limits: null }), '/v1/tenants/acme', 'PUT',
      { quota: { maxDocuments: 5 }, limits: null }],
    ['deleteTenant', (c) => c.deleteTenant('acme'), '/v1/tenants/acme', 'DELETE'],
  ])('%s', (_label, ...row) => expectEndpoint(client, ...row));
});
