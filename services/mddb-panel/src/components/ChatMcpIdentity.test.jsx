// @vitest-environment jsdom
// #414: "Forward the user's identity" on an MCP server row, and the
// "signed-in users only" note on a saved scenario's server states.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, within } from '@testing-library/react';
import { useState } from 'react';

vi.mock('../lib/mddb-client', () => ({ default: { inspectChatMcpServer: vi.fn() } }));

const { default: ChatMcpServers } = await import('./ChatMcpServers');
const { default: ChatMcpStates } = await import('./ChatMcpStates');
const { toMcpRows, fromMcpRows } = await import('../lib/chat-mcp');

function Harness({ initial, secrets, spy }) {
  const [rows, setRows] = useState(toMcpRows(initial));
  spy?.(rows);
  return <ChatMcpServers rows={rows} onChange={setRows} secrets={secrets} scenarioEnabled />;
}

afterEach(cleanup);

describe('forward the user identity', () => {
  test('off by default; switched on, it takes a key, a header and a lifetime', () => {
    const spy = vi.fn();
    const secrets = [{ name: 'acct-service' }, { name: 'acct-identity' }];
    render(<Harness initial={[{ name: 'acct', url: 'https://a', enabled: true, secretRef: 'acct-service' }]} secrets={secrets} spy={spy} />);
    const toggle = screen.getByRole('switch', { name: "Forward the user's identity" });
    expect(toggle.checked).toBe(false);
    expect(toggle.getAttribute('aria-describedby')).toBe('chat-mcp-0-identity-help');
    expect(screen.getByText(/anonymous visitors are not offered/)).toBeTruthy();
    expect(screen.queryByLabelText('Identity key secret')).toBeNull();

    fireEvent.click(toggle);
    const key = screen.getByLabelText('Identity key secret');
    expect(within(key).getByRole('option', { name: 'acct-service' }).disabled).toBe(true);
    fireEvent.change(key, { target: { value: 'acct-identity' } });
    fireEvent.change(screen.getByLabelText('Identity header'), { target: { value: 'X-User' } });
    fireEvent.change(screen.getByLabelText('Token lifetime, s (10–300)'), { target: { value: '60' } });
    expect(screen.getByLabelText('Identity header').getAttribute('placeholder')).toBe('X-MDDB-Chat-Identity');
    expect(fromMcpRows(spy.mock.lastCall[0])[0].forwardIdentity).toEqual({ secretRef: 'acct-identity', header: 'X-User', ttlSeconds: 60 });
  });

  test('a stored key missing from the list stays selectable', () => {
    render(<Harness initial={[{ name: 'acct', url: 'https://a', forwardIdentity: { secretRef: 'gone' } }]} secrets={null} />);
    expect(screen.getByLabelText('Identity key secret').value).toBe('gone');
  });
});

describe('server states', () => {
  test('a server that forwards the identity says it serves signed-in users only', () => {
    render(<ChatMcpStates servers={[
      { name: 'acct', active: true, forwardIdentity: true, tools: [{ name: 'orders' }] },
      { name: 'wiki', active: true, tools: [] },
    ]} />);
    expect(screen.getByText('(signed-in users only)')).toBeTruthy();
    expect(screen.getAllByText(/signed-in users only/)).toHaveLength(1);
  });
});
