// @vitest-environment jsdom
// MCPConfigPanel (LLM Connections): the Server Domain every example uses —
// including the server's own MCP config, whose addresses read
// "localhost127.0.0.1:18924" before 2.19.6 — each tab's example, the
// alternative config, Copy and Download.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { getMCPConfigText: vi.fn() } }));
vi.mock('./MCPAPIKeysPanel', () => ({
  default: ({ onKeyCreated }) => (
    <div>
      api keys panel
      <button type="button" onClick={() => onKeyCreated('mcp_created_now')}>create test key</button>
    </div>
  ),
}));
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
  test('#441: opens on the tab a link asked for, then forgets it; an unknown tab is ignored', async () => {
    useStore.setState({ viewTarget: 'claude' });
    await renderWith({ protocols: { mcp: { domain: 'db.example.com', addr: ':9000' } } });
    expect(code()).toContain('claude mcp add');
    expect(useStore.getState().viewTarget).toBeNull();
    cleanup();
    useStore.setState({ viewTarget: 'nope' });
    await renderWith({});
    expect(screen.getByText('mcp_config.yaml')).toBeTruthy();
    expect(useStore.getState().viewTarget).toBeNull();
  });

  test('every tab has an example; the MCP tab falls back to the generated one', async () => {
    vi.mocked(mddbClient.getMCPConfigText).mockRejectedValue(new Error('down'));
    await renderWith({ protocols: { mcp: { domain: 'db.example.com', addr: ':9000' } } });
    expect(screen.getByText('mcp_config.yaml')).toBeTruthy();
    expect(code()).toContain('custom_tools:');

    const tabs = {
      Claude: 'claude mcp add --transport http mddb http://db.example.com:9000/mcp',
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

  test('the DeepSeek stdio config says it starts a separate local database', async () => {
    await renderWith(null);
    fireEvent.click(screen.getByRole('button', { name: 'DeepSeek' }));
    expect(screen.getByText(/separate local database/)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'ChatGPT' }));
    expect(screen.queryByText(/separate local database/)).toBeNull();
  });

  test('shows a spinner while the server config loads', () => {
    vi.mocked(mddbClient.getMCPConfigText).mockReturnValue(new Promise(() => {}));
    useStore.setState({ config: null });
    const { container } = render(<MCPConfigPanel />);
    expect(container.querySelector('.animate-spin')).not.toBeNull();
  });
});

// The examples of every tab except Claude, recorded on 2.19.8 before the
// templates moved out of the component (#440): the split must not change a
// byte of them.
describe('MCPConfigPanel non-Claude examples', () => {
  const otherTabs = ['MCP Config', 'ChatGPT', 'Ollama (Python)', 'DeepSeek', 'Manus', 'Bielik.ai', 'Open WebUI'];
  const shown = () => ({
    filename: document.querySelector('.bg-gray-900 .font-mono').textContent,
    content: code(),
    instructions: [...document.querySelectorAll('ol li')].map((li) => li.textContent),
  });

  test.each([
    ['generated', () => vi.mocked(mddbClient.getMCPConfigText).mockRejectedValue(new Error('down'))],
    ['from the server', () => {}],
  ])('are unchanged since 2.19.8 (MCP config %s)', async (_name, arrange) => {
    arrange();
    await renderWith({ protocols: { mcp: { domain: 'db.example.com', addr: ':9000' }, http: { addr: ':11023' } } });
    const examples = {};
    for (const tab of otherTabs) {
      fireEvent.click(screen.getByRole('button', { name: tab }));
      examples[tab] = shown();
    }
    expect(examples).toMatchSnapshot();
  });
});

// #440: the Claude tab connects to the server the panel shows.
describe('MCPConfigPanel Claude tab', () => {
  const reach = (mcp) => ({ protocols: { mcp: { domain: 'myserver.com', addr: ':9000', ...mcp } } });
  const openClaude = async (config) => {
    await renderWith(config);
    fireEvent.click(screen.getByRole('button', { name: 'Claude' }));
  };
  const pick = (label) => fireEvent.click(screen.getByRole('button', { name: label }));
  const keyField = () => screen.queryByLabelText(/^API key/);
  const mcpUrl = () => screen.getByTestId('mcp-url').textContent;

  test('with a public URL and keys on it opens on the command; a typed key reaches every variant and Copy', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    await openClaude(reach({ publicUrl: 'https://mcp.example.com', apiKeyEnabled: true, oauthEnabled: true }));
    expect(mcpUrl()).toBe('https://mcp.example.com/mcp');
    expect(screen.getByText(/from MDDB_MCP_PUBLIC_URL/)).toBeTruthy();
    expect(code()).toBe('claude mcp add --transport http mddb https://mcp.example.com/mcp --header "X-API-Key: mcp_…"');
    expect(screen.getByRole('button', { name: 'Claude Code (command)' }).getAttribute('aria-pressed')).toBe('true');
    expect(screen.queryByRole('button', { name: 'Download' })).toBeNull();

    fireEvent.change(keyField(), { target: { value: 'mcp_typed' } });
    expect(code()).toBe('claude mcp add --transport http mddb https://mcp.example.com/mcp --header "X-API-Key: mcp_typed"');
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Copy' })));
    expect(writeText).toHaveBeenLastCalledWith('claude mcp add --transport http mddb https://mcp.example.com/mcp --header "X-API-Key: mcp_typed"');

    pick('Project file');
    expect(JSON.parse(code()).mcpServers.mddb).toEqual({ type: 'http', url: 'https://mcp.example.com/mcp', headers: { 'X-API-Key': 'mcp_typed' } });
    expect(screen.getByRole('button', { name: 'Download' })).toBeTruthy();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Copy' })));
    expect(writeText).toHaveBeenLastCalledWith(code());

    pick('Custom connector');
    expect(code()).toBe('https://mcp.example.com/mcp');
    expect(screen.queryByRole('button', { name: 'Download' })).toBeNull();
    expect(screen.getByText(/Customize → Connectors/)).toBeTruthy();
    delete navigator.clipboard;
  });

  test('without a public URL the MCP URL follows the Server Domain, a host name or a URL', async () => {
    await openClaude(reach({ apiKeyEnabled: false }));
    expect(mcpUrl()).toBe('http://myserver.com:9000/mcp');
    expect(code()).toBe('claude mcp add --transport http mddb http://myserver.com:9000/mcp');
    fireEvent.change(domainField(), { target: { value: 'https://myserver.com' } });
    expect(mcpUrl()).toBe('https://myserver.com/mcp');
    expect(code()).toBe('claude mcp add --transport http mddb https://myserver.com/mcp');
  });

  test('keys off: no header, no headers, no key input', async () => {
    await openClaude(reach({ apiKeyEnabled: false, oauthEnabled: false }));
    expect(keyField()).toBeNull();
    expect(code()).not.toContain('--header');
    pick('Project file');
    expect(code()).not.toContain('headers');
  });

  test('OAuth off: the connector lists the missing settings instead of steps', async () => {
    await openClaude(reach({ apiKeyEnabled: false, oauthEnabled: false }));
    pick('Custom connector');
    expect(screen.getByText('Custom connectors need OAuth, which is off on this server')).toBeTruthy();
    expect(screen.getByText('MDDB_MCP_API_KEY_ENABLED=true')).toBeTruthy();
    expect(screen.getByText('MDDB_MCP_OAUTH_ENABLED=true')).toBeTruthy();
    expect(screen.getByRole('link', { name: /OAuth 2.1 for claude.ai connectors/ }).getAttribute('href')).toContain('docs/MCP.md');
    expect(screen.queryByText('Setup Instructions:')).toBeNull();
  });

  test('both stdio variants carry the separate local database note and download their file', async () => {
    const createObjectURL = vi.fn(() => 'blob:x');
    vi.stubGlobal('URL', { ...URL, createObjectURL, revokeObjectURL: vi.fn() });
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
    await openClaude(reach({ apiKeyEnabled: false }));
    pick('Local stdio (Docker)');
    expect(code()).toContain('tradik/mddb:latest');
    expect(screen.getByText(/separate local database/)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Download' }));
    expect(click.mock.contexts[0].download).toBe('claude_desktop_config.json');
    pick('Local stdio (binary)');
    expect(code()).toContain('/path/to/mddbd');
    expect(screen.getByText(/separate local database/)).toBeTruthy();
  });

  test('a key created in API Keys fills the field; it is gone after leaving the page and never stored', async () => {
    const setItem = vi.spyOn(Storage.prototype, 'setItem');
    await openClaude(reach({ apiKeyEnabled: true }));
    expect(keyField().value).toBe('');
    expect(keyField().getAttribute('type')).toBe('password');
    expect(keyField().getAttribute('autocomplete')).toBe('off');
    fireEvent.click(screen.getByRole('button', { name: 'Create a key' }));
    expect(screen.getByText('api keys panel')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'create test key' }));
    pick('Claude');
    expect(keyField().value).toBe('mcp_created_now');
    expect(code()).toContain('X-API-Key: mcp_created_now');
    expect(screen.queryByRole('button', { name: 'Create a key' })).toBeNull();
    const show = screen.getByRole('button', { name: 'Show key' });
    fireEvent.click(show);
    expect(keyField().getAttribute('type')).toBe('text');
    expect(show.getAttribute('aria-pressed')).toBe('true');
    fireEvent.click(screen.getByRole('button', { name: 'Hide key' }));
    expect(keyField().getAttribute('type')).toBe('password');

    cleanup();
    await openClaude(reach({ apiKeyEnabled: true }));
    expect(keyField().value).toBe('');
    expect(setItem.mock.calls.filter(([, value]) => String(value).includes('mcp_created_now'))).toEqual([]);
  });

  test('against a server before 2.19.9 the key is optional and nothing breaks', async () => {
    await openClaude({ protocols: { mcp: { domain: 'myserver.com', addr: ':9000' } } });
    expect(screen.getByLabelText('API key (optional)')).toBeTruthy();
    expect(code()).toBe('claude mcp add --transport http mddb http://myserver.com:9000/mcp');
    fireEvent.change(keyField(), { target: { value: 'mcp_k' } });
    expect(code()).toContain('--header "X-API-Key: mcp_k"');
    pick('Custom connector');
    expect(screen.getByText('MDDB_MCP_API_KEY_ENABLED=true')).toBeTruthy();
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
