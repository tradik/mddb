import { useState, useEffect } from 'react';
import { X, Settings2, Plus, Trash2 } from 'lucide-react';
import mddbClient from '../lib/mddb-client';

const COLLECTION_TYPES = [
  { value: 'default', label: 'Default', icon: '\uD83D\uDCC1' },
  { value: 'website', label: 'Website', icon: '\uD83C\uDF10' },
  { value: 'images', label: 'Images', icon: '\uD83D\uDDBC\uFE0F' },
  { value: 'audio', label: 'Audio', icon: '\uD83C\uDFB5' },
  { value: 'documents', label: 'Documents', icon: '\uD83D\uDCC4' },
];

const QUANTIZATION_LEVELS = [
  { value: 'float32', label: 'float32 (no compression)', description: 'Full precision, highest accuracy' },
  { value: 'int8', label: 'int8 (4x compression)', description: '~1% recall drop, recommended for most use cases' },
  { value: 'int4', label: 'int4 (8x compression)', description: '~2-3% recall drop, best for large collections' },
];

const STORAGE_BACKENDS = [
  { value: 'boltdb', label: 'BoltDB (default)', icon: '\uD83D\uDDC4\uFE0F', description: 'Embedded key-value store, persisted to disk' },
  { value: 'memory', label: 'In-Memory (ephemeral)', icon: '\u26A1', description: 'Fast but data is lost on restart' },
  { value: 's3', label: 'S3 / MinIO', icon: '\u2601\uFE0F', description: 'S3-compatible object storage' },
];

export default function CollectionConfigModal({ collection, onClose, onSave }) {
  const [type, setType] = useState('default');
  const [description, setDescription] = useState('');
  const [icon, setIcon] = useState('');
  const [color, setColor] = useState('#3b82f6');
  const [customMeta, setCustomMeta] = useState([]);
  const [quantization, setQuantization] = useState('float32');
  const [storageBackend, setStorageBackend] = useState('boltdb');
  const [storageConfig, setStorageConfig] = useState({ endpoint: '', bucket: '', region: '', accessKey: '', secretKey: '', prefix: '', useTLS: false });
  const [trackAccess, setTrackAccess] = useState(false);
  const [trackHot, setTrackHot] = useState(false);
  const [spellCorrect, setSpellCorrect] = useState(false);
  const [spellLang, setSpellLang] = useState('');
  const [maxRevisions, setMaxRevisions] = useState(0);
  const [encrypted, setEncrypted] = useState(false);
  // Retrieval profile (RAG-001). Empty values mean "not configured" — the
  // caller's request parameter or MDDB's own default applies.
  const [retrievalSearchType, setRetrievalSearchType] = useState('');
  const [retrievalTopK, setRetrievalTopK] = useState('');
  const [retrievalMode, setRetrievalMode] = useState('');
  const [hybridStrategy, setHybridStrategy] = useState('');
  const [hybridAlpha, setHybridAlpha] = useState('');
  const [contextTokenBudget, setContextTokenBudget] = useState('');
  const [responsePrompt, setResponsePrompt] = useState('');
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState(null);

  useEffect(() => {
    loadConfig();
  }, [collection]);

  const loadConfig = async () => {
    setLoading(true);
    setError(null);
    try {
      const data = await mddbClient.getCollectionConfig(collection);
      if (data.configured && data.config) {
        const cfg = data.config;
        setType(cfg.type || 'default');
        setDescription(cfg.description || '');
        setIcon(cfg.icon || '');
        setColor(cfg.color || '#3b82f6');
        const metaEntries = cfg.customMeta
          ? Object.entries(cfg.customMeta).map(([k, v]) => ({ key: k, value: v }))
          : [];
        setCustomMeta(metaEntries);
        setQuantization(cfg.quantization || 'float32');
        setStorageBackend(cfg.storageBackend || 'boltdb');
        if (cfg.storageConfig) {
          setStorageConfig({ endpoint: '', bucket: '', region: '', accessKey: '', secretKey: '', prefix: '', useTLS: false, ...cfg.storageConfig });
        }
        setTrackAccess(cfg.trackAccess || false);
        setTrackHot(cfg.trackHot || false);
        setSpellCorrect(cfg.spellCorrect || false);
        setSpellLang(cfg.spellLang || '');
        setMaxRevisions(cfg.maxRevisions || 0);
        setEncrypted(cfg.encrypted === true);
        const r = cfg.retrieval || {};
        setRetrievalSearchType(r.defaultSearchType || '');
        setRetrievalTopK(r.topK || '');
        setRetrievalMode(r.retrievalMode || '');
        setHybridStrategy(r.hybridStrategy || '');
        // hybridAlpha 0 is a real weight (pure keyword), so it is only
        // "unset" when the server says it was never configured.
        setHybridAlpha(r.hybridAlphaSet ? String(r.hybridAlpha ?? 0) : '');
        setContextTokenBudget(r.contextTokenBudget || '');
        setResponsePrompt(cfg.responsePrompt || '');
      }
    } catch (err) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  };

  const handleSave = async () => {
    setSaving(true);
    setError(null);
    try {
      const metaObj = {};
      customMeta.forEach(({ key, value }) => {
        if (key.trim()) {
          metaObj[key.trim()] = value;
        }
      });
      const payload = {
        collection,
        type,
        description,
        icon,
        color,
        customMeta: Object.keys(metaObj).length > 0 ? metaObj : undefined,
        quantization: quantization !== 'float32' ? quantization : undefined,
        storageBackend: storageBackend !== 'boltdb' ? storageBackend : undefined,
        trackAccess: trackAccess || undefined,
        trackHot: trackHot || undefined,
        spellCorrect: spellCorrect || undefined,
        spellLang: spellLang || undefined,
        maxRevisions: Number(maxRevisions) > 0 ? Number(maxRevisions) : undefined,
        encrypted: encrypted || undefined,
      };

      // Send the retrieval block only when something in it is set: an empty
      // profile and no profile must mean the same thing to the server.
      const retrieval = {};
      if (retrievalSearchType) retrieval.defaultSearchType = retrievalSearchType;
      if (Number(retrievalTopK) > 0) retrieval.topK = Number(retrievalTopK);
      if (retrievalMode) retrieval.retrievalMode = retrievalMode;
      if (hybridStrategy) retrieval.hybridStrategy = hybridStrategy;
      if (hybridAlpha !== '') {
        retrieval.hybridAlpha = Number(hybridAlpha);
        retrieval.hybridAlphaSet = true;
      }
      if (Number(contextTokenBudget) > 0) retrieval.contextTokenBudget = Number(contextTokenBudget);
      if (Object.keys(retrieval).length > 0) payload.retrieval = retrieval;
      if (responsePrompt.trim()) payload.responsePrompt = responsePrompt.trim();
      if (storageBackend === 's3') {
        payload.storageConfig = storageConfig;
      }
      await mddbClient.setCollectionConfig(payload);
      onSave();
      onClose();
    } catch (err) {
      setError(err.message);
    } finally {
      setSaving(false);
    }
  };

  const addMetaEntry = () => {
    setCustomMeta([...customMeta, { key: '', value: '' }]);
  };

  const removeMetaEntry = (index) => {
    setCustomMeta(customMeta.filter((_, i) => i !== index));
  };

  const updateMetaEntry = (index, field, value) => {
    const updated = [...customMeta];
    updated[index] = { ...updated[index], [field]: value };
    setCustomMeta(updated);
  };

  return (
    <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50 p-4">
      <div className="bg-white rounded-lg shadow-xl w-full max-w-md max-h-[90vh] overflow-y-auto">
        <div className="p-6">
          <div className="flex items-center justify-between mb-4">
            <div className="flex items-center gap-2">
              <Settings2 className="w-6 h-6 text-blue-600" />
              <h2 className="text-xl font-bold text-gray-900">Collection Settings</h2>
            </div>
            <button onClick={onClose} className="text-gray-400 hover:text-gray-600">
              <X className="w-5 h-5" />
            </button>
          </div>

          <p className="text-sm text-gray-500 mb-4">
            Configure <span className="font-medium text-gray-700">{collection}</span>
          </p>

          {loading ? (
            <div className="flex items-center justify-center py-8">
              <div className="animate-spin rounded-full h-6 w-6 border-b-2 border-blue-600"></div>
            </div>
          ) : (
            <div className="space-y-4">
              {/* Type */}
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">Type</label>
                <select
                  value={type}
                  onChange={(e) => setType(e.target.value)}
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm"
                >
                  {COLLECTION_TYPES.map((t) => (
                    <option key={t.value} value={t.value}>
                      {t.icon} {t.label}
                    </option>
                  ))}
                </select>
              </div>

              {/* Description */}
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">Description</label>
                <textarea
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  placeholder="Brief description of this collection..."
                  rows={2}
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm resize-none"
                />
              </div>

              {/* Icon */}
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">Icon (emoji)</label>
                <div className="flex items-center gap-2">
                  <input
                    type="text"
                    value={icon}
                    onChange={(e) => setIcon(e.target.value)}
                    placeholder="e.g. \uD83D\uDCDA"
                    className="flex-1 px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm"
                    maxLength={4}
                  />
                  {icon && (
                    <span className="text-2xl">{icon}</span>
                  )}
                </div>
              </div>

              {/* Color */}
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">Color</label>
                <div className="flex items-center gap-3">
                  <input
                    type="color"
                    value={color}
                    onChange={(e) => setColor(e.target.value)}
                    className="w-10 h-10 rounded border border-gray-300 cursor-pointer"
                  />
                  <input
                    type="text"
                    value={color}
                    onChange={(e) => setColor(e.target.value)}
                    placeholder="#3b82f6"
                    className="flex-1 px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm font-mono"
                  />
                  <div
                    className="w-8 h-8 rounded-full border border-gray-200"
                    style={{ backgroundColor: color }}
                  />
                </div>
              </div>

              {/* Storage Backend */}
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">Storage Backend</label>
                <select
                  value={storageBackend}
                  onChange={(e) => setStorageBackend(e.target.value)}
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm"
                >
                  {STORAGE_BACKENDS.map((b) => (
                    <option key={b.value} value={b.value}>
                      {b.icon} {b.label}
                    </option>
                  ))}
                </select>
                <p className="text-xs text-gray-400 mt-1">
                  {STORAGE_BACKENDS.find(b => b.value === storageBackend)?.description}
                </p>
              </div>

              {/* S3 Configuration */}
              {storageBackend === 's3' && (
                <div className="space-y-3 p-3 bg-gray-50 rounded-lg border border-gray-200">
                  <p className="text-xs font-medium text-gray-600 uppercase tracking-wide">S3 / MinIO Settings</p>
                  <div className="grid grid-cols-2 gap-2">
                    <div>
                      <label className="block text-xs text-gray-500 mb-0.5">Endpoint *</label>
                      <input
                        type="text"
                        value={storageConfig.endpoint}
                        onChange={(e) => setStorageConfig({ ...storageConfig, endpoint: e.target.value })}
                        placeholder="s3.amazonaws.com"
                        className="w-full px-2 py-1.5 border border-gray-300 rounded text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                      />
                    </div>
                    <div>
                      <label className="block text-xs text-gray-500 mb-0.5">Bucket *</label>
                      <input
                        type="text"
                        value={storageConfig.bucket}
                        onChange={(e) => setStorageConfig({ ...storageConfig, bucket: e.target.value })}
                        placeholder="my-mddb-bucket"
                        className="w-full px-2 py-1.5 border border-gray-300 rounded text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                      />
                    </div>
                  </div>
                  <div className="grid grid-cols-2 gap-2">
                    <div>
                      <label className="block text-xs text-gray-500 mb-0.5">Region</label>
                      <input
                        type="text"
                        value={storageConfig.region}
                        onChange={(e) => setStorageConfig({ ...storageConfig, region: e.target.value })}
                        placeholder="us-east-1"
                        className="w-full px-2 py-1.5 border border-gray-300 rounded text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                      />
                    </div>
                    <div>
                      <label className="block text-xs text-gray-500 mb-0.5">Prefix</label>
                      <input
                        type="text"
                        value={storageConfig.prefix}
                        onChange={(e) => setStorageConfig({ ...storageConfig, prefix: e.target.value })}
                        placeholder="mddb/"
                        className="w-full px-2 py-1.5 border border-gray-300 rounded text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                      />
                    </div>
                  </div>
                  <div className="grid grid-cols-2 gap-2">
                    <div>
                      <label className="block text-xs text-gray-500 mb-0.5">Access Key</label>
                      <input
                        type="text"
                        value={storageConfig.accessKey}
                        onChange={(e) => setStorageConfig({ ...storageConfig, accessKey: e.target.value })}
                        placeholder="AKIAIOSFODNN7EXAMPLE"
                        className="w-full px-2 py-1.5 border border-gray-300 rounded text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                      />
                    </div>
                    <div>
                      <label className="block text-xs text-gray-500 mb-0.5">Secret Key</label>
                      <input
                        type="password"
                        value={storageConfig.secretKey}
                        onChange={(e) => setStorageConfig({ ...storageConfig, secretKey: e.target.value })}
                        placeholder="wJalrXUtnFEMI/K7MDENG..."
                        className="w-full px-2 py-1.5 border border-gray-300 rounded text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                      />
                    </div>
                  </div>
                  <label className="flex items-center gap-2 text-sm text-gray-600 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={storageConfig.useTLS}
                      onChange={(e) => setStorageConfig({ ...storageConfig, useTLS: e.target.checked })}
                      className="rounded border-gray-300 text-blue-600 focus:ring-blue-500"
                    />
                    Use TLS (HTTPS)
                  </label>
                </div>
              )}

              {/* In-Memory Warning */}
              {storageBackend === 'memory' && (
                <div className="p-3 bg-amber-50 border border-amber-200 rounded-lg">
                  <p className="text-sm text-amber-800">Data in this collection will be lost when the server restarts. Use for temporary/scratch data only.</p>
                </div>
              )}

              {/* Vector Quantization */}
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">Vector Quantization</label>
                <select
                  value={quantization}
                  onChange={(e) => setQuantization(e.target.value)}
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm"
                >
                  {QUANTIZATION_LEVELS.map((q) => (
                    <option key={q.value} value={q.value}>
                      {q.label}
                    </option>
                  ))}
                </select>
                <p className="text-xs text-gray-400 mt-1">
                  {QUANTIZATION_LEVELS.find(q => q.value === quantization)?.description}
                </p>
              </div>

              {/* Quantization Warning */}
              {quantization !== 'float32' && (
                <div className="p-3 bg-blue-50 border border-blue-200 rounded-lg">
                  <p className="text-sm text-blue-800">After changing quantization, run <span className="font-mono text-xs bg-blue-100 px-1 rounded">vector-reindex --force</span> to re-encode existing vectors.</p>
                </div>
              )}

              {/* Temporal Tracking */}
              <div className="space-y-2">
                <label className="block text-sm font-medium text-gray-700">Temporal Tracking</label>
                <label className="flex items-center gap-2 text-sm text-gray-600 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={trackAccess}
                    onChange={(e) => setTrackAccess(e.target.checked)}
                    className="rounded border-gray-300 text-blue-600 focus:ring-blue-500"
                  />
                  Track document access events
                </label>
                <label className="flex items-center gap-2 text-sm text-gray-600 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={trackHot}
                    onChange={(e) => setTrackHot(e.target.checked)}
                    className="rounded border-gray-300 text-blue-600 focus:ring-blue-500"
                  />
                  Maintain hot-docs leaderboard
                </label>
              </div>

              {/* Spell Correction */}
              <div className="space-y-2">
                <label className="block text-sm font-medium text-gray-700">Spell Correction</label>
                <label className="flex items-center gap-2 text-sm text-gray-600 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={spellCorrect}
                    onChange={(e) => setSpellCorrect(e.target.checked)}
                    className="rounded border-gray-300 text-blue-600 focus:ring-blue-500"
                  />
                  Auto-correct FTS queries
                </label>
                {spellCorrect && (
                  <div>
                    <label className="block text-xs text-gray-500 mb-0.5">Override spell language (optional)</label>
                    <input
                      type="text"
                      value={spellLang}
                      onChange={(e) => setSpellLang(e.target.value)}
                      placeholder="e.g. en, de, fr (leave empty to use query lang)"
                      className="w-full px-2 py-1.5 border border-gray-300 rounded text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                    />
                  </div>
                )}
              </div>

              {/* Revision Retention (v2.9.15+) */}
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">Revision History Retention</label>
                <div className="flex items-center gap-2">
                  <input
                    type="number"
                    min="0"
                    value={maxRevisions}
                    onChange={(e) => setMaxRevisions(e.target.value)}
                    className="w-32 px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm"
                  />
                  <span className="text-xs text-gray-500">
                    {Number(maxRevisions) > 0
                      ? `Keep last ${maxRevisions} revisions per document`
                      : 'Unlimited (default) — every update retained'}
                  </span>
                </div>
                <p className="text-xs text-gray-400 mt-1">
                  Older revisions are trimmed synchronously on every write so history can&apos;t grow unbounded on high-churn collections.
                </p>
              </div>

              {/* Retrieval profile (RAG-001, v2.12.0+) */}
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">Retrieval Defaults</label>
                <p className="text-xs text-gray-500 mb-2">
                  Search settings stored with the collection instead of repeated by every client.
                  A request that passes its own value always wins; leave a field empty to keep MDDB&apos;s default.
                </p>

                <div className="grid grid-cols-2 gap-3">
                  <div>
                    <label className="block text-xs text-gray-500 mb-0.5">Default search type</label>
                    <select
                      value={retrievalSearchType}
                      onChange={(e) => setRetrievalSearchType(e.target.value)}
                      className="w-full px-2 py-1.5 border border-gray-300 rounded text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                    >
                      <option value="">MDDB default</option>
                      <option value="fts">Full-text</option>
                      <option value="vector">Vector</option>
                      <option value="hybrid">Hybrid</option>
                    </select>
                  </div>

                  <div>
                    <label className="block text-xs text-gray-500 mb-0.5">Results per search (topK)</label>
                    <input
                      type="number"
                      min="0"
                      max="1000"
                      value={retrievalTopK}
                      onChange={(e) => setRetrievalTopK(e.target.value)}
                      placeholder="per-endpoint default"
                      className="w-full px-2 py-1.5 border border-gray-300 rounded text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                    />
                  </div>

                  <div>
                    <label className="block text-xs text-gray-500 mb-0.5">Granularity</label>
                    <select
                      value={retrievalMode}
                      onChange={(e) => setRetrievalMode(e.target.value)}
                      className="w-full px-2 py-1.5 border border-gray-300 rounded text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                    >
                      <option value="">MDDB default (whole document)</option>
                      <option value="parent">Whole document</option>
                      <option value="chunk">Matching passage</option>
                      <option value="window">Passage with neighbours</option>
                    </select>
                  </div>

                  <div>
                    <label className="block text-xs text-gray-500 mb-0.5">Hybrid fusion</label>
                    <select
                      value={hybridStrategy}
                      onChange={(e) => setHybridStrategy(e.target.value)}
                      className="w-full px-2 py-1.5 border border-gray-300 rounded text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                    >
                      <option value="">MDDB default (alpha)</option>
                      <option value="alpha">Alpha blend</option>
                      <option value="rrf">Reciprocal rank fusion</option>
                    </select>
                  </div>

                  {hybridStrategy !== 'rrf' && (
                    <div>
                      <label className="block text-xs text-gray-500 mb-0.5">
                        Keyword ↔ semantic balance {hybridAlpha !== '' && `(${hybridAlpha})`}
                      </label>
                      <input
                        type="number"
                        min="0"
                        max="1"
                        step="0.05"
                        value={hybridAlpha}
                        onChange={(e) => setHybridAlpha(e.target.value)}
                        placeholder="0.5"
                        className="w-full px-2 py-1.5 border border-gray-300 rounded text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                      />
                      <p className="text-xs text-gray-400 mt-0.5">0 = keyword only, 1 = semantic only</p>
                    </div>
                  )}

                  <div>
                    <label className="block text-xs text-gray-500 mb-0.5">Context budget (tokens)</label>
                    <input
                      type="number"
                      min="0"
                      value={contextTokenBudget}
                      onChange={(e) => setContextTokenBudget(e.target.value)}
                      placeholder="no cap"
                      className="w-full px-2 py-1.5 border border-gray-300 rounded text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                    />
                    <p className="text-xs text-gray-400 mt-0.5">
                      Drops results from the tail once the total would exceed this. Approximate, not tokenised.
                    </p>
                  </div>
                </div>
              </div>

              {/* Response prompt (RAG-002, v2.12.0+) */}
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">Answer Formatting</label>
                <p className="text-xs text-gray-500 mb-2">
                  How answers drawn from this collection should be shaped — numbered steps for runbooks,
                  code blocks for API docs. Applied automatically by the chat service and handed to MCP agents
                  with their search results, so it travels with the data instead of living in every client.
                  Use <code className="px-1 bg-gray-100 rounded">{'{{collection}}'}</code> and{' '}
                  <code className="px-1 bg-gray-100 rounded">{'{{query}}'}</code> as placeholders.
                </p>
                <textarea
                  value={responsePrompt}
                  onChange={(e) => setResponsePrompt(e.target.value.slice(0, 4096))}
                  rows={4}
                  placeholder="e.g. Answer as numbered steps. Quote the exact command for each step and name the file it belongs in."
                  className="w-full px-2 py-1.5 border border-gray-300 rounded text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500"
                />
                <p className={`text-xs mt-0.5 ${responsePrompt.length > 3800 ? 'text-amber-600' : 'text-gray-400'}`}>
                  {responsePrompt.length} / 4096 characters
                  {responsePrompt.length > 3800 && ' — this is prepended to every prompt, so it competes with the answer for context'}
                </p>
              </div>

              {/* At-Rest Encryption (ISO 27001 A.8.24 / SOC 2 CC6.7) */}
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">At-Rest Encryption</label>
                <label className="flex items-center gap-2 text-sm text-gray-600 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={encrypted}
                    onChange={(e) => setEncrypted(e.target.checked)}
                    className="rounded border-gray-300 text-blue-600 focus:ring-blue-500"
                  />
                  Encrypt this collection (AES-256-GCM)
                </label>
                <p className="text-xs text-gray-400 mt-1">
                  Requires <span className="font-mono">MDDB_ENCRYPTION_KEY</span> to be set on the server. Legacy plaintext documents remain readable after enabling.
                </p>
              </div>

              {/* Custom Metadata */}
              <div>
                <div className="flex items-center justify-between mb-1">
                  <label className="block text-sm font-medium text-gray-700">Custom Metadata</label>
                  <button
                    type="button"
                    onClick={addMetaEntry}
                    className="flex items-center gap-1 text-xs text-blue-600 hover:text-blue-700"
                  >
                    <Plus className="w-3 h-3" />
                    Add
                  </button>
                </div>
                {customMeta.length === 0 ? (
                  <p className="text-xs text-gray-400">No custom metadata defined</p>
                ) : (
                  <div className="space-y-2">
                    {customMeta.map((entry, idx) => (
                      <div key={idx} className="flex items-center gap-2">
                        <input
                          type="text"
                          value={entry.key}
                          onChange={(e) => updateMetaEntry(idx, 'key', e.target.value)}
                          placeholder="Key"
                          className="flex-1 px-2 py-1.5 border border-gray-300 rounded text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                        />
                        <input
                          type="text"
                          value={entry.value}
                          onChange={(e) => updateMetaEntry(idx, 'value', e.target.value)}
                          placeholder="Value"
                          className="flex-1 px-2 py-1.5 border border-gray-300 rounded text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                        />
                        <button
                          type="button"
                          onClick={() => removeMetaEntry(idx)}
                          className="p-1 text-red-400 hover:text-red-600"
                        >
                          <Trash2 className="w-3.5 h-3.5" />
                        </button>
                      </div>
                    ))}
                  </div>
                )}
              </div>

              {/* Error */}
              {error && (
                <div className="p-3 bg-red-50 border border-red-200 rounded-lg">
                  <p className="text-sm text-red-800">{error}</p>
                </div>
              )}

              {/* Actions */}
              <div className="flex gap-3 pt-2">
                <button
                  type="button"
                  onClick={onClose}
                  className="flex-1 px-4 py-2 text-gray-700 bg-gray-100 hover:bg-gray-200 rounded-lg transition-colors"
                >
                  Cancel
                </button>
                <button
                  type="button"
                  onClick={handleSave}
                  disabled={saving}
                  className="flex-1 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 transition-colors disabled:opacity-50 flex items-center justify-center gap-2"
                >
                  {saving ? (
                    <div className="animate-spin rounded-full h-4 w-4 border-b-2 border-white" />
                  ) : (
                    <Settings2 className="w-4 h-4" />
                  )}
                  Save Settings
                </button>
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
