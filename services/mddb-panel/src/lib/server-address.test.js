// withServerDomain (2.19.6): the server's MCP config shown with the Server
// Domain of LLM Connections — a domain, no domain, and the malformed address
// servers before 2.19.6 generated for a bound listen address.
import { describe, test, expect } from 'vitest';
import { withServerDomain, urlOnHost, mcpBaseUrl, mcpEndpoint } from './server-address.js';

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

  // #440: an instance behind a TLS proxy or tunnel has no port of its own.
  test.each([
    ['https://myserver.com', 'https://myserver.com'],
    ['https://myserver.com/', 'https://myserver.com'],
    ['HTTPS://myserver.com:8443/path', 'https://myserver.com:8443'],
    ['http://10.0.0.5:8080', 'http://10.0.0.5:8080'],
  ])('a Server Domain typed as the URL %s is used as %s', (domain, url) => {
    expect(urlOnHost(domain, ':9000')).toBe(url);
  });

  test('a URL that does not parse is taken as a host name, as before', () => {
    expect(urlOnHost('http://', ':9000')).toBe('http://http://:9000');
  });
});

describe('withServerDomain with a URL', () => {
  test('the host name of the URL goes in front of each port', () => {
    const out = withServerDomain(yaml('localhost:11024', 'http://localhost:11023'), 'https://db.example.com:8443');
    expect(out).toContain('grpcAddress: "db.example.com:11024"');
    expect(out).toContain('restBaseUrl: "http://db.example.com:11023"');
  });
});

describe('mcpEndpoint', () => {
  test('the server public URL wins', () => {
    const address = { publicUrl: 'https://mcp.example.com', domain: 'myserver.com', mcpAddr: ':9000' };
    expect(mcpEndpoint(address)).toBe('https://mcp.example.com/mcp');
    expect(mcpBaseUrl(address)).toBe('https://mcp.example.com');
  });

  test.each([
    ['myserver.com', 'http://myserver.com:9000/mcp'],
    ['https://myserver.com', 'https://myserver.com/mcp'],
    ['https://myserver.com:8443', 'https://myserver.com:8443/mcp'],
  ])('without one, the Server Domain %s gives %s', (domain, url) => {
    expect(mcpEndpoint({ publicUrl: '', domain, mcpAddr: '0.0.0.0:9000' })).toBe(url);
  });
});
