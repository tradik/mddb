/**
 * MDDB API Client
 * Simple client for interacting with MDDB HTTP API
 */

import { authManager } from './auth';

const API_BASE = '/v1';

class MDDBClient {
  constructor(baseUrl = API_BASE) {
    this.baseUrl = baseUrl;
  }

  async request(endpoint, options = {}) {
    const url = `${this.baseUrl}${endpoint}`;
    const token = authManager.getToken();

    const config = {
      headers: {
        'Content-Type': 'application/json',
        ...options.headers,
      },
      ...options,
    };

    // Add authentication header if token exists
    if (token) {
      config.headers['Authorization'] = `Bearer ${token}`;
    }

    try {
      const response = await fetch(url, config);

      // Handle 401 Unauthorized - just throw error, let App.jsx handle login
      if (response.status === 401) {
        const error = await response.text();
        throw new Error(`Unauthorized: ${error}`);
      }

      if (!response.ok) {
        const error = await response.text();
        throw new Error(`API Error (${response.status}): ${error}`);
      }

      return await response.json();
    } catch (error) {
      if (error.name === 'AbortError') {
        throw error;
      }
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
   * Get replication/cluster status
   */
  async getReplicationStatus() {
    return this.request('/replication/status', { method: 'GET' });
  }

  /**
   * Search documents in a collection
   */
  async search({ collection, filterMeta = {}, sort = 'addedAt', asc = false, limit = 100, offset = 0 }) {
    const url = `${this.baseUrl}/search`;
    const token = authManager.getToken();
    const headers = { 'Content-Type': 'application/json' };
    if (token) headers['Authorization'] = `Bearer ${token}`;

    const response = await fetch(url, {
      method: 'POST',
      headers,
      body: JSON.stringify({ collection, filterMeta, sort, asc, limit, offset }),
    });
    if (!response.ok) {
      const error = await response.text();
      throw new Error(`API Error (${response.status}): ${error}`);
    }
    const totalCount = parseInt(response.headers.get('X-Total-Count') || '0', 10);
    const documents = await response.json();
    return { documents: Array.isArray(documents) ? documents : [], totalCount };
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
   * Vector/semantic search
   */
  async vectorSearch({ collection, query, topK = 5, threshold = 0.0, filterMeta = {}, includeContent = false, algorithm = 'flat', distanceMetric = 'cosine', signal }) {
    return this.request('/vector-search', {
      method: 'POST',
      body: JSON.stringify({
        collection,
        query,
        topK,
        threshold,
        filterMeta,
        includeContent,
        algorithm,
        distanceMetric,
      }),
      signal,
    });
  }

  /**
   * Re-embed documents in a collection
   */
  async vectorReindex({ collection, force = false }) {
    return this.request('/vector-reindex', {
      method: 'POST',
      body: JSON.stringify({ collection, force }),
    });
  }

  /**
   * Get vector/embedding statistics
   */
  async vectorStats() {
    return this.request('/vector-stats', { method: 'GET' });
  }

  /**
   * 2D PCA projection of a collection's embedding vectors for visualization.
   * @param {object} opts
   * @param {string} opts.collection - collection to project
   * @param {number} [opts.sample=1000] - max points (server caps at 2000)
   * @param {string} [opts.query] - optional text query to embed and overlay
   */
  async vectorProjection({ collection, sample = 1000, query }) {
    return this.request('/vector-projection', {
      method: 'POST',
      body: JSON.stringify({ collection, sample, query }),
    });
  }

  /**
   * Geo radius search: find docs within N meters of (lat, lng).
   * @param {object} opts
   * @param {string} opts.collection
   * @param {number} opts.lat
   * @param {number} opts.lng
   * @param {number} opts.radiusMeters
   * @param {number} [opts.topK=10]
   * @param {'rtree'|'geohash'} [opts.algorithm='rtree']
   * @param {object} [opts.filterMeta]
   * @param {boolean} [opts.includeContent=false]
   */
  async geoSearch({ collection, lat, lng, radiusMeters, topK = 10, algorithm = 'rtree', filterMeta = {}, includeContent = false, signal }) {
    return this.request('/geo-search', {
      method: 'POST',
      body: JSON.stringify({ collection, lat, lng, radiusMeters, topK, algorithm, filterMeta, includeContent }),
      signal,
    });
  }

  /** Geo bbox search. */
  async geoWithin({ collection, minLat, maxLat, minLng, maxLng, filterMeta = {}, includeContent = false, signal }) {
    return this.request('/geo-within', {
      method: 'POST',
      body: JSON.stringify({ collection, minLat, maxLat, minLng, maxLng, filterMeta, includeContent }),
      signal,
    });
  }

  /**
   * GeoJSON Polygon / MultiPolygon containment. Exactly one of `polygon` or
   * `multiPolygon` must be supplied. Coordinates follow GeoJSON order [lng, lat].
   */
  async geoPolygon({ collection, polygon, multiPolygon, filterMeta = {}, includeContent = false, signal }) {
    const body = { collection, filterMeta, includeContent };
    if (polygon) body.polygon = polygon;
    if (multiPolygon) body.multiPolygon = multiPolygon;
    return this.request('/geo-polygon', {
      method: 'POST',
      body: JSON.stringify(body),
      signal,
    });
  }

  /** Geo index statistics. */
  async geoStats() {
    return this.request('/geo-stats', { method: 'GET' });
  }

  /** Encode (lat, lng) → geohash string. */
  async geoEncode({ lat, lng, precision = 12 }) {
    return this.request('/geo-encode', {
      method: 'POST',
      body: JSON.stringify({ lat, lng, precision }),
    });
  }

  /** Decode geohash → (lat, lng) centroid + bbox. */
  async geoDecode({ geohash }) {
    return this.request('/geo-decode', {
      method: 'POST',
      body: JSON.stringify({ geohash }),
    });
  }

  /** Force-rebuild geo index and optionally load postcode CSVs. */
  async geoReindex({ collection = '', loadPostcodes = [] } = {}) {
    return this.request('/geo-reindex', {
      method: 'POST',
      body: JSON.stringify({ collection, loadPostcodes }),
    });
  }

  /**
   * Import document from URL
   */
  async importURL({ collection, url, lang, key, meta = {}, ttl = 0 }) {
    const body = { collection, url, lang };
    if (key) body.key = key;
    if (Object.keys(meta).length > 0) body.meta = meta;
    if (ttl > 0) body.ttl = ttl;
    return this.request('/import-url', {
      method: 'POST',
      body: JSON.stringify(body),
    });
  }

  /**
   * Set TTL on a document
   */
  async setTTL({ collection, key, lang, ttl }) {
    return this.request('/set-ttl', {
      method: 'POST',
      body: JSON.stringify({ collection, key, lang, ttl }),
    });
  }

  /**
   * Get MCP configuration (returns YAML text, not JSON)
   */
  async getMCPConfigText() {
    const url = `${this.baseUrl}/mcp/config`;
    const token = authManager.getToken();
    const headers = {};
    if (token) headers['Authorization'] = `Bearer ${token}`;
    const response = await fetch(url, { headers });
    if (!response.ok) throw new Error('Failed to load MCP config');
    return response.text();
  }

  /**
   * Full-text search
   */
  async ftsSearch({ collection, query, limit = 50, algorithm = 'tfidf', fuzzy = 0, mode = 'auto', distance, disableStem = false, disableSynonyms = false, fieldWeights = null, filterMeta = {}, rangeMeta, lang, boost, highlight, highlightTag, maxHighlights, fragmentSize, signal }) {
    const body = { collection, query, limit, algorithm, fuzzy, disableStem, disableSynonyms };
    if (mode && mode !== 'auto') {
      body.mode = mode;
    }
    if (mode === 'proximity' && distance) {
      body.distance = distance;
    }
    if (filterMeta && Object.keys(filterMeta).length > 0) {
      body.filterMeta = filterMeta;
    }
    if (rangeMeta && rangeMeta.length > 0) {
      body.rangeMeta = rangeMeta;
    }
    if (algorithm === 'bm25f' && fieldWeights) {
      body.fieldWeights = fieldWeights;
    }
    if (lang) {
      body.lang = lang;
    }
    if (boost && Object.keys(boost).length > 0) {
      body.boost = boost;
    }
    if (highlight) {
      body.highlight = true;
      if (highlightTag) body.highlightTag = highlightTag;
      if (maxHighlights) body.maxHighlights = maxHighlights;
      if (fragmentSize) body.fragmentSize = fragmentSize;
    }
    return this.request('/fts', {
      method: 'POST',
      body: JSON.stringify(body),
      signal,
    });
  }

  /**
   * Reindex FTS for a collection (re-applies language-aware stemming)
   */
  async ftsReindex({ collection }) {
    return this.request('/fts-reindex', {
      method: 'POST',
      body: JSON.stringify({ collection }),
    });
  }

  /**
   * List supported FTS languages
   */
  async ftsLanguages() {
    return this.request('/fts-languages', { method: 'GET' });
  }

  /**
   * Prefix autocomplete over the FTS inverted index. Returns up to `topN`
   * terms starting with the given prefix, ranked by document frequency.
   */
  async autocomplete({ collection, q, field = '', topN = 10, signal }) {
    const params = new URLSearchParams({ collection, q, topN: String(topN) });
    if (field) params.set('field', field);
    return this.request(`/autocomplete?${params.toString()}`, { method: 'GET', signal });
  }

  /**
   * Get metadata keys and values for a collection
   */
  async getMetaKeys(collection) {
    return this.request(`/meta-keys?collection=${encodeURIComponent(collection)}`);
  }

  /**
   * Hybrid search (sparse + dense)
   */
  async hybridSearch({ collection, query, topK = 10, algorithm = 'bm25', vectorAlgorithm = 'flat', alpha = 0.5, strategy = 'alpha', rrfK = 60, fuzzy = 0, threshold = 0.0, filterMeta = {}, includeContent = false, distanceMetric = 'cosine', lang, boost, geo, sort, signals, signal }) {
    const body = {
      collection,
      query,
      topK,
      algorithm,
      vectorAlgorithm,
      alpha,
      strategy,
      rrfK,
      fuzzy,
      threshold,
      filterMeta,
      includeContent,
      distanceMetric,
    };
    if (lang) {
      body.lang = lang;
    }
    // SRCH-002: sent only when a weight is set, so a "weighted" request with
    // no signals behaves exactly like "alpha" rather than carrying an empty
    // object the server has to interpret.
    if (signals && Object.values(signals).some((v) => Number(v) > 0)) {
      body.signals = signals;
    }
    if (boost && Object.keys(boost).length > 0) {
      body.boost = boost;
    }
    if (geo) {
      body.geo = geo;
    }
    if (sort) {
      body.sort = sort;
    }
    return this.request('/hybrid-search', {
      method: 'POST',
      body: JSON.stringify(body),
      signal,
    });
  }

  /**
   * Synonyms CRUD
   */
  async listSynonyms(collection) {
    return this.request(`/synonyms?collection=${encodeURIComponent(collection)}`, { method: 'GET' });
  }

  async setSynonym({ collection, term, synonyms }) {
    return this.request('/synonyms', {
      method: 'POST',
      body: JSON.stringify({ collection, term, synonyms }),
    });
  }

  async deleteSynonym({ collection, term }) {
    return this.request('/synonyms', {
      method: 'DELETE',
      body: JSON.stringify({ collection, term }),
    });
  }

  /**
   * Stop Words CRUD
   */
  async listStopWords(collection, lang) {
    let url = `/stopwords?collection=${encodeURIComponent(collection)}`;
    if (lang) url += `&lang=${encodeURIComponent(lang)}`;
    return this.request(url, { method: 'GET' });
  }

  async addStopWords({ collection, words }) {
    return this.request('/stopwords', {
      method: 'POST',
      body: JSON.stringify({ collection, words }),
    });
  }

  async deleteStopWord({ collection, words }) {
    return this.request('/stopwords', {
      method: 'DELETE',
      body: JSON.stringify({ collection, words }),
    });
  }

  /**
   * Register a webhook
   */
  async registerWebhook({ url, events, collection }) {
    const body = { url, events };
    if (collection) body.collection = collection;
    return this.request('/webhooks', {
      method: 'POST',
      body: JSON.stringify(body),
    });
  }

  /**
   * List all webhooks
   */
  async listWebhooks() {
    return this.request('/webhooks', { method: 'GET' });
  }

  /**
   * Delete a webhook
   */
  async deleteWebhook(id) {
    return this.request('/webhooks/delete', {
      method: 'POST',
      body: JSON.stringify({ id }),
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

  // ---- System & Configuration Methods ----

  /**
   * Get system information
   */
  async getSystemInfo() {
    return this.request('/system/info', { method: 'GET' });
  }

  /**
   * Get server configuration
   */
  async getConfig() {
    return this.request('/config', { method: 'GET' });
  }

  /**
   * Get MCP configuration YAML
   */
  async getMCPConfig() {
    return this.request('/mcp/config', { method: 'GET' });
  }

  async listMCPAPIKeys() {
    return this.request('/mcp/keys', { method: 'GET' });
  }

  async createMCPAPIKey(name, expiresAt = 0) {
    return this.request('/mcp/keys', { method: 'POST', body: JSON.stringify({ name, expiresAt }) });
  }

  async deleteMCPAPIKey(key) {
    return this.request('/mcp/keys', { method: 'DELETE', body: JSON.stringify({ key }) });
  }

  async disableMCPAPIKey(key) {
    return this.request('/mcp/keys/disable', { method: 'POST', body: JSON.stringify({ key }) });
  }

  /**
   * Get all API endpoints
   */
  async getEndpoints() {
    return this.request('/endpoints', { method: 'GET' });
  }

  /**
   * Get vector search statistics
   */
  async getVectorStats() {
    return this.request('/vector-stats', { method: 'GET' });
  }

  /**
   * Reindex vectors for a collection
   */
  async reindexVectors(collection) {
    return this.request('/vector-reindex', {
      method: 'POST',
      body: JSON.stringify({ collection }),
    });
  }

  // ---- User Management Methods ----

  /**
   * List all users (admin only)
   */
  async listUsers() {
    return this.request('/auth/users', { method: 'GET' });
  }

  /**
   * Create a new user (admin only)
   */
  async createUser({ username, password, admin = false }) {
    return this.request('/auth/register', {
      method: 'POST',
      body: JSON.stringify({ username, password, admin }),
    });
  }

  /**
   * Delete a user (admin only)
   */
  async deleteUser(username) {
    return this.request(`/auth/users/${username}`, { method: 'DELETE' });
  }

  /**
   * Set user permission
   */
  async setUserPermission({ username, collection, read, write, admin }) {
    return this.request('/auth/permissions', {
      method: 'POST',
      body: JSON.stringify({ username, collection, read, write, admin }),
    });
  }

  /**
   * Get user permissions
   */
  async getUserPermissions(username) {
    return this.request(`/auth/permissions?username=${username}`, {
      method: 'GET',
    });
  }

  // ---- Group Management Methods ----

  /**
   * Create a new group
   */
  async createGroup({ name, description, members = [] }) {
    return this.request('/auth/groups', {
      method: 'POST',
      body: JSON.stringify({ name, description, members }),
    });
  }

  /**
   * List all groups
   */
  async listGroups() {
    return this.request('/auth/groups', { method: 'GET' });
  }

  /**
   * Get group details
   */
  async getGroup(name) {
    return this.request(`/auth/groups/${name}`, { method: 'GET' });
  }

  /**
   * Update group
   */
  async updateGroup(name, { description, members }) {
    return this.request(`/auth/groups/${name}`, {
      method: 'PUT',
      body: JSON.stringify({ description, members }),
    });
  }

  /**
   * Delete group
   */
  async deleteGroup(name) {
    return this.request(`/auth/groups/${name}`, { method: 'DELETE' });
  }

  /**
   * Set group permission
   */
  async setGroupPermission({ groupName, collection, read, write, admin }) {
    return this.request('/auth/group-permissions', {
      method: 'POST',
      body: JSON.stringify({ groupName, collection, read, write, admin }),
    });
  }

  /**
   * Get group permissions
   */
  async getGroupPermissions(groupName) {
    return this.request(`/auth/group-permissions?group=${encodeURIComponent(groupName)}`, {
      method: 'GET',
    });
  }

  /**
   * Embedding Configurations
   */

  /**
   * List all embedding configurations
   */
  async listEmbeddingConfigs() {
    return this.request('/embedding-configs', { method: 'GET' });
  }

  /**
   * Get embedding configuration by ID
   */
  async getEmbeddingConfig(id) {
    return this.request(`/embedding-configs/${id}`, { method: 'GET' });
  }

  /**
   * Create embedding configuration
   */
  async createEmbeddingConfig({ id, name, provider, model, dimensions, apiKey, apiUrl, isDefault }) {
    return this.request('/embedding-configs', {
      method: 'POST',
      body: JSON.stringify({ id, name, provider, model, dimensions, apiKey, apiUrl, isDefault }),
    });
  }

  /**
   * Update embedding configuration
   */
  async updateEmbeddingConfig(id, { name, provider, model, dimensions, apiKey, apiUrl, isDefault }) {
    return this.request(`/embedding-configs/${id}`, {
      method: 'PUT',
      body: JSON.stringify({ name, provider, model, dimensions, apiKey, apiUrl, isDefault }),
    });
  }

  /**
   * Delete embedding configuration
   */
  async deleteEmbeddingConfig(id) {
    return this.request(`/embedding-configs/${id}`, { method: 'DELETE' });
  }

  /**
   * Set default embedding configuration
   */
  async setDefaultEmbeddingConfig(id) {
    return this.request('/embedding-configs/set-default', {
      method: 'POST',
      body: JSON.stringify({ id }),
    });
  }

  /**
   * Partial document update (meta and/or content independently)
   */
  async updateDocument({ collection, key, lang, meta, contentMd, ttl }) {
    const body = { collection, key, lang };
    if (meta !== undefined) body.meta = meta;
    if (contentMd !== undefined) body.contentMd = contentMd;
    if (ttl !== undefined) body.ttl = ttl;
    return this.request('/update', { method: 'PATCH', body: JSON.stringify(body) });
  }

  /**
   * Get document metadata only (without content)
   */
  async getDocumentMeta({ collection, key, lang }) {
    return this.request(`/doc-meta?collection=${encodeURIComponent(collection)}&key=${encodeURIComponent(key)}&lang=${encodeURIComponent(lang || 'en')}`);
  }

  /**
   * Zero-shot document classification
   */
  async classify({ collection, key, lang, text, labels, topK, multi, threshold }) {
    return this.request('/classify', {
      method: 'POST',
      body: JSON.stringify({ collection, key, lang, text, labels, topK, multi, threshold }),
    });
  }

  // ---- Automation Methods ----

  /**
   * List automation rules, optionally filtered by type
   */
  async listAutomation(type) {
    const params = type ? `?type=${type}` : '';
    return this.request(`/automation${params}`, { method: 'GET' });
  }

  /**
   * Create an automation rule
   */
  async createAutomation(rule) {
    return this.request('/automation', {
      method: 'POST',
      body: JSON.stringify(rule),
    });
  }

  /**
   * Get a single automation rule by ID
   */
  async getAutomation(id) {
    return this.request(`/automation/${id}`, { method: 'GET' });
  }

  /**
   * Update an automation rule
   */
  async updateAutomation(id, rule) {
    return this.request(`/automation/${id}`, {
      method: 'PUT',
      body: JSON.stringify(rule),
    });
  }

  /**
   * Delete an automation rule
   */
  async deleteAutomation(id) {
    return this.request(`/automation/${id}`, { method: 'DELETE' });
  }

  /**
   * Test an automation rule (trigger)
   */
  async testAutomation(id) {
    return this.request(`/automation/${id}/test`, { method: 'POST' });
  }

  /**
   * List automation logs
   */
  async listAutomationLogs({ limit = 50, cursor = '', ruleId = '', status = '' } = {}) {
    const params = new URLSearchParams();
    if (limit) params.set('limit', limit);
    if (cursor) params.set('cursor', cursor);
    if (ruleId) params.set('ruleId', ruleId);
    if (status) params.set('status', status);
    return this.request(`/automation-logs?${params.toString()}`, { method: 'GET' });
  }

  // ---- Collection Config Methods ----

  /**
   * Get collection configuration
   */
  // SRCH-010: ask the server how a collection should be searched, instead of
  // making the operator choose between eight vector algorithms by name.
  async searchAdvisor(collection, { apply = false } = {}) {
    const params = new URLSearchParams({ collection });
    if (apply) params.set('apply', 'true');
    return this.request(`/search-advisor?${params.toString()}`);
  }

  // CODE-005: what depends on this document, and what it depends on.
  async codeGraph({ collection, key, direction = 'both', depth = 1, maxDegree = 100, lines = false }) {
    const params = new URLSearchParams({ collection, key, direction, depth: String(depth), maxDegree: String(maxDegree) });
    if (lines) params.set('lines', 'true');
    return this.request(`/code-graph?${params.toString()}`);
  }

  async getCollectionConfig(collection) {
    return this.request(`/collection-config?collection=${encodeURIComponent(collection)}`);
  }

  /**
   * Set collection configuration
   */
  async setCollectionConfig(data) {
    return this.request('/collection-config', {
      method: 'PUT',
      body: JSON.stringify(data),
    });
  }

  /**
   * Delete collection configuration
   */
  async deleteCollectionConfig(collection) {
    return this.request(`/collection-config?collection=${encodeURIComponent(collection)}`, {
      method: 'DELETE',
    });
  }

  /**
   * List all collection configurations
   */
  async listCollectionConfigs() {
    return this.request('/collection-configs');
  }

  // ---- Curation Rules (v2.9.14+) ----

  /** List curation rules — scope by collection when given, otherwise return all. */
  async listCurationRules(collection) {
    const q = collection ? `?collection=${encodeURIComponent(collection)}` : '';
    return this.request(`/curation${q}`);
  }

  /** Get a single curation rule by id. */
  async getCurationRule(id) {
    return this.request(`/curation?id=${encodeURIComponent(id)}`);
  }

  /** Create a new curation rule. Server assigns the id. */
  async createCurationRule(rule) {
    return this.request('/curation', {
      method: 'POST',
      body: JSON.stringify(rule),
    });
  }

  /** Replace an existing curation rule. `rule.id` is required. */
  async updateCurationRule(rule) {
    return this.request('/curation', {
      method: 'PUT',
      body: JSON.stringify(rule),
    });
  }

  /** Remove a curation rule by id. */
  async deleteCurationRule(id) {
    return this.request(`/curation?id=${encodeURIComponent(id)}`, {
      method: 'DELETE',
    });
  }

  // ---- Cross-Search Methods ----

  /**
   * Cross-collection semantic search
   */
  async crossSearch(params, { signal } = {}) {
    return this.request('/cross-search', {
      method: 'POST',
      body: JSON.stringify(params),
      signal,
    });
  }

  /**
   * Upload files via multipart/form-data (supports md/txt/html/pdf/docx)
   */
  async uploadFile({ files, collection, lang, key, signal }) {
    const url = `${this.baseUrl}/upload`;
    const token = authManager.getToken();

    const formData = new FormData();
    if (Array.isArray(files)) {
      files.forEach((f) => formData.append('files[]', f));
    } else {
      formData.append('file', files);
    }
    if (collection) formData.append('collection', collection);
    if (lang) formData.append('lang', lang);
    if (key) formData.append('key', key);

    const headers = {};
    if (token) headers['Authorization'] = `Bearer ${token}`;

    const response = await fetch(url, {
      method: 'POST',
      headers,
      body: formData,
      signal,
    });

    if (response.status === 401) {
      const error = await response.text();
      throw new Error(`Unauthorized: ${error}`);
    }
    if (!response.ok) {
      const error = await response.text();
      throw new Error(`Upload Error (${response.status}): ${error}`);
    }
    return response.json();
  }

  async getRevisions({ collection, key, lang }) {
    return this.request('/revisions', {
      method: 'POST',
      body: JSON.stringify({ collection, key, lang }),
    });
  }

  async restoreRevision({ collection, key, lang, timestamp }) {
    return this.request('/revisions/restore', {
      method: 'POST',
      body: JSON.stringify({ collection, key, lang, timestamp }),
    });
  }

  /**
   * Temporal event tracking
   */
  async temporalQuery({ collection, key, lang, eventType, from, to, limit }) {
    return this.request('/temporal/query', {
      method: 'POST',
      body: JSON.stringify({ collection, key, lang, eventType, from, to, limit }),
    });
  }

  async temporalHot({ collection, topN, since }) {
    return this.request('/temporal/hot', {
      method: 'POST',
      body: JSON.stringify({ collection, topN, since }),
    });
  }

  async temporalHistogram({ collection, eventType, interval, from, to }) {
    return this.request('/temporal/histogram', {
      method: 'POST',
      body: JSON.stringify({ collection, eventType, interval, from, to }),
    });
  }

  /**
   * Spell correction
   */
  async spellSuggest({ collection, text, lang, maxSuggestions }) {
    return this.request('/spell-suggest', {
      method: 'POST',
      body: JSON.stringify({ collection, text, lang, maxSuggestions }),
    });
  }

  async spellCleanup({ collection, text, lang }) {
    return this.request('/spell-cleanup', {
      method: 'POST',
      body: JSON.stringify({ collection, text, lang }),
    });
  }

  // ---- Audit / Compliance / Health (ISO 27001 / SOC 2) ----

  /**
   * Query the audit log. Accepts optional filters: actor, action, result,
   * from / to (RFC3339), fromNanos / toNanos, limit.
   */
  async listAuditEvents(filters = {}) {
    const params = new URLSearchParams();
    for (const [k, v] of Object.entries(filters)) {
      if (v !== undefined && v !== null && v !== '') params.append(k, v);
    }
    const qs = params.toString();
    return this.request(`/audit${qs ? '?' + qs : ''}`, { method: 'GET' });
  }

  /**
   * Get the production-guard compliance status for the compliance banner
   * and the Security dashboard. Returns {production, compliant, missing[]}.
   */
  async getComplianceStatus() {
    return this.request('/compliance-status', { method: 'GET' });
  }

  /** Basic health probe. */
  async getHealth() {
    return this.request('/health', { method: 'GET' });
  }

  /** Audit log exporter health (per-sink delivery counters). */
  async listAuditExporters() {
    return this.request('/audit/exporters', { method: 'GET' });
  }

  /**
   * At-rest encryption posture: primary keyID, configured previous
   * keyIDs, per-collection counts of documents sealed under primary
   * vs legacy keys.
   */
  async getEncryptionStatus() {
    return this.request('/encryption/status', { method: 'GET' });
  }

  /** Start a re-encryption job; optional collection scope. */
  async rotateEncryption({ collection } = {}) {
    return this.request('/encryption/rotate', {
      method: 'POST',
      body: JSON.stringify({ collection: collection || '' }),
    });
  }

  /** List rotation jobs (newest first). */
  async listRotationJobs() {
    return this.request('/encryption/jobs', { method: 'GET' });
  }

  /** Single rotation job status. */
  async getRotationJob(id) {
    return this.request(`/encryption/jobs/${encodeURIComponent(id)}`, { method: 'GET' });
  }

  async spellDictionary(method, { collection, lang, words, frequency }) {
    if (method === 'GET') {
      const params = new URLSearchParams({ lang });
      if (collection) params.set('collection', collection);
      return this.request(`/spell-dictionary?${params}`, { method: 'GET' });
    }
    return this.request('/spell-dictionary', {
      method,
      body: JSON.stringify({ collection, lang, words, frequency }),
    });
  }
}

export default new MDDBClient();
