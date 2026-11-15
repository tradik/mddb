// withServerDomain (2.19.6): the server's MCP config shown with the Server
// Domain of LLM Connections — a domain, no domain, and the malformed address
// servers before 2.19.6 generated for a bound listen address.
import { describe, test, expect } from 'vitest';
import { withServerDomain, urlOnHost } from './server-address.js';

const yaml = (grpc, rest) => `mcp:
  # listenAddress: "0.0.0.0:9000"

mddb:
  grpcAddress: "${grpc}"
  restBaseUrl: "${rest}"
  transportMode: "grpc_with_rest_fallback"
`;

describe('withServerDomain', () => {
  test('puts the domain in front of each port', () => {
    const out = withServerDomain(yaml('localhost:11024', 'http://localhost:11023'), 'db.example.com');
    expect(out).toContain('grpcAddress: "db.example.com:11024"');
    expect(out).toContain('restBaseUrl: "http://db.example.com:11023"');
    expect(out).toContain('# listenAddress: "0.0.0.0:9000"');
  });

  test('repairs the address older servers concatenated onto localhost', () => {
    const out = withServerDomain(yaml('localhost127.0.0.1:18924', 'http://localhost127.0.0.1:18923'), 'localhost');
    expect(out).toContain('grpcAddress: "localhost:18924"');
    expect(out).toContain('restBaseUrl: "http://localhost:18923"');
    expect(out).not.toContain('localhost127');
  });

  test('a host with replacement characters is inserted literally', () => {
    expect(withServerDomain(yaml('h:1', 'https://h:2'), 'a$1b')).toContain('restBaseUrl: "https://a$1b:2"');
  });

  test('without a domain, or without the keys, the text is unchanged', () => {
    const text = yaml('localhost:11024', 'http://localhost:11023');
    expect(withServerDomain(text, '')).toBe(text);
    expect(withServerDomain('custom_tools: []\n', 'db.example.com')).toBe('custom_tools: []\n');
  });

  test('an address it cannot read is left as it is', () => {
    const odd = 'grpcAddress: bare:1\nrestBaseUrl: "half\ngrpcAddress: "unix-socket"\n';
    expect(withServerDomain(odd, 'db.example.com')).toBe(odd);
  });
});

describe('urlOnHost', () => {
  test.each([
    [':11023', 'http://db.example.com:11023'],
    ['0.0.0.0:9000', 'http://db.example.com:9000'],
    ['127.0.0.1:18923', 'http://db.example.com:18923'],
  ])('%s on the domain is %s', (addr, url) => {
    expect(urlOnHost('db.example.com', addr)).toBe(url);
  });
});
