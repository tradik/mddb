// @vitest-environment jsdom
// TenantsPanel, TenantsTable, TenantDetail, TenantForm (GO-054): the list a
// global admin sees, a tenant admin's own report, the refusals, and setting
// and removing a tenant's quota and limits.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor, within } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: { listTenants: vi.fn(), getTenantUsage: vi.fn(), setTenant: vi.fn(), deleteTenant: vi.fn(), getMe: vi.fn() },
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { default: TenantsPanel } = await import('./TenantsPanel');

const acme = {
  tenant: 'acme',
  quota: { maxDocuments: 10, maxBytes: 2048, maxCollections: 4 },
  limits: { readPerSecond: 5, maxConcurrentWrites: -1 },
  effectiveLimits: { readPerSecond: 5, writePerSecond: 0 },
  vectorRamLimitBytes: 0,
  usage: { documents: 9, bytes: 1024, collections: 2, vectors: 3 },
  collectionNames: ['acme/a', 'acme/b'],
  queues: {
    index: { depth: 2, taken: { jobs: 4, waitSecondsTotal: 2, maxWaitSeconds: 1 } },
    embedding: { depth: 1, taken: { jobs: 0 } },
    embeddingParked: { depth: 3 },
  },
  inFlight: { searches: 1, writes: 0, reindexes: 0 },
  refusals: { 'tenant.readRate': 2, 'quota.documents': 1 },
};
const bare = {
  tenant: 'bare', effectiveLimits: {}, vectorRamLimitBytes: 0, collectionNames: [], refusals: {},
  usage: { documents: 0, bytes: 0, vectors: 0, collections: 0, vectorRamBytes: 0, embeddingCalls: 0, embeddingTokens: 0 },
  queues: { index: { depth: 0, taken: { jobs: 0 } }, embedding: { depth: 0, taken: { jobs: 0 } }, embeddingParked: { depth: 0 } },
  inFlight: { searches: 0, writes: 0, reindexes: 0 },
};

function apiError(status, message = `API Error (${status})`) {
  return Object.assign(new Error(message), { status });
}

beforeEach(() => {
  vi.mocked(mddbClient.getMe).mockResolvedValue({ username: 'root', admin: true });
  vi.spyOn(console, 'error').mockImplementation(() => {});
});
afterEach(cleanup);

async function openAcme() {
  vi.mocked(mddbClient.listTenants).mockResolvedValue({ tenants: [acme, bare], total: 2 });
  render(<TenantsPanel />);
  const table = await screen.findByRole('table');
  fireEvent.click(within(table).getByRole('button', { name: 'Details of acme' }));
  return screen.findByRole('heading', { name: 'Tenant acme' });
}

describe('the list a global admin sees', () => {
  test('rows show usage against the quota, queues, running work and refusals', async () => {
    vi.mocked(mddbClient.listTenants).mockResolvedValue({ tenants: [acme, bare], total: 2 });
    render(<TenantsPanel />);
    const row = (await screen.findByRole('rowheader', { name: 'acme' })).closest('tr');
    const cells = within(row).getAllByRole('cell').map((c) => c.textContent);
    expect(cells.slice(0, 6)).toEqual(['9 of 10 (90%)', '1.0 KB of 2.0 KB (50%)', '2 of 4 (50%)', '6', '1', '3']);
    const bareCells = within(screen.getByRole('rowheader', { name: 'bare' }).closest('tr')).getAllByRole('cell');
    expect(bareCells[0].textContent).toBe('0');
  });

  test('details: bars, limits in force, queues, refusals and collections', async () => {
    await openAcme();
    expect(screen.getByRole('progressbar', { name: 'Documents' })).toHaveProperty('value', 90);
    expect(screen.getByText('Usage against the tenant quota')).toBeTruthy();
    expect(screen.getByText('Reads and searches per second (tenant)', { selector: 'dt' }).nextSibling.textContent).toBe('5');
    expect(screen.getByText('Writes per second (tenant)', { selector: 'dt' }).nextSibling.textContent).toBe('none');
    expect(screen.getByText(/4 jobs taken, average wait 0.5 s/)).toBeTruthy();
    expect(screen.getByText(/Running now:/).parentElement.textContent).toMatch(/1 searches, 0 writes, 0 reindexes/);
    expect(screen.getByText('tenant.readRate').parentElement.textContent).toBe('tenant.readRate: 2');
    expect(screen.getByText('acme/a, acme/b')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Details of acme' }).getAttribute('aria-pressed')).toBe('true');
  });

  test('a tenant without a quota, refusals or collections says so', async () => {
    vi.mocked(mddbClient.listTenants).mockResolvedValue({ tenants: [bare] });
    render(<TenantsPanel />);
    fireEvent.click(await screen.findByRole('button', { name: 'Details of bare' }));
    expect(await screen.findByText(/No tenant quota: its collections hold 0 documents in 0 collections/)).toBeTruthy();
    expect(screen.getByText('Nothing refused.')).toBeTruthy();
    expect(screen.getByText('The tenant has no collections yet.')).toBeTruthy();
    expect(screen.getAllByText(/0 waiting/, { selector: 'li' })).toHaveLength(3);
  });

  test('no tenants at all', async () => {
    vi.mocked(mddbClient.listTenants).mockResolvedValue({});
    render(<TenantsPanel />);
    expect(await screen.findByText(/No tenants yet/)).toBeTruthy();
  });

  test('reload reads the list again', async () => {
    vi.mocked(mddbClient.listTenants).mockResolvedValue({ tenants: [acme] });
    render(<TenantsPanel />);
    await screen.findByRole('table');
    fireEvent.click(screen.getByRole('button', { name: /Reload/ }));
    await waitFor(() => expect(mddbClient.listTenants).toHaveBeenCalledTimes(2));
  });

  test('a failed list is shown', async () => {
    vi.mocked(mddbClient.listTenants).mockRejectedValue(apiError(500, 'boom'));
    render(<TenantsPanel />);
    expect((await screen.findByRole('alert')).textContent).toBe('Could not load the tenants: boom');
  });
});

describe('the form', () => {
  test('saves both parts; empty fields are removed parts', async () => {
    await openAcme();
    vi.mocked(mddbClient.setTenant).mockResolvedValue({});
    fireEvent.change(screen.getByRole('spinbutton', { name: 'Documents' }), { target: { value: '20' } });
    fireEvent.change(screen.getByRole('spinbutton', { name: 'Stored bytes' }), { target: { value: '' } });
    fireEvent.change(screen.getByRole('spinbutton', { name: 'Collections holding documents' }), { target: { value: '' } });
    for (const label of ['Reads and searches per second (tenant)', 'Writes at once']) {
      fireEvent.change(screen.getByRole('spinbutton', { name: label }), { target: { value: '' } });
    }
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await screen.findByText('Saved the quota and limits of acme.');
    expect(mddbClient.setTenant).toHaveBeenCalledWith('acme', { quota: { maxDocuments: 20 }, limits: null });
    expect(mddbClient.listTenants).toHaveBeenCalledTimes(2);
  });

  test('a bad value is refused before anything is sent', async () => {
    await openAcme();
    fireEvent.change(screen.getByRole('spinbutton', { name: 'Share of the vector RAM limit (0–1)' }), { target: { value: '3' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect((await screen.findAllByRole('alert')).at(-1).textContent).toMatch(/a number from 0 to 1/);
    expect(mddbClient.setTenant).not.toHaveBeenCalled();
    fireEvent.change(screen.getByRole('spinbutton', { name: 'Share of the vector RAM limit (0–1)' }), { target: { value: '' } });
    fireEvent.change(screen.getByRole('spinbutton', { name: 'Searches at once' }), { target: { value: '-5' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect((await screen.findAllByRole('alert')).at(-1).textContent).toMatch(/^Searches at once/);
  });

  test('removing the quota and limits, and a failure', async () => {
    await openAcme();
    vi.mocked(mddbClient.deleteTenant).mockRejectedValueOnce(apiError(403, 'forbidden')).mockResolvedValueOnce({});
    fireEvent.click(screen.getByRole('button', { name: 'Remove its quota and limits' }));
    expect((await screen.findByRole('alert')).textContent).toBe('forbidden');
    fireEvent.click(screen.getByRole('button', { name: 'Remove its quota and limits' }));
    await screen.findByText('acme has no quota or limits of its own any more.');
    expect(mddbClient.deleteTenant).toHaveBeenCalledWith('acme');
  });
});

describe('anyone but a global admin', () => {
  test('a tenant admin sees its own tenant, read-only', async () => {
    vi.mocked(mddbClient.getMe).mockResolvedValue({ username: 'tina', tenant: 'acme' });
    vi.mocked(mddbClient.listTenants).mockRejectedValue(apiError(403));
    vi.mocked(mddbClient.getTenantUsage).mockResolvedValue(acme);
    render(<TenantsPanel />);
    await screen.findByRole('heading', { name: 'Tenant acme' });
    expect(mddbClient.getTenantUsage).toHaveBeenCalledWith('acme');
    expect(screen.getByText(/Only a global admin can change/)).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Save' })).toBeNull();
  });

  test('a tenant user without read on "*" is told why', async () => {
    vi.mocked(mddbClient.getMe).mockResolvedValue({ username: 'tom', tenant: 'acme' });
    vi.mocked(mddbClient.listTenants).mockRejectedValue(apiError(403));
    vi.mocked(mddbClient.getTenantUsage).mockRejectedValue(apiError(403));
    render(<TenantsPanel />);
    expect((await screen.findByRole('alert')).textContent).toMatch(/report of your tenant "acme" was refused too/);
  });

  test('a user without a tenant is told there is nothing to show', async () => {
    vi.mocked(mddbClient.getMe).mockRejectedValue(apiError(401));
    vi.mocked(mddbClient.listTenants).mockRejectedValue(apiError(403));
    render(<TenantsPanel />);
    expect((await screen.findByRole('alert')).textContent).toMatch(/your account belongs to no tenant/);
  });
});
