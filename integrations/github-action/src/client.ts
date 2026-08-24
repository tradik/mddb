import { Agent, type Dispatcher } from 'undici';
import type { MddbDocument } from './document.js';

export interface MddbClientOptions {
  baseUrl: string;
  apiKey: string;
  timeoutSeconds: number;
  verifySsl: boolean;
  /** Override the fetch implementation — used by tests. */
  fetchImpl?: typeof fetch;
  /** Max attempts per request (including the first). */
  maxAttempts?: number;
  /** Initial backoff in ms; doubles per retry. */
  backoffMs?: number;
  /** Sleep implementation — used by tests. */
  sleepImpl?: (ms: number) => Promise<void>;
}

const DEFAULT_MAX_ATTEMPTS = 4;
const DEFAULT_BACKOFF_MS = 500;
const RETRY_STATUS = new Set([408, 425, 429, 500, 502, 503, 504]);

export class MddbHttpError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly body: string,
  ) {
    super(message);
    this.name = 'MddbHttpError';
  }
}

export class MddbClient {
  private readonly baseUrl: string;
  private readonly apiKey: string;
  private readonly timeoutMs: number;
  private readonly fetchImpl: typeof fetch;
  private readonly dispatcher: Dispatcher | undefined;
  private readonly maxAttempts: number;
  private readonly backoffMs: number;
  private readonly sleep: (ms: number) => Promise<void>;

  constructor(opts: MddbClientOptions) {
    this.baseUrl = opts.baseUrl.replace(/\/+$/, '');
    this.apiKey = opts.apiKey;
    this.timeoutMs = Math.max(1, opts.timeoutSeconds) * 1000;
    this.fetchImpl = opts.fetchImpl ?? fetch;
    this.maxAttempts = opts.maxAttempts ?? DEFAULT_MAX_ATTEMPTS;
    this.backoffMs = opts.backoffMs ?? DEFAULT_BACKOFF_MS;
    this.sleep = opts.sleepImpl ?? defaultSleep;
    // Node's global fetch is undici, which ignores the `agent` option a
    // node:https.Agent would provide and honours `dispatcher` instead — so a
    // permissive https.Agent silently did nothing here (INT-013).
    this.dispatcher = opts.verifySsl
      ? undefined
      : new Agent({ connect: { rejectUnauthorized: false } });
  }

  /**
   * Cheap connectivity + auth probe. Accepts 2xx/404/405 (instance alive),
   * rejects 401/403 (auth) and 5xx (server unhealthy).
   */
  async ping(): Promise<void> {
    const body = { collection: '_gh_action_probe', query: '*', limit: 1 };
    const response = await this.rawRequest('/v1/search', body, /* allowRetry */ false);
    if (response.status === 401 || response.status === 403) {
      throw new MddbHttpError(
        `MDDB rejected credentials (HTTP ${response.status})`,
        response.status,
        response.bodyText,
      );
    }
    if (response.status >= 500) {
      throw new MddbHttpError(
        `MDDB server error (HTTP ${response.status})`,
        response.status,
        response.bodyText,
      );
    }
    if (response.status >= 200 && response.status < 300) return;
    if (response.status === 404 || response.status === 405) return;
    throw new MddbHttpError(
      `Unexpected /v1/search response (HTTP ${response.status})`,
      response.status,
      response.bodyText,
    );
  }

  async addDocument(doc: MddbDocument): Promise<void> {
    const response = await this.rawRequest('/v1/add', doc, /* allowRetry */ true);
    if (response.status >= 200 && response.status < 300) return;
    throw new MddbHttpError(
      `MDDB /v1/add failed for key=${doc.key} (HTTP ${response.status})`,
      response.status,
      response.bodyText,
    );
  }

  private async rawRequest(
    path: string,
    body: unknown,
    allowRetry: boolean,
  ): Promise<{ status: number; bodyText: string }> {
    const url = `${this.baseUrl}${path}`;
    const headers: Record<string, string> = { 'Content-Type': 'application/json' };
    if (this.apiKey) headers.Authorization = `Bearer ${this.apiKey}`;

    let lastErr: unknown;
    const attempts = allowRetry ? this.maxAttempts : 1;
    for (let attempt = 1; attempt <= attempts; attempt++) {
      const controller = new AbortController();
      const timeout = setTimeout(() => controller.abort(), this.timeoutMs);
      try {
        const init: RequestInit = {
          method: 'POST',
          headers,
          body: JSON.stringify(body),
          signal: controller.signal,
        };
        if (this.dispatcher) {
          (init as RequestInit & { dispatcher?: Dispatcher }).dispatcher = this.dispatcher;
        }
        const res = await this.fetchImpl(url, init);
        const text = await res.text();
        if (res.ok || !RETRY_STATUS.has(res.status) || attempt === attempts) {
          return { status: res.status, bodyText: text };
        }
        lastErr = new MddbHttpError(`Retryable HTTP ${res.status}`, res.status, text);
      } catch (err) {
        lastErr = err;
        if (attempt === attempts) break;
      } finally {
        clearTimeout(timeout);
      }
      await this.sleep(this.backoffMs * 2 ** (attempt - 1));
    }
    throw lastErr instanceof Error ? lastErr : new Error(String(lastErr));
  }
}

/* istanbul ignore next — exercised in production only; tests inject sleepImpl. */
function defaultSleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
