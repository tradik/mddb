import type { WsIncoming, WsOutgoing } from './protocol';

export type MessageHandler = (msg: WsIncoming) => void;

export class WsClient {
  private ws: WebSocket | null = null;
  private url: string;
  private onMessage: MessageHandler;
  private onStatusChange: (connected: boolean) => void;
  private reconnectAttempts = 0;
  private maxReconnectAttempts = 10;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private pingTimer: ReturnType<typeof setInterval> | null = null;

  constructor(
    url: string,
    onMessage: MessageHandler,
    onStatusChange: (connected: boolean) => void,
  ) {
    this.url = url;
    this.onMessage = onMessage;
    this.onStatusChange = onStatusChange;
  }

  connect(): void {
    if (this.ws?.readyState === WebSocket.OPEN) return;

    try {
      this.ws = new WebSocket(this.url);

      this.ws.onopen = () => {
        this.reconnectAttempts = 0;
        this.onStatusChange(true);
        this.startPing();
      };

      this.ws.onmessage = (event) => {
        try {
          const msg: WsIncoming = JSON.parse(event.data);
          this.onMessage(msg);
        } catch {
          // Bounded and single-line. The payload is whatever arrived on the
          // socket, so dumping it raw meant an unbounded write into the
          // console and, with newlines in it, a frame that could forge
          // entries around itself (CodeQL js/log-injection). A prefix is
          // enough to recognise what failed to parse.
          console.error('[mddb-chat] invalid message:', summariseForLog(event.data));
        }
      };

      this.ws.onclose = () => {
        this.onStatusChange(false);
        this.stopPing();
        this.scheduleReconnect();
      };

      this.ws.onerror = () => {
        // onclose will fire after this
      };
    } catch {
      this.scheduleReconnect();
    }
  }

  send(msg: WsOutgoing): void {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(msg));
    }
  }

  disconnect(): void {
    this.stopPing();
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    if (this.ws) {
      this.ws.onclose = null;
      this.ws.close();
      this.ws = null;
    }
  }

  get isConnected(): boolean {
    return this.ws?.readyState === WebSocket.OPEN;
  }

  private scheduleReconnect(): void {
    if (this.reconnectAttempts >= this.maxReconnectAttempts) return;

    const delay = Math.min(1000 * Math.pow(2, this.reconnectAttempts), 30000);
    this.reconnectAttempts++;

    this.reconnectTimer = setTimeout(() => {
      this.connect();
    }, delay);
  }

  private startPing(): void {
    this.pingTimer = setInterval(() => {
      this.send({ type: 'ping' });
    }, 30000);
  }

  private stopPing(): void {
    if (this.pingTimer) {
      clearInterval(this.pingTimer);
      this.pingTimer = null;
    }
  }
}

/**
 * Renders an untrusted value for a single console line.
 *
 * Newlines and carriage returns become spaces so one frame cannot look like
 * several log entries, and the result is capped — a malformed 100 KB frame is
 * not more diagnostic than its first 200 characters.
 */
function summariseForLog(value: unknown): string {
  const text = typeof value === 'string' ? value : String(value);
  const oneLine = text.replace(/[\r\n]+/g, ' ');
  return oneLine.length > 200 ? `${oneLine.slice(0, 200)}… (${text.length} chars)` : oneLine;
}
