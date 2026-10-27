export interface MddbCollection {
  name: string;
  documentCount: number;
  revisionCount?: number;
  metaIndexCount?: number;
}

export interface MddbStats {
  databasePath?: string;
  databaseSize?: number;
  mode?: string;
  uptime?: string;
  totalDocuments: number;
  totalRevisions: number;
  totalMetaIndices?: number;
  collections: MddbCollection[];
}

export interface MddbHealth {
  status: string;
  mode?: string;
}

export interface ClientOptions {
  baseUrl: string;
  apiKey?: string;
  timeoutMs?: number;
  fetchImpl?: typeof fetch;
}

export class MddbApiError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.name = 'MddbApiError';
    this.status = status;
  }
}

const DEFAULT_TIMEOUT_MS = 8000;

/** Strips trailing slashes without a regex (a `/\/+$/` backtracks). */
function trimTrailingSlashes(s: string): string {
  let end = s.length;
  while (end > 0 && s[end - 1] === '/') end--;
  return s.slice(0, end);
}

function joinUrl(base: string, path: string): string {
  const stripped = trimTrailingSlashes(base);
  const prefixed = path.startsWith('/') ? path : `/${path}`;
  return `${stripped}${prefixed}`;
}

async function request<T>(opts: ClientOptions, path: string): Promise<T> {
  const fetchImpl = opts.fetchImpl ?? fetch;
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), opts.timeoutMs ?? DEFAULT_TIMEOUT_MS);

  const headers: Record<string, string> = { Accept: 'application/json' };
  if (opts.apiKey) {
    headers['X-API-Key'] = opts.apiKey;
  }

  try {
    const res = await fetchImpl(joinUrl(opts.baseUrl, path), {
      method: 'GET',
      headers,
      signal: controller.signal,
      credentials: 'omit',
    });
    if (!res.ok) {
      throw new MddbApiError(`HTTP ${res.status} from ${path}`, res.status);
    }
    return (await res.json()) as T;
  } catch (err: unknown) {
    if (err instanceof MddbApiError) throw err;
    if (err instanceof Error && err.name === 'AbortError') {
      throw new MddbApiError(`Request to ${path} timed out`, 0);
    }
    const message = err instanceof Error ? err.message : String(err);
    throw new MddbApiError(`Network error: ${message}`, 0);
  } finally {
    clearTimeout(timer);
  }
}

export function getHealth(opts: ClientOptions): Promise<MddbHealth> {
  return request<MddbHealth>(opts, '/v1/health');
}

export function getStats(opts: ClientOptions): Promise<MddbStats> {
  return request<MddbStats>(opts, '/v1/stats');
}
