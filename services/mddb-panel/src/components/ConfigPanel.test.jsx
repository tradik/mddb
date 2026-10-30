// @vitest-environment jsdom
// ConfigPanel: the Config page — loading, error with retry, database mode,
// the protocol addresses GET /v1/config reports, feature switches, the search
// surface / tracing / proxy card, and the vector configuration.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { getConfig: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: ConfigPanel } = await import('./ConfigPanel');

const initialStore = useStore.getState();

const config = {
  databasePath: '/data/mddb.db',
  mode: 'wr',
  protocols: {
    http: { enabled: true, addr: ':11023' },
    grpc: { enabled: true, addr: ':11024' },
    http3: { enabled: true, addr: ':11443' },
  },
  authEnabled: true,
  metricsEnabled: false,
  searchStatsEnabled: true,
  vectorConfig: { enabled: true, provider: 'openai', model: 'text-embedding-3-small', dimensions: 1536, apiUrl: 'https://api.openai.com/v1' },
  esCompat: { enabled: true, prefix: '/es' },
  searchMaxLimit: 1000,
  telemetry: { enabled: false },
  clientAddressing: { trustedProxies: 1, header: 'X-Forwarded-For', proxyProtocol: false },
};

/** The value printed next to a label such as "Path". */
const valueOf = (label) => screen.getByText(label).nextElementSibling.textContent;

/** Renders the page with getConfig answering `data`, and waits for it. */
async function renderWith(data) {
  vi.mocked(mddbClient.getConfig).mockResolvedValue(data);
  render(<ConfigPanel />);
  await screen.findByText('Server Configuration');
}

beforeEach(() => {
  useStore.setState(initialStore, true);
  vi.mocked(mddbClient.getConfig).mockReset();
  vi.spyOn(console, 'error').mockImplementation(() => {});
});
afterEach(cleanup);

describe('ConfigPanel', () => {
  test('shows a spinner while the request is in flight', () => {
    vi.mocked(mddbClient.getConfig).mockReturnValue(new Promise(() => {}));
    const { container } = render(<ConfigPanel />);
    expect(container.querySelector('.animate-spin')).not.toBeNull();
  });

  test('reports a failure and loads again on Retry', async () => {
    vi.mocked(mddbClient.getConfig)
      .mockRejectedValueOnce(new Error('API Error (500): down'))
      .mockResolvedValueOnce(config);
    render(<ConfigPanel />);
    expect(await screen.findByText('Failed to load configuration')).toBeTruthy();
    expect(screen.getByText('API Error (500): down')).toBeTruthy();
    expect(console.error).toHaveBeenCalledWith('Failed to load config:', expect.any(Error));
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('Server Configuration')).toBeTruthy();
  });

  test('shows the database, protocol addresses, features, the edge card and vectors', async () => {
    await renderWith(config);
    expect(valueOf('Path')).toBe('/data/mddb.db');
    expect(valueOf('Mode')).toBe('Read/Write');
    expect(valueOf('HTTP Address')).toBe(':11023');
    expect(valueOf('gRPC Address')).toBe(':11024');
    expect(valueOf('HTTP/3 Address')).toBe(':11443');
    expect(valueOf('Authentication & RBAC')).toBe('Enabled');
    expect(valueOf('Prometheus Metrics')).toBe('Disabled');
    expect(valueOf('Search Statistics')).toBe('Enabled');
    // The card shows the page's config; it does not load it again.
    expect(valueOf('Elasticsearch subset')).toBe('On, under /es');
    expect(valueOf('Trusted proxies')).toBe('1 configured');
    expect(mddbClient.getConfig).toHaveBeenCalledTimes(1);
    expect(valueOf('Provider')).toBe('openai');
    expect(valueOf('Dimensions')).toBe('1536');
    expect(valueOf('API URL')).toBe('https://api.openai.com/v1');
    expect(screen.queryByText('Matches by meaning')).toBeNull();
  });

  test.each([
    ['read', true, 'Read Only', 'Yes'],
    ['write', false, 'Write Only', 'No — shared spelling only (semantic: false)'],
  ])('mode %s and a vector model with semantic=%s', async (mode, semantic, modeText, semanticText) => {
    await renderWith({ ...config, mode, vectorConfig: { ...config.vectorConfig, semantic } });
    expect(valueOf('Mode')).toBe(modeText);
    expect(valueOf('Matches by meaning')).toBe(semanticText);
  });

  test('without HTTP/3 or vectors those rows are left out', async () => {
    await renderWith({ ...config, protocols: { http: { addr: ':1' }, grpc: { addr: ':2' }, http3: { enabled: false } }, vectorConfig: undefined });
    expect(screen.queryByText('HTTP/3 Address')).toBeNull();
    expect(screen.queryByText('Vector Search Configuration')).toBeNull();
    expect(valueOf('Vector Search')).toBe('Disabled');
  });
});
