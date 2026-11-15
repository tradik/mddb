// @vitest-environment jsdom
// MCPConfigPanel (LLM Connections): the Server Domain every example uses —
// including the server's own MCP config, whose addresses read
// "localhost127.0.0.1:18924" before 2.19.6 — each tab's example, the
// alternative config, Copy and Download.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { getMCPConfigText: vi.fn() } }));
vi.mock('./MCPAPIKeysPanel', () => ({ default: () => <div>api keys panel</div> }));
vi.mock('@scripts/mddb_model.py?raw', () => ({ default: 'MDDB_URL = "http://mddb:9000"' }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: MCPConfigPanel } = await import('./MCPConfigPanel');

const initialStore = useStore.getState();
const serverYaml = `mddb:
  grpcAddress: "localhost127.0.0.1:18924"
  restBaseUrl: "http://localhost127.0.0.1:18923"
`;

/** Renders the page with `config` in the store and waits for the YAML load. */
async function renderWith(config) {
  useStore.setState({ config });
  render(<MCPConfigPanel />);
  await act(async () => {});
}

const code = () => document.querySelector('pre code').textContent;
const domainField = () => screen.getByLabelText('Server Domain');

beforeEach(() => {
  useStore.setState(initialStore, true);
  vi.mocked(mddbClient.getMCPConfigText).mockReset().mockResolvedValue(serverYaml);
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe('MCPConfigPanel Server Domain', () => {
  test('the server config shows its addresses on the configured domain', async () => {
    await renderWith({ protocols: { mcp: { domain: 'db.example.com', addr: ':9000' }, http: { addr: '127.0.0.1:18923' } } });
    expect(domainField().value).toBe('db.example.com');
    expect(code()).toContain('grpcAddress: "db.example.com:18924"');
    expect(code()).toContain('restBaseUrl: "http://db.example.com:18923"');
    expect(code()).not.toContain('localhost127');

    fireEvent.change(domainField(), { target: { value: 'mddb.internal' } });
    expect(code()).toContain('grpcAddress: "mddb.internal:18924"');
  });

  test('without a domain the page host is used, and every example follows the field', async () => {
    await renderWith({ httpAddr: '0.0.0.0:11023' });
    expect(domainField().value).toBe(window.location.hostname);
    expect(code()).toContain(`grpcAddress: "${window.location.hostname}:18924"`);

    fireEvent.click(screen.getByRole('button', { name: 'Ollama (Python)' }));
    expect(code()).toContain(`MDDB_URL = "http://${window.location.hostname}:11023"`);
    fireEvent.change(domainField(), { target: { value: '' } });
    expect(code()).toContain(`MDDB_URL = "http://${window.location.hostname}:11023"`);
  });

  test('a page without a host name falls back to localhost', async () => {
    const url = window.location.href;
    // eslint-disable-next-line no-undef -- the jsdom environment exposes its instance
    jsdom.reconfigure({ url: 'file:///panel/index.html' });
    try {
      await renderWith(null);
      expect(domainField().value).toBe('localhost');
      fireEvent.change(domainField(), { target: { value: '' } });
      expect(code()).toContain('grpcAddress: "localhost:18924"');
    } finally {
      // eslint-disable-next-line no-undef -- see above
      jsdom.reconfigure({ url });
    }
  });
});

describe('MCPConfigPanel tabs', () => {
  test('every tab has an example; the MCP tab falls back to the generated one', async () => {
    vi.mocked(mddbClient.getMCPConfigText).mockRejectedValue(new Error('down'));
    await renderWith({ protocols: { mcp: { domain: 'db.example.com', addr: ':9000' } } });
    expect(screen.getByText('mcp_config.yaml')).toBeTruthy();
    expect(code()).toContain('custom_tools:');

    const tabs = {
      'Claude Desktop': 'claude_desktop_config.json',
      ChatGPT: 'openai_actions.json',
      'Ollama (Python)': 'http://db.example.com:11023',
      DeepSeek: 'deepseek_mcp_config.json',
      Manus: 'http://db.example.com:11023',
      'Bielik.ai': 'http://db.example.com:11023',
      'Open WebUI': 'http://db.example.com:9000',
    };
    for (const [tab, expected] of Object.entries(tabs)) {
      fireEvent.click(screen.getByRole('button', { name: tab }));
      expect(document.querySelector('pre').closest('.bg-gray-900').textContent).toContain(expected);
      expect(screen.getByText('Setup Instructions:')).toBeTruthy();
    }

    fireEvent.click(screen.getByRole('button', { name: 'API Keys' }));
    expect(screen.getByText('api keys panel')).toBeTruthy();
    expect(screen.queryByText('Setup Instructions:')).toBeNull();
  });

  test('the alternative config toggles and resets on a tab change', async () => {
    await renderWith(null);
    fireEvent.click(screen.getByRole('button', { name: 'Claude Desktop' }));
    expect(code()).toContain('tradik/mddb:latest');
    fireEvent.click(screen.getByRole('button', { name: 'Local Binary (no Docker)' }));
    expect(code()).toContain('/path/to/mddbd');
    expect(screen.getByText(/\(Local Binary \(no Docker\)\)/)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Docker' }));
    expect(code()).toContain('tradik/mddb:latest');
    fireEvent.click(screen.getByRole('button', { name: 'Local Binary (no Docker)' }));
    fireEvent.click(screen.getByRole('button', { name: 'ChatGPT' }));
    expect(screen.queryByRole('button', { name: 'Docker' })).toBeNull();
  });

  test('shows a spinner while the server config loads', () => {
    vi.mocked(mddbClient.getMCPConfigText).mockReturnValue(new Promise(() => {}));
    useStore.setState({ config: null });
    const { container } = render(<MCPConfigPanel />);
    expect(container.querySelector('.animate-spin')).not.toBeNull();
  });
});

describe('MCPConfigPanel Copy and Download', () => {
  test('Copy writes the shown config and says Copied! for two seconds; a failure is logged', async () => {
    vi.useFakeTimers();
    const writeText = vi.fn().mockResolvedValueOnce(undefined).mockRejectedValueOnce(new Error('denied'));
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    const logged = vi.spyOn(console, 'error').mockImplementation(() => {});
    await renderWith({ protocols: { mcp: { domain: 'db.example.com' } } });
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Copy' })));
    expect(writeText).toHaveBeenCalledWith(code());
    expect(screen.getByText('Copied!')).toBeTruthy();
    act(() => vi.advanceTimersByTime(2000));
    expect(screen.queryByText('Copied!')).toBeNull();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Copy' })));
    expect(logged).toHaveBeenCalledWith('Failed to copy:', expect.any(Error));
    delete navigator.clipboard;
  });

  test('Download saves the shown config under the tab file name', async () => {
    const createObjectURL = vi.fn(() => 'blob:x');
    const revokeObjectURL = vi.fn();
    vi.stubGlobal('URL', { ...URL, createObjectURL, revokeObjectURL });
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
    await renderWith(null);
    fireEvent.click(screen.getByRole('button', { name: 'Download' }));
    expect(createObjectURL).toHaveBeenCalledWith(expect.any(Blob));
    expect(click.mock.contexts[0].download).toBe('mcp_config.yaml');
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:x');
  });
});
