// Minimal chrome.* API mock for jest + jsdom.
// Provide a tiny Response stub — the client only ever calls .ok, .status, .json().
const g = globalThis as unknown as Record<string, unknown>;
if (typeof g.Response === 'undefined') {
  class StubResponse {
    body: string;
    status: number;
    ok: boolean;
    constructor(body: string, init: { status?: number } = {}) {
      this.body = body;
      this.status = init.status ?? 200;
      this.ok = this.status >= 200 && this.status < 300;
    }
    async json() {
      return JSON.parse(this.body);
    }
    async text() {
      return this.body;
    }
  }
  g.Response = StubResponse as unknown as typeof Response;
}

type StorageArea = Record<string, unknown>;
const storage: StorageArea = {};

type Listener = (...args: unknown[]) => void;

const listeners = {
  storageChanged: [] as Listener[],
  runtimeMessage: [] as Listener[],
  alarms: [] as Listener[],
  installed: [] as Listener[],
  startup: [] as Listener[],
};

const chromeMock = {
  storage: {
    local: {
      get: jest.fn(async (key?: string | string[] | Record<string, unknown>) => {
        if (key == null) return { ...storage };
        if (typeof key === 'string') {
          return { [key]: storage[key] };
        }
        if (Array.isArray(key)) {
          const out: StorageArea = {};
          for (const k of key) out[k] = storage[k];
          return out;
        }
        const out: StorageArea = {};
        for (const k of Object.keys(key)) out[k] = storage[k] ?? key[k];
        return out;
      }),
      set: jest.fn(async (items: StorageArea) => {
        const changes: Record<string, { oldValue?: unknown; newValue?: unknown }> = {};
        for (const [k, v] of Object.entries(items)) {
          changes[k] = { oldValue: storage[k], newValue: v };
          storage[k] = v;
        }
        for (const l of listeners.storageChanged) l(changes, 'local');
      }),
      remove: jest.fn(async (k: string) => {
        delete storage[k];
      }),
      clear: jest.fn(async () => {
        for (const k of Object.keys(storage)) delete storage[k];
      }),
    },
    onChanged: {
      addListener: (cb: Listener) => listeners.storageChanged.push(cb),
    },
  },
  runtime: {
    id: 'mddb-test-extension-id',
    openOptionsPage: jest.fn(),
    sendMessage: jest.fn((msg: unknown, cb?: (resp: unknown) => void) => {
      if (cb) cb({ status: null });
    }),
    onMessage: {
      addListener: (cb: Listener) => listeners.runtimeMessage.push(cb),
    },
    onInstalled: {
      addListener: (cb: Listener) => listeners.installed.push(cb),
    },
    onStartup: {
      addListener: (cb: Listener) => listeners.startup.push(cb),
    },
    lastError: undefined as unknown,
  },
  alarms: {
    create: jest.fn(async () => undefined),
    clear: jest.fn(async () => true),
    onAlarm: {
      addListener: (cb: Listener) => listeners.alarms.push(cb),
    },
  },
  action: {
    setBadgeText: jest.fn(async () => undefined),
    setBadgeBackgroundColor: jest.fn(async () => undefined),
    setTitle: jest.fn(async () => undefined),
  },
  permissions: {
    request: jest.fn(async () => true),
    remove: jest.fn(async () => true),
  },
};

(globalThis as unknown as { chrome: typeof chromeMock }).chrome = chromeMock;

beforeEach(() => {
  for (const k of Object.keys(storage)) delete storage[k];
  listeners.storageChanged.length = 0;
  listeners.runtimeMessage.length = 0;
  listeners.alarms.length = 0;
  listeners.installed.length = 0;
  listeners.startup.length = 0;
  jest.clearAllMocks();
  chromeMock.runtime.lastError = undefined;
  chromeMock.permissions.request.mockImplementation(async () => true);
  chromeMock.permissions.remove.mockImplementation(async () => true);
  chromeMock.runtime.sendMessage.mockImplementation(
    (msg: unknown, cb?: (resp: unknown) => void) => {
      if (cb) cb({ status: null });
    },
  );
});

export { chromeMock, listeners, storage };
