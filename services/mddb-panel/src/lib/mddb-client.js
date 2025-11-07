/**
 * MDDB API Client
 * Simple client for interacting with MDDB HTTP API
 */

const API_BASE = import.meta.env.MODE === 'production' 
  ? `http://${import.meta.env.VITE_MDBB_SERVER || 'localhost:11023'}/v1` 
  : '/v1';

class MDDBClient {
  constructor(baseUrl = API_BASE) {
    this.baseUrl = baseUrl;
  }

  async request(endpoint, options = {}) {
    const url = `${this.baseUrl}${endpoint}`;
    const config = {
      headers: {
        'Content-Type': 'application/json',
        ...options.headers,
      },
      ...options,
    };

    try {
      const response = await fetch(url, config);
      
      if (!response.ok) {
        const error = await response.text();
        throw new Error(`API Error (${response.status}): ${error}`);
      }

      return await response.json();
    } catch (error) {
      console.error('MDDB API Error:', error);
      throw error;
    }
  }

  /**
   * Get server statistics
   */
  async getStats() {
    return this.request('/stats', { method: 'GET' });
  }

  /**
   * Search documents in a collection
   */
  async search({ collection, filterMeta = {}, sort = 'addedAt', asc = false, limit = 100 }) {
    return this.request('/search', {
      method: 'POST',
      body: JSON.stringify({
        collection,
        filterMeta,
        sort,
        asc,
        limit,
      }),
    });
  }

  /**
   * Get a specific document
   */
  async getDocument({ collection, key, lang, env = {} }) {
    return this.request('/get', {
      method: 'POST',
      body: JSON.stringify({
        collection,
        key,
        lang,
        env,
      }),
    });
  }

  /**
   * Add or update a document
   */
  async addDocument({ collection, key, lang, meta = {}, contentMd }) {
    return this.request('/add', {
      method: 'POST',
      body: JSON.stringify({
        collection,
        key,
        lang,
        meta,
        contentMd,
      }),
    });
  }

  /**
   * Export documents
   */
  async export({ collection, filterMeta = {}, format = 'ndjson' }) {
    return this.request('/export', {
      method: 'POST',
      body: JSON.stringify({
        collection,
        filterMeta,
        format,
      }),
    });
  }

  /**
   * Create database backup
   */
  async backup(filename) {
    const url = `${this.baseUrl}/backup${filename ? `?to=${filename}` : ''}`;
    const response = await fetch(url, { method: 'GET' });
    
    if (!response.ok) {
      throw new Error(`Backup failed: ${response.statusText}`);
    }
    
    return response.json();
  }

  /**
   * Truncate old revisions
   */
  async truncate({ collection, keepRevs = 3, dropCache = true }) {
    return this.request('/truncate', {
      method: 'POST',
      body: JSON.stringify({
        collection,
        keepRevs,
        dropCache,
      }),
    });
  }

  /**
   * Delete a single document
   */
  async deleteDocument({ collection, key, lang }) {
    return this.request('/delete', {
      method: 'POST',
      body: JSON.stringify({
        collection,
        key,
        lang,
      }),
    });
  }

  /**
   * Delete entire collection
   */
  async deleteCollection({ collection }) {
    return this.request('/delete-collection', {
      method: 'POST',
      body: JSON.stringify({
        collection,
      }),
    });
  }
}

export default new MDDBClient();
