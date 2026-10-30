// @vitest-environment jsdom
// ServerEdgeCard: the ES subset, result cap, OpenTelemetry and proxy settings
// of GET /v1/config — given by the Config page or loaded by the card itself,
// an older server that reports none of them, and a failed load.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { getConfig: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { default: ServerEdgeCard } = await import('./ServerEdgeCard');
const { edgeRows } = await import('../lib/server-edge');

const full = {
  esCompat: { enabled: true, prefix: '/es' },
  searchMaxLimit: 1000,
  telemetry: { enabled: true, endpoint: 'https://collector.internal:4318' },
  clientAddressing: { trustedProxies: 2, header: 'Cf-Connecting-Ip', proxyProtocol: true },
};

/** The value shown next to a label. */
const valueOf = (label) => screen.getByText(label).nextElementSibling.textContent;

beforeEach(() => {
  vi.mocked(mddbClient.getConfig).mockReset();
});
afterEach(cleanup);

describe('ServerEdgeCard', () => {
  test('shows every setting of a given config without loading it again', () => {
    render(<ServerEdgeCard config={full} />);
    expect(mddbClient.getConfig).not.toHaveBeenCalled();
    expect(screen.getByRole('heading', { name: 'Search surface, tracing & proxies' })).toBeTruthy();
    expect(valueOf('Elasticsearch subset')).toBe('On, under /es');
    expect(valueOf('Search result cap')).toBe('1,000 results per search');
    expect(valueOf('OpenTelemetry traces')).toBe('On, to https://collector.internal:4318');
    expect(valueOf('Trusted proxies')).toBe('2 configured');
    expect(valueOf('Client IP header')).toBe('Cf-Connecting-Ip');
    expect(valueOf('PROXY protocol')).toBe('On');
  });

  test('loads the config itself, and shows the switches off', async () => {
    vi.mocked(mddbClient.getConfig).mockResolvedValue({
      esCompat: { enabled: false, prefix: '/es' },
      searchMaxLimit: 50,
      telemetry: { enabled: false },
      clientAddressing: { trustedProxies: 0, header: 'X-Forwarded-For', proxyProtocol: false },
    });
    render(<ServerEdgeCard />);
    expect(screen.getByText('Loading…')).toBeTruthy();
    expect(await screen.findByText('50 results per search')).toBeTruthy();
    expect(valueOf('Elasticsearch subset')).toBe('Off');
    expect(valueOf('OpenTelemetry traces')).toBe('Off');
    expect(valueOf('Trusted proxies')).toBe('None: forwarding headers are ignored');
    expect(valueOf('PROXY protocol')).toBe('Off');
  });

  test('says so when the load fails', async () => {
    vi.mocked(mddbClient.getConfig).mockRejectedValue(new Error('API Error (403): forbidden'));
    render(<ServerEdgeCard />);
    expect(await screen.findByText('Could not load the server configuration: API Error (403): forbidden')).toBeTruthy();
    expect(screen.queryByText('Loading…')).toBeNull();
  });

  test('an older server reports none of the settings', () => {
    expect(edgeRows({}).map(([, value]) => value)).toEqual(Array(6).fill('Not reported by this server'));
  });
});
