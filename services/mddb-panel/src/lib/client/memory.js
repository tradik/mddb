/**
 * Agent memory (Memory RAG) calls: sessions, their messages, semantic recall
 * and summaries. Mixed into the MDDB client, so `this.request` is the
 * client's authenticated JSON request. Every route is a POST with a JSON body
 * (docs/API.md, "Memory RAG Endpoints"); there is no endpoint that deletes or
 * ends a session — sessions expire after their TTL.
 */

import { postJson as post } from './http';

export const memoryMethods = {
  /** Starts a session: `{ userId, scenario?, title?, meta?, ttl? }` → the session. */
  createMemorySession(session) {
    return post(this, '/memory/session', session);
  },

  /** Lists sessions, filtered by `userId`/`scenario`, newest first by default. */
  listMemorySessions({ userId, scenario, limit = 50, offset = 0, sort = 'updatedAt', asc = false } = {}) {
    return post(this, '/memory/sessions', { userId, scenario, limit, offset, sort, asc });
  },

  /** A session's messages as documents: `{ messages, total }`. */
  getMemoryHistory({ sessionId, limit = 100, offset = 0 }) {
    return post(this, '/memory/history', { sessionId, limit, offset });
  },

  /** Stores one message (`role`: user, assistant, system or tool). */
  addMemoryMessage({ sessionId, role, content }) {
    return post(this, '/memory/message', { sessionId, role, content });
  },

  /** Semantic recall across sessions: `{ results, total, strategy }`. */
  recallMemory({ query, userId, sessionId, topK = 10, strategy = 'hybrid', includeContent = true }) {
    return post(this, '/memory/recall', { query, userId, sessionId, topK, strategy, includeContent });
  },

  /** Writes and returns a summary of the session. */
  summarizeMemorySession({ sessionId }) {
    return post(this, '/memory/summarize', { sessionId });
  },
};
