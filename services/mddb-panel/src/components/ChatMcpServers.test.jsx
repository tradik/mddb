// @vitest-environment jsdom
// ChatMcpServers (MCP-005): the "External knowledge (MCP)" section — add and
// remove a server, test the connection, allow read-only tools, and the
// reason each tool is inactive.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, within } from '@testing-library/react';
import { useState } from 'react';

vi.mock('../lib/mddb-client', () => ({ default: { inspectChatMcpServer: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { default: ChatMcpServers } = await import('./ChatMcpServers');
const { toMcpRows } = await import('../lib/chat-mcp');

const tools = [
  { name: 'search', description: 'Search the wiki', annotations: { readOnlyHint: true }, allowable: true },
  { name: 'wipe', annotations: { readOnlyHint: true, destructiveHint: true }, allowable: false, reason: 'it is marked destructiveHint: true' },
  { name: 'write_doc', annotations: {}, allowable: false, reason: 'it is not marked readOnlyHint: true' },
];

/** The section over `initial`, holding its rows the way the form does. */
function Harness({ initial, secrets = [{ name: 'wiki-token' }], scenarioEnabled = true, spy }) {
  const [rows, setRows] = useState(toMcpRows(initial));
  spy?.(rows);
  return <ChatMcpServers rows={rows} onChange={setRows} secrets={secrets} scenarioEnabled={scenarioEnabled} />;
}

afterEach(cleanup);

describe('servers', () => {
  test('adds a server, fills it in, and removes it', () => {
    const spy = vi.fn();
    render(<Harness initial={[]} spy={spy} />);
    fireEvent.click(screen.getByRole('button', { name: /Add an MCP server/ }));
    fireEvent.change(screen.getByLabelText('Server name'), { target: { value: 'wiki' } });
    fireEvent.change(screen.getByLabelText('URL (https, Streamable HTTP)'), { target: { value: 'https://w/mcp' } });
    fireEvent.change(screen.getByLabelText('Credential'), { target: { value: 'wiki-token' } });
    fireEvent.change(screen.getByLabelText('Timeout, ms'), { target: { value: '500' } });
    fireEvent.change(screen.getByLabelText('Result limit, chars'), { target: { value: '900' } });
    fireEvent.click(screen.getByRole('switch', { name: 'Server enabled' }));
    expect(spy.mock.lastCall[0][0]).toMatchObject({ name: 'wiki', url: 'https://w/mcp', secretRef: 'wiki-token', timeoutMs: '500',
      resultMaxChars: '900', enabled: false });
    fireEvent.click(screen.getByRole('button', { name: 'Remove server wiki' }));
    expect(spy.mock.lastCall[0]).toEqual([]);
  });

  test('with two servers, editing or removing one leaves the other', () => {
    const spy = vi.fn();
    render(<Harness initial={[{ name: 'a', url: 'https://a' }, { name: 'b', url: 'https://b' }]} spy={spy} />);
    fireEvent.change(screen.getAllByLabelText('Server name')[1], { target: { value: 'bb' } });
    expect(spy.mock.lastCall[0].map((r) => r.name)).toEqual(['a', 'bb']);
    fireEvent.click(screen.getByRole('button', { name: 'Remove server a' }));
    expect(spy.mock.lastCall[0].map((r) => r.name)).toEqual(['bb']);
  });

  test('a stored credential missing from the list stays selectable; without the list it says why', () => {
    render(<Harness initial={[{ name: 'w', url: 'https://w', secretRef: 'gone' }]} secrets={null} />);
    expect(screen.getByLabelText('Credential').value).toBe('gone');
    expect(screen.getByText(/The secret list needs admin/)).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Remove server w' })).toBeTruthy();
  });
});

describe('Test connection', () => {
  test('lists the tools with badges; only read-only ones can be allowed', async () => {
    vi.mocked(mddbClient.inspectChatMcpServer).mockResolvedValue({ serverName: 'wiki', serverVersion: '1.2', tools });
    const spy = vi.fn();
    render(<Harness initial={[{ name: 'w', url: ' https://w/mcp ', enabled: true, secretRef: 'wiki-token' }]} spy={spy} />);
    fireEvent.click(screen.getByRole('button', { name: 'Test connection' }));
    expect(await screen.findByText('Connected to wiki 1.2: 3 tools.')).toBeTruthy();
    expect(mddbClient.inspectChatMcpServer).toHaveBeenCalledWith({ url: 'https://w/mcp', secretRef: 'wiki-token' });
    const list = within(screen.getByRole('list', { name: 'Tools of this server' }));
    expect(list.getByLabelText('wipe').disabled).toBe(true);
    expect(list.getByText('destructive')).toBeTruthy();
    expect(list.getByText('may write')).toBeTruthy();
    expect(list.getByText('Cannot be allowed: it is not marked readOnlyHint: true')).toBeTruthy();
    expect(list.getByText('Search the wiki')).toBeTruthy();
    expect(list.getAllByText('Inactive: Not allowed')).toHaveLength(3);
    fireEvent.click(list.getByLabelText('search'));
    expect(spy.mock.lastCall[0][0].allowedTools).toEqual(['search']);
    expect(list.getByText('Active')).toBeTruthy();
    fireEvent.click(list.getByLabelText('search'));
    expect(spy.mock.lastCall[0][0].allowedTools).toEqual([]);
  });

  test('an allowed tool the server no longer offers can only be unchecked', async () => {
    vi.mocked(mddbClient.inspectChatMcpServer).mockResolvedValue({ tools: [] });
    render(<Harness initial={[{ name: 'w', url: 'https://w', enabled: false, allowedTools: ['retired'] }]} scenarioEnabled={false} />);
    expect(screen.getByText('Inactive: Scenario is disabled')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Test connection' }));
    expect(await screen.findByText('Connected to the server : 0 tools.')).toBeTruthy();
    expect(screen.getByText('Cannot be allowed: the server no longer offers it')).toBeTruthy();
    expect(screen.getByLabelText('retired').disabled).toBe(false);
  });

  test('the feature switched off, and any other failure', async () => {
    const off = Object.assign(new Error('API Error (404): feature disabled'), { status: 404, body: { feature: 'chatExternalMcp' } });
    vi.mocked(mddbClient.inspectChatMcpServer).mockRejectedValueOnce(off).mockRejectedValueOnce(new Error('API Error (502): refused'));
    render(<Harness initial={[{ name: 'w', url: 'https://w', enabled: false, allowedTools: ['search'] }]} />);
    expect(screen.getByText('Inactive: Server is disabled')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Test connection' }));
    expect((await screen.findByRole('alert')).textContent).toMatch(/turn on the chatExternalMcp feature/);
    expect(screen.getByText('Inactive: Feature chatExternalMcp is off')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Test connection' }));
    expect((await screen.findByText('API Error (502): refused')).getAttribute('role')).toBe('alert');
  });

  test('needs a URL', () => {
    render(<Harness initial={[{ name: 'w', url: ' ' }]} />);
    expect(screen.getByRole('button', { name: 'Test connection' }).disabled).toBe(true);
  });
});
