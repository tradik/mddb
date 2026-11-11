import { WsClient } from './ws/client';
import { feedbackMessage, joinMessage, ratedTurn } from './ws/messages';
import { buildCommentBox } from './ui/feedbackComment';
import type { WsIncoming } from './ws/protocol';
import { Store, type WidgetState, type Message } from './store/state';
import { saveSession, loadSession, clearSession, setSessionTtl } from './store/session';
import { sanitizeInput } from './utils/sanitize';
import { renderMarkdown } from './utils/markdown';
import styles from './styles/widget.css?inline';

export interface WidgetOptions {
  server: string;
  scenario?: string;
  theme?: 'light' | 'dark';
  accent?: string;
  position?: 'bottom-right' | 'bottom-left';
  sessionTtlHours?: number;
  /**
   * Returns the host's signed join token (#405), asked at every join so a
   * refreshed token is used on the next reconnect. Anonymous without it.
   */
  identityToken?: () => string | undefined;
}

const CHAT_ICON_SVG = `<svg viewBox="0 0 24 24"><path d="M20 2H4c-1.1 0-2 .9-2 2v18l4-4h14c1.1 0 2-.9 2-2V4c0-1.1-.9-2-2-2zm0 14H6l-2 2V4h16v12z"/></svg>`;
const SEND_ICON_SVG = `<svg viewBox="0 0 24 24"><path d="M2.01 21L23 12 2.01 3 2 10l15 2-15 2z"/></svg>`;
const THUMB_UP_SVG = `<svg viewBox="0 0 24 24" width="14" height="14"><path d="M1 21h4V9H1v12zm22-11c0-1.1-.9-2-2-2h-6.31l.95-4.57.03-.32c0-.41-.17-.79-.44-1.06L14.17 1 7.59 7.59C7.22 7.95 7 8.45 7 9v10c0 1.1.9 2 2 2h9c.83 0 1.54-.5 1.84-1.22l3.02-7.05c.09-.23.14-.47.14-.73v-2z"/></svg>`;
const THUMB_DOWN_SVG = `<svg viewBox="0 0 24 24" width="14" height="14"><path d="M15 3H6c-.83 0-1.54.5-1.84 1.22l-3.02 7.05c-.09.23-.14.47-.14.73v2c0 1.1.9 2 2 2h6.31l-.95 4.57-.03.32c0 .41.17.79.44 1.06L9.83 23l6.59-6.59c.36-.36.58-.86.58-1.41V5c0-1.1-.9-2-2-2zm4 0v12h4V3h-4z"/></svg>`;
const CLOSE_ICON = '\u00D7';

export class ChatWidget {
  private store: Store;
  private ws: WsClient;
  private options: WidgetOptions;
  private root: ShadowRoot;
  private container: HTMLDivElement;

  // DOM references
  private windowEl!: HTMLDivElement;
  private messagesEl!: HTMLDivElement;
  private inputEl!: HTMLTextAreaElement;
  private sendBtn!: HTMLButtonElement;
  private nameFormEl!: HTMLDivElement;
  private chatViewEl!: HTMLDivElement;
  private typingEl!: HTMLDivElement;
  private queueEl!: HTMLDivElement;
  private errorEl!: HTMLDivElement;
  private endBtn!: HTMLButtonElement;
  private ratedMessages: Set<number> = new Set();
  // #406: the answer whose comment box is open after a thumbs-down, what has
  // been typed in it (a re-render must not lose it), and whether it still
  // needs the focus.
  private commentFor: number | null = null;
  private commentDraft = '';
  private focusComment = false;

  constructor(hostEl: HTMLElement, options: WidgetOptions) {
    this.options = options;
    this.store = new Store();

    // Configure session TTL
    if (options.sessionTtlHours) {
      setSessionTtl(options.sessionTtlHours);
    }

    // Create shadow DOM
    this.root = hostEl.attachShadow({ mode: 'closed' });
    this.container = document.createElement('div');
    this.root.appendChild(this.container);

    // Inject styles
    const styleEl = document.createElement('style');
    styleEl.textContent = styles;
    this.root.appendChild(styleEl);

    // Apply custom accent
    if (options.accent) {
      this.container.style.setProperty('--mddb-accent', options.accent);
    }

    // WebSocket
    this.ws = new WsClient(
      options.server,
      (msg) => this.handleMessage(msg),
      (connected) => {
        this.store.update({ isConnected: connected });
        // Auto-rejoin on reconnect if we have a saved session
        if (connected) {
          const state = this.store.getState();
          if (state.sessionId && !state.isJoined) {
            this.ws.send({ type: 'resume', session_id: state.sessionId });
          }
        }
      },
    );

    // Subscribe to state changes
    this.store.subscribe((state) => this.render(state));

    // Build UI
    this.buildUI();

    // Try to resume session from localStorage
    const saved = loadSession();
    if (saved) {
      this.store.update({
        userName: saved.userName,
        sessionId: saved.sessionId,
        messages: saved.messages || [],
      });
    }
  }

  private buildUI(): void {
    // Chat icon button
    const iconBtn = document.createElement('button');
    iconBtn.className = 'mddb-chat-icon';
    iconBtn.innerHTML = CHAT_ICON_SVG;
    iconBtn.addEventListener('click', () => this.toggle());
    if (this.options.position === 'bottom-left') {
      iconBtn.style.left = '20px';
      iconBtn.style.right = 'auto';
    }
    this.container.appendChild(iconBtn);

    // Chat window
    this.windowEl = document.createElement('div');
    this.windowEl.className = 'mddb-chat-window hidden';
    if (this.options.position === 'bottom-left') {
      this.windowEl.style.left = '20px';
      this.windowEl.style.right = 'auto';
    }

    // Header
    const header = document.createElement('div');
    header.className = 'mddb-header';
    header.innerHTML = `
      <img class="mddb-header-logo" src="data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHZpZXdCb3g9IjAgMCA1MTIgNTEyIiB3aWR0aD0iMTAwJSIgaGVpZ2h0PSIxMDAlIj4KICA8ZGVmcz4KICAgIDxsaW5lYXJHcmFkaWVudCBpZD0iYmdHcmFkIiB4MT0iMCUiIHkxPSIwJSIgeDI9IjEwMCUiIHkyPSIxMDAlIj4KICAgICAgPHN0b3Agb2Zmc2V0PSIwJSIgc3RvcC1jb2xvcj0iIzBmMTcyYSIvPgogICAgICA8c3RvcCBvZmZzZXQ9IjEwMCUiIHN0b3AtY29sb3I9IiMxZTI5M2IiLz4KICAgIDwvbGluZWFyR3JhZGllbnQ+CgogICAgPGxpbmVhckdyYWRpZW50IGlkPSJoZXhHcmFkIiB4MT0iMCUiIHkxPSIwJSIgeDI9IjAlIiB5Mj0iMTAwJSI+CiAgICAgIDxzdG9wIG9mZnNldD0iMCUiIHN0b3AtY29sb3I9IiMwMGQyZmYiLz4KICAgICAgPHN0b3Agb2Zmc2V0PSIxMDAlIiBzdG9wLWNvbG9yPSIjMDA3MmZmIi8+CiAgICA8L2xpbmVhckdyYWRpZW50PgoKICAgIDxsaW5lYXJHcmFkaWVudCBpZD0idHJpR3JhZCIgeDE9IjAlIiB5MT0iMCUiIHgyPSIwJSIgeTI9IjEwMCUiPgogICAgICA8c3RvcCBvZmZzZXQ9IjAlIiBzdG9wLWNvbG9yPSIjMDBmMmZlIi8+CiAgICAgIDxzdG9wIG9mZnNldD0iMTAwJSIgc3RvcC1jb2xvcj0iIzRmYWNmZSIvPgogICAgPC9saW5lYXJHcmFkaWVudD4KCiAgICA8ZmlsdGVyIGlkPSJnbG93IiB4PSItMjAlIiB5PSItMjAlIiB3aWR0aD0iMTQwJSIgaGVpZ2h0PSIxNDAlIj4KICAgICAgPGZlR2F1c3NpYW5CbHVyIHN0ZERldmlhdGlvbj0iMTAiIHJlc3VsdD0iYmx1ciIgLz4KICAgICAgPGZlQ29tcG9zaXRlIGluPSJTb3VyY2VHcmFwaGljIiBpbjI9ImJsdXIiIG9wZXJhdG9yPSJvdmVyIiAvPgogICAgPC9maWx0ZXI+CiAgPC9kZWZzPgoKICA8cmVjdCB4PSIzMiIgeT0iMzIiIHdpZHRoPSI0NDgiIGhlaWdodD0iNDQ4IiByeD0iOTYiIGZpbGw9InVybCgjYmdHcmFkKSIgLz4KCiAgPHBvbHlnb24gcG9pbnRzPSIyNTYsOTYgMzk0LDE3NiAzOTQsMzM2IDI1Niw0MTYgMTE4LDMzNiAxMTgsMTc2IiAKICAgICAgICAgICBmaWxsPSJ1cmwoI2hleEdyYWQpIiAKICAgICAgICAgICBmaWx0ZXI9InVybCgjZ2xvdykiLz4KCiAgPHBvbHlnb24gcG9pbnRzPSIyNTYsMTIwIDM3MywxODcgMzczLDMyNSAyNTYsMzkyIDEzOSwzMjUgMTM5LDE4NyIgCiAgICAgICAgICAgZmlsbD0iIzBmMTcyYSIgLz4KCiAgPHBvbHlnb24gcG9pbnRzPSIyNTYsMTY1IDM0NSwzMjAgMTY3LDMyMCIgCiAgICAgICAgICAgZmlsbD0idXJsKCN0cmlHcmFkKSIgCiAgICAgICAgICAgZmlsdGVyPSJ1cmwoI2dsb3cpIiAvPgoKICA8cG9seWdvbiBwb2ludHM9IjI1NiwxODUgMzMwLDMxMCAxODIsMzEwIiAKICAgICAgICAgICBmaWxsPSIjMDBiY2Q0IiAvPgoKICA8cG9seWdvbiBwb2ludHM9IjI1NiwyMDAgMzE1LDMwMCAxOTcsMzAwIiAKICAgICAgICAgICBmaWxsPSJ1cmwoI3RyaUdyYWQpIiAvPgoKICA8cGF0aCBkPSJNIDI1NiAyMjAgUSAyNTYgMjUwIDIyNiAyNTAgUSAyNTYgMjUwIDI1NiAyODAgUSAyNTYgMjUwIDI4NiAyNTAgUSAyNTYgMjUwIDI1NiAyMjAgWiIgCiAgICAgICAgZmlsbD0iI2ZmZmZmZiIgCiAgICAgICAgZmlsdGVyPSJ1cmwoI2dsb3cpIi8+Cjwvc3ZnPg==" alt="" width="28" height="28">
      <div>
        <div class="mddb-header-title">Chat Support</div>
        <div class="mddb-header-status">Online</div>
      </div>
    `;

    const headerBtns = document.createElement('div');
    headerBtns.style.display = 'flex';
    headerBtns.style.alignItems = 'center';
    headerBtns.style.gap = '4px';

    // End chat button
    this.endBtn = document.createElement('button');
    this.endBtn.className = 'mddb-header-end';
    this.endBtn.textContent = 'End';
    this.endBtn.title = 'End chat session';
    this.endBtn.style.display = 'none';
    this.endBtn.addEventListener('click', () => this.endChat());
    headerBtns.appendChild(this.endBtn);

    // Close (minimize) button
    const closeBtn = document.createElement('button');
    closeBtn.className = 'mddb-header-close';
    closeBtn.textContent = CLOSE_ICON;
    closeBtn.addEventListener('click', () => this.toggle());
    headerBtns.appendChild(closeBtn);

    header.appendChild(headerBtns);
    this.windowEl.appendChild(header);

    // Error bar
    this.errorEl = document.createElement('div');
    this.errorEl.className = 'mddb-error';
    this.errorEl.style.display = 'none';
    this.windowEl.appendChild(this.errorEl);

    // Queue notice
    this.queueEl = document.createElement('div');
    this.queueEl.className = 'mddb-queue-notice';
    this.queueEl.style.display = 'none';
    this.windowEl.appendChild(this.queueEl);

    // Name form
    this.nameFormEl = document.createElement('div');
    this.nameFormEl.className = 'mddb-name-form';
    this.nameFormEl.innerHTML = `
      <h3>Welcome!</h3>
      <p>Enter your name to start chatting</p>
    `;
    const nameInput = document.createElement('input');
    nameInput.className = 'mddb-name-input';
    nameInput.type = 'text';
    nameInput.placeholder = 'Your name...';
    nameInput.maxLength = 50;

    const nameSubmit = document.createElement('button');
    nameSubmit.className = 'mddb-name-submit';
    nameSubmit.textContent = 'Start Chat';

    const submitName = () => {
      const name = sanitizeInput(nameInput.value, 50);
      if (name) this.join(name);
    };

    nameSubmit.addEventListener('click', submitName);
    nameInput.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') submitName();
    });

    this.nameFormEl.appendChild(nameInput);
    this.nameFormEl.appendChild(nameSubmit);
    this.windowEl.appendChild(this.nameFormEl);

    // Chat view (messages + input)
    this.chatViewEl = document.createElement('div');
    this.chatViewEl.style.display = 'none';
    this.chatViewEl.style.flex = '1';
    this.chatViewEl.style.flexDirection = 'column';
    this.chatViewEl.style.overflow = 'hidden';

    this.messagesEl = document.createElement('div');
    this.messagesEl.className = 'mddb-messages';

    this.typingEl = document.createElement('div');
    this.typingEl.className = 'mddb-typing';
    this.typingEl.style.display = 'none';
    this.typingEl.innerHTML = `
      <div class="mddb-typing-dot"></div>
      <div class="mddb-typing-dot"></div>
      <div class="mddb-typing-dot"></div>
    `;

    const inputArea = document.createElement('div');
    inputArea.className = 'mddb-input-area';

    this.inputEl = document.createElement('textarea');
    this.inputEl.className = 'mddb-input';
    this.inputEl.placeholder = 'Type a message...';
    this.inputEl.rows = 1;
    this.inputEl.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        this.sendMessage();
      }
    });
    this.inputEl.addEventListener('input', () => {
      this.inputEl.style.height = 'auto';
      this.inputEl.style.height = Math.min(this.inputEl.scrollHeight, 80) + 'px';
    });

    this.sendBtn = document.createElement('button');
    this.sendBtn.className = 'mddb-send-btn';
    this.sendBtn.innerHTML = SEND_ICON_SVG;
    this.sendBtn.addEventListener('click', () => this.sendMessage());

    inputArea.appendChild(this.inputEl);
    inputArea.appendChild(this.sendBtn);

    // Powered by
    const powered = document.createElement('div');
    powered.className = 'mddb-powered';
    powered.textContent = 'Powered by MDDB';

    this.chatViewEl.appendChild(this.messagesEl);
    this.chatViewEl.appendChild(this.typingEl);
    this.chatViewEl.appendChild(inputArea);
    this.chatViewEl.appendChild(powered);
    this.windowEl.appendChild(this.chatViewEl);

    this.container.appendChild(this.windowEl);
  }

  private toggle(): void {
    const isOpen = !this.store.getState().isOpen;
    this.store.update({ isOpen });

    if (isOpen && !this.ws.isConnected) {
      this.ws.connect();
    }
  }

  private join(name: string): void {
    this.store.update({ userName: name });
    const send = () =>
      this.ws.send(
        joinMessage(name, this.options.scenario || 'assistant', this.options.identityToken?.()),
      );

    if (!this.ws.isConnected) {
      this.ws.connect();
      // Wait for connection, then join
      const unsub = this.store.subscribe((state) => {
        if (state.isConnected) {
          unsub();
          send();
        }
      });
    } else {
      send();
    }
  }

  private endChat(): void {
    this.ws.send({ type: 'end' });
    clearSession();
    this.store.update({
      isJoined: false,
      sessionId: null,
      messages: [],
      isStreaming: false,
    });
  }

  private sendMessage(): void {
    const state = this.store.getState();
    if (state.isStreaming || !state.isJoined) return;

    const content = sanitizeInput(this.inputEl.value, 2000);
    if (!content) return;

    this.inputEl.value = '';
    this.inputEl.style.height = 'auto';

    this.store.addMessage('user', content);
    this.store.update({ isStreaming: true });

    this.ws.send({ type: 'message', content });
  }

  private sendFeedback(rating: 'up' | 'down', msgIndex: number): void {
    if (this.ratedMessages.has(msgIndex)) return;
    this.ratedMessages.add(msgIndex);

    const { question, answer, messageId } = ratedTurn(this.store.getState().messages, msgIndex);
    this.ws.send(feedbackMessage(rating, question, answer, messageId));
    if (rating === 'down') {
      // #406: offer a comment; the rating is already stored without one.
      this.commentFor = msgIndex;
      this.commentDraft = '';
      this.focusComment = true;
    }

    // Re-render to update button states
    this.render(this.store.getState());
  }

  /** Sends the comment with the same rating again (#406), then closes the box. */
  private sendComment(msgIndex: number, text: string): void {
    const { question, answer, messageId } = ratedTurn(this.store.getState().messages, msgIndex);
    if (text.trim()) this.ws.send(feedbackMessage('down', question, answer, messageId, text));
    this.closeComment();
  }

  private closeComment(): void {
    this.commentFor = null;
    this.commentDraft = '';
    this.render(this.store.getState());
  }

  /** The comment box under answer `i`, focused the first time it is shown. */
  private commentBox(i: number): HTMLFormElement {
    const form = buildCommentBox(document, {
      id: `mddb-comment-${i}`,
      draft: this.commentDraft,
      onInput: (text) => { this.commentDraft = text; },
      onSend: (text) => this.sendComment(i, text),
      onSkip: () => this.closeComment(),
    });
    if (this.focusComment) {
      this.focusComment = false;
      queueMicrotask(() => form.querySelector('textarea')?.focus());
    }
    return form;
  }

  private persistMessages(): void {
    const state = this.store.getState();
    if (state.sessionId && state.userName) {
      saveSession(state.sessionId, state.userName, state.messages);
    }
  }

  private handleMessage(msg: WsIncoming): void {
    switch (msg.type) {
      case 'session':
        this.store.update({
          isJoined: true,
          isQueued: false,
          sessionId: msg.id,
        });
        this.persistMessages();
        break;

      case 'queued':
        this.store.update({
          isQueued: true,
          queuePosition: msg.position,
        });
        break;

      case 'chunk':
        this.store.appendToLastAssistant(msg.content);
        break;

      case 'done':
        this.store.tagLastAssistant(msg.message_id);
        this.store.update({ isStreaming: false });
        this.persistMessages();
        break;

      case 'ended':
        clearSession();
        this.store.update({
          isJoined: false,
          sessionId: null,
          messages: [],
          isStreaming: false,
        });
        break;

      case 'error':
        this.store.update({
          error: msg.message,
          isStreaming: false,
        });
        // If session expired on server, try to rejoin
        if (msg.message.includes('session not found') || msg.message.includes('must join first')) {
          const state = this.store.getState();
          if (state.userName) {
            this.join(state.userName);
          }
        }
        // Clear error after 5s
        setTimeout(() => this.store.update({ error: null }), 5000);
        break;

      case 'pong':
        break;
    }
  }

  private render(state: WidgetState): void {
    // Window visibility
    this.windowEl.classList.toggle('hidden', !state.isOpen);

    // Show/hide end button based on join status
    this.endBtn.style.display = state.isJoined ? 'block' : 'none';

    // Name form vs chat view
    if (state.isJoined) {
      this.nameFormEl.style.display = 'none';
      this.chatViewEl.style.display = 'flex';
    } else if (state.sessionId && state.messages.length > 0) {
      // Have saved history but not joined yet — show chat as read-only + rejoin
      this.nameFormEl.style.display = 'none';
      this.chatViewEl.style.display = 'flex';
    } else {
      this.nameFormEl.style.display = 'flex';
      this.chatViewEl.style.display = 'none';
    }

    // Queue notice
    if (state.isQueued) {
      this.queueEl.style.display = 'block';
      this.queueEl.textContent = `You are #${state.queuePosition} in queue. Please wait...`;
    } else {
      this.queueEl.style.display = 'none';
    }

    // Error
    if (state.error) {
      this.errorEl.style.display = 'block';
      this.errorEl.textContent = state.error;
    } else {
      this.errorEl.style.display = 'none';
    }

    // Messages
    this.renderMessages(state.messages);

    // Typing indicator
    this.typingEl.style.display = state.isStreaming ? 'flex' : 'none';

    // Input state
    this.sendBtn.disabled = state.isStreaming || !state.isJoined;
    this.inputEl.disabled = state.isStreaming || !state.isJoined;
  }

  private renderMessages(messages: Message[]): void {
    const state = this.store.getState();
    // Count actual message wrappers (each has class mddb-msg-wrap)
    const currentCount = this.messagesEl.querySelectorAll('.mddb-msg-wrap').length;

    if (currentCount === messages.length && messages.length > 0) {
      // Update last message content (for streaming)
      const last = messages[messages.length - 1];
      const lastWrap = this.messagesEl.lastElementChild as HTMLDivElement;
      const lastEl = lastWrap?.querySelector('.mddb-message') as HTMLDivElement;
      if (lastEl && last.role === 'assistant') {
        lastEl.innerHTML = renderMarkdown(last.content);
      }
      this.scrollToBottom();
      return;
    }

    this.messagesEl.innerHTML = '';
    for (let i = 0; i < messages.length; i++) {
      const msg = messages[i];
      const wrap = document.createElement('div');
      wrap.className = `mddb-msg-wrap ${msg.role}`;

      const el = document.createElement('div');
      el.className = `mddb-message ${msg.role}`;
      el.innerHTML = msg.role === 'assistant'
        ? renderMarkdown(msg.content)
        : this.escapeHtml(msg.content);
      wrap.appendChild(el);

      // Add feedback buttons for completed assistant messages (not the one still streaming)
      if (msg.role === 'assistant' && !(i === messages.length - 1 && state.isStreaming)) {
        const feedbackBar = document.createElement('div');
        feedbackBar.className = 'mddb-feedback';

        const rated = this.ratedMessages.has(i);

        const upBtn = document.createElement('button');
        upBtn.className = `mddb-feedback-btn${rated ? ' rated' : ''}`;
        upBtn.innerHTML = THUMB_UP_SVG;
        upBtn.title = 'Good response';
        upBtn.disabled = rated;
        upBtn.addEventListener('click', () => this.sendFeedback('up', i));

        const downBtn = document.createElement('button');
        downBtn.className = `mddb-feedback-btn${rated ? ' rated' : ''}`;
        downBtn.innerHTML = THUMB_DOWN_SVG;
        downBtn.title = 'Bad response';
        downBtn.disabled = rated;
        downBtn.addEventListener('click', () => this.sendFeedback('down', i));

        feedbackBar.appendChild(upBtn);
        feedbackBar.appendChild(downBtn);
        wrap.appendChild(feedbackBar);
        if (this.commentFor === i) wrap.appendChild(this.commentBox(i));
      }

      this.messagesEl.appendChild(wrap);
    }
    this.scrollToBottom();
  }

  private scrollToBottom(): void {
    this.messagesEl.scrollTop = this.messagesEl.scrollHeight;
  }

  private escapeHtml(text: string): string {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
  }

  destroy(): void {
    this.ws.disconnect();
    clearSession();
  }
}
