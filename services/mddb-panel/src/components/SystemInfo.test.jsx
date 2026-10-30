// @vitest-environment jsdom
// SystemInfo: the System page — loading, error with retry, the hardware /
// memory / network / software cards, the gain card and Refresh.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { getSystemInfo: vi.fn(), getGain: vi.fn() } }));
// The corpus-pack, diagnose and server-edge cards have their own suites.
vi.mock('./CorpusPacksCard', () => ({ default: () => <div>corpus packs card</div> }));
vi.mock('./DiagnoseCard', () => ({ default: () => <div>diagnose card</div> }));
vi.mock('./ServerEdgeCard', () => ({ default: () => <div>server edge card</div> }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: SystemInfo } = await import('./SystemInfo');

const info = {
  hostname: 'db-1',
  uptimeSeconds: 2 * 86400 + 3 * 3600 + 4 * 60,
  numCPU: 8,
  cpuUsagePercent: 91.6,
  os: 'linux',
  arch: 'amd64',
  numGoroutines: 12345,
  memoryUsed: 512 * 1024 * 1024,
  memorySystem: 2 * 1024 * 1024 * 1024,
  memoryTotal: 1536,
  memoryHeap: 0,
  network: [
    { name: 'eth0', flags: 'up|broadcast', addresses: ['10.0.0.2/24', 'fe80::1/64'] },
    { name: 'lo', flags: 'up|loopback', addresses: ['127.0.0.1/8'] },
  ],
  goVersion: 'go1.26.0',
  version: '2.18.8',
};

const initialStore = useStore.getState();

/** The value printed under a card label such as "Hostname". */
function valueOf(label) {
  return screen.getByText(label).nextElementSibling.textContent;
}

/** The CPU bar's fill element. */
function cpuBar(container) {
  return container.querySelector('.h-2.rounded-full.transition-all');
}

beforeEach(() => {
  useStore.setState(initialStore, true);
  vi.mocked(mddbClient.getSystemInfo).mockReset();
  vi.mocked(mddbClient.getGain).mockReset().mockResolvedValue({ windowDays: 30, totals: {} });
  vi.spyOn(console, 'error').mockImplementation(() => {});
});
afterEach(cleanup);

describe('SystemInfo', () => {
  test('shows a spinner while the request is in flight', () => {
    vi.mocked(mddbClient.getSystemInfo).mockReturnValue(new Promise(() => {}));
    const { container } = render(<SystemInfo />);
    expect(container.querySelector('.animate-spin')).not.toBeNull();
    expect(screen.queryByText('System Information')).toBeNull();
  });

  test('reports a failure and loads again on Retry', async () => {
    vi.mocked(mddbClient.getSystemInfo)
      .mockRejectedValueOnce(new Error('API Error (500): down'))
      .mockResolvedValueOnce(info);
    render(<SystemInfo />);
    expect(await screen.findByText('Failed to load system info')).toBeTruthy();
    expect(screen.getByText('API Error (500): down')).toBeTruthy();
    expect(console.error).toHaveBeenCalledWith('Failed to load system info:', expect.any(Error));

    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('System Information')).toBeTruthy();
    expect(screen.queryByText('Failed to load system info')).toBeNull();
    expect(mddbClient.getSystemInfo).toHaveBeenCalledTimes(2);
  });

  test('renders server, hardware, memory, network and software details', async () => {
    vi.mocked(mddbClient.getSystemInfo).mockResolvedValue(info);
    const { container } = render(<SystemInfo />);
    await screen.findByText('System Information');

    expect(valueOf('Hostname')).toBe('db-1');
    expect(valueOf('Uptime')).toBe('2d 3h 4m');
    expect(screen.getByText('CPU Usage (8 cores)').nextElementSibling.textContent).toBe('92%');
    expect(cpuBar(container).className).toContain('bg-red-500');
    expect(cpuBar(container).style.width).toBe('92%');
    expect(valueOf('Operating System')).toBe('linux (amd64)');
    expect(valueOf('CPU Cores')).toBe('8');
    expect(valueOf('Goroutines')).toBe((12345).toLocaleString());

    expect(screen.getByText('Heap Memory Usage').nextElementSibling.textContent).toBe('25%');
    expect(screen.getByText('512 MB used')).toBeTruthy();
    expect(screen.getByText('2 GB system')).toBeTruthy();
    expect(valueOf('Total Allocated')).toBe('1.5 KB');
    expect(valueOf('In Use')).toBe('512 MB');
    expect(valueOf('Heap In Use')).toBe('0 B');

    expect(screen.getByText('eth0')).toBeTruthy();
    expect(screen.getByText('fe80::1/64')).toBeTruthy();
    expect(screen.getByText('127.0.0.1/8')).toBeTruthy();

    expect(valueOf('Go Version')).toBe('go1.26.0');
    expect(valueOf('MDDB Version')).toBe('2.18.8');
    expect(await screen.findByText('Retrieval gain, last 30 days')).toBeTruthy();
    expect(screen.getByText('corpus packs card')).toBeTruthy();
    expect(screen.getByText('diagnose card')).toBeTruthy();
    expect(screen.getByText('server edge card')).toBeTruthy();
  });

  test.each([
    [{ uptimeSeconds: 3 * 3600 + 120, cpuUsagePercent: 60 }, '3h 2m', 'bg-yellow-500', '60%'],
    [{ uptimeSeconds: 59 * 60, cpuUsagePercent: 10 }, '59m', 'bg-green-500', '10%'],
    [{ uptimeSeconds: 0, cpuUsagePercent: undefined }, '0m', 'bg-green-500', '0%'],
    [{ cpuUsagePercent: 250 }, '2d 3h 4m', 'bg-red-500', '100%'],
  ])('uptime and CPU bar for %o', async (overrides, uptime, colour, width) => {
    vi.mocked(mddbClient.getSystemInfo).mockResolvedValue({ ...info, ...overrides });
    const { container } = render(<SystemInfo />);
    await screen.findByText('System Information');
    expect(valueOf('Uptime')).toBe(uptime);
    expect(cpuBar(container).className).toContain(colour);
    expect(cpuBar(container).style.width).toBe(width);
  });

  test.each([[undefined], [[]]])('hides the network card when interfaces are %o', async (network) => {
    vi.mocked(mddbClient.getSystemInfo).mockResolvedValue({ ...info, network });
    render(<SystemInfo />);
    await screen.findByText('System Information');
    expect(screen.queryByText('Network')).toBeNull();
  });

  test('Refresh fetches again and shows the new values', async () => {
    vi.mocked(mddbClient.getSystemInfo)
      .mockResolvedValueOnce(info)
      .mockResolvedValueOnce({ ...info, hostname: 'db-2' });
    render(<SystemInfo />);
    await screen.findByText('db-1');

    fireEvent.click(screen.getByRole('button', { name: /Refresh/ }));
    expect(await screen.findByText('db-2')).toBeTruthy();
    expect(mddbClient.getSystemInfo).toHaveBeenCalledTimes(2);
  });
});
