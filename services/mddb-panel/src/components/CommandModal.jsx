import { useState } from 'react';
import { jsonToPHP, jsonToPython } from '../lib/code-snippets';
import { X, Copy, Check } from 'lucide-react';

function generateCurl(type, params) {
  const base = 'http://localhost:11023/v1';
  let endpoint, body;

  switch (type) {
    case 'search':
      endpoint = '/search';
      body = {
        collection: params.collection || 'my-collection',
        filterMeta: params.filterMeta || {},
        sort: params.sort || 'addedAt',
        asc: params.asc ?? false,
        limit: params.limit || 100,
        offset: params.offset || 0,
      };
      break;
    case 'fts':
      endpoint = '/fts';
      body = {
        collection: params.collection || 'my-collection',
        query: params.query || '',
        limit: params.limit || 50,
        algorithm: params.algorithm || 'tfidf',
        fuzzy: params.fuzzy || 0,
        disableStem: params.disableStem ?? false,
        disableSynonyms: params.disableSynonyms ?? false,
      };
      if (params.algorithm === 'bm25f' && params.fieldWeights) {
        body.fieldWeights = params.fieldWeights;
      }
      break;
    case 'vector':
      endpoint = '/vector-search';
      body = {
        collection: params.collection || 'my-collection',
        query: params.query || '',
        topK: params.topK || 10,
        threshold: params.threshold || 0.0,
        algorithm: params.algorithm || 'flat',
        includeContent: params.includeContent ?? false,
      };
      break;
    case 'hybrid':
      endpoint = '/hybrid-search';
      body = {
        collection: params.collection || 'my-collection',
        query: params.query || '',
        topK: params.topK || 10,
        algorithm: params.algorithm || 'bm25',
        vectorAlgorithm: params.vectorAlgorithm || 'flat',
        alpha: params.alpha ?? 0.5,
        strategy: params.strategy || 'alpha',
        rrfK: params.rrfK || 60,
        fuzzy: params.fuzzy || 0,
        threshold: params.threshold || 0.0,
        includeContent: params.includeContent ?? false,
      };
      break;
    default:
      endpoint = '/search';
      body = {};
  }

  const json = JSON.stringify(body, null, 2);
  return `curl -X POST ${base}${endpoint} \\
  -H "Content-Type: application/json" \\
  -d '${json}'`;
}

function generatePHP(type, params) {
  const base = 'http://localhost:11023/v1';
  let endpoint, body;

  switch (type) {
    case 'search':
      endpoint = '/search';
      body = {
        collection: params.collection || 'my-collection',
        filterMeta: params.filterMeta || {},
        sort: params.sort || 'addedAt',
        asc: params.asc ?? false,
        limit: params.limit || 100,
        offset: params.offset || 0,
      };
      break;
    case 'fts':
      endpoint = '/fts';
      body = {
        collection: params.collection || 'my-collection',
        query: params.query || '',
        limit: params.limit || 50,
        algorithm: params.algorithm || 'tfidf',
        fuzzy: params.fuzzy || 0,
        disableStem: params.disableStem ?? false,
        disableSynonyms: params.disableSynonyms ?? false,
      };
      if (params.algorithm === 'bm25f' && params.fieldWeights) {
        body.fieldWeights = params.fieldWeights;
      }
      break;
    case 'vector':
      endpoint = '/vector-search';
      body = {
        collection: params.collection || 'my-collection',
        query: params.query || '',
        topK: params.topK || 10,
        threshold: params.threshold || 0.0,
        algorithm: params.algorithm || 'flat',
        includeContent: params.includeContent ?? false,
      };
      break;
    case 'hybrid':
      endpoint = '/hybrid-search';
      body = {
        collection: params.collection || 'my-collection',
        query: params.query || '',
        topK: params.topK || 10,
        algorithm: params.algorithm || 'bm25',
        vectorAlgorithm: params.vectorAlgorithm || 'flat',
        alpha: params.alpha ?? 0.5,
        strategy: params.strategy || 'alpha',
        rrfK: params.rrfK || 60,
        fuzzy: params.fuzzy || 0,
        threshold: params.threshold || 0.0,
        includeContent: params.includeContent ?? false,
      };
      break;
    default:
      endpoint = '/search';
      body = {};
  }

  const phpArray = jsonToPHP(body);
  return `<?php
$url = '${base}${endpoint}';
$data = ${phpArray};

$options = [
    'http' => [
        'method'  => 'POST',
        'header'  => 'Content-Type: application/json',
        'content' => json_encode($data),
    ],
];

$context = stream_context_create($options);
$response = file_get_contents($url, false, $context);
$result = json_decode($response, true);

print_r($result);`;
}



function generatePython(type, params) {
  const base = 'http://localhost:11023/v1';
  let endpoint, body;

  switch (type) {
    case 'search':
      endpoint = '/search';
      body = {
        collection: params.collection || 'my-collection',
        filterMeta: params.filterMeta || {},
        sort: params.sort || 'addedAt',
        asc: params.asc ?? false,
        limit: params.limit || 100,
        offset: params.offset || 0,
      };
      break;
    case 'fts':
      endpoint = '/fts';
      body = {
        collection: params.collection || 'my-collection',
        query: params.query || '',
        limit: params.limit || 50,
        algorithm: params.algorithm || 'tfidf',
        fuzzy: params.fuzzy || 0,
        disableStem: params.disableStem ?? false,
        disableSynonyms: params.disableSynonyms ?? false,
      };
      if (params.algorithm === 'bm25f' && params.fieldWeights) {
        body.fieldWeights = params.fieldWeights;
      }
      break;
    case 'vector':
      endpoint = '/vector-search';
      body = {
        collection: params.collection || 'my-collection',
        query: params.query || '',
        topK: params.topK || 10,
        threshold: params.threshold || 0.0,
        algorithm: params.algorithm || 'flat',
        includeContent: params.includeContent ?? false,
      };
      break;
    case 'hybrid':
      endpoint = '/hybrid-search';
      body = {
        collection: params.collection || 'my-collection',
        query: params.query || '',
        topK: params.topK || 10,
        algorithm: params.algorithm || 'bm25',
        vectorAlgorithm: params.vectorAlgorithm || 'flat',
        alpha: params.alpha ?? 0.5,
        strategy: params.strategy || 'alpha',
        rrfK: params.rrfK || 60,
        fuzzy: params.fuzzy || 0,
        threshold: params.threshold || 0.0,
        includeContent: params.includeContent ?? false,
      };
      break;
    default:
      endpoint = '/search';
      body = {};
  }

  const pyDict = jsonToPython(body, 0);
  return `import requests

url = "${base}${endpoint}"
payload = ${pyDict}

response = requests.post(url, json=payload)
result = response.json()

print(result)`;
}



function generateJavaScript(type, params) {
  const base = 'http://localhost:11023/v1';
  let endpoint, body;

  switch (type) {
    case 'search':
      endpoint = '/search';
      body = {
        collection: params.collection || 'my-collection',
        filterMeta: params.filterMeta || {},
        sort: params.sort || 'addedAt',
        asc: params.asc ?? false,
        limit: params.limit || 100,
        offset: params.offset || 0,
      };
      break;
    case 'fts':
      endpoint = '/fts';
      body = {
        collection: params.collection || 'my-collection',
        query: params.query || '',
        limit: params.limit || 50,
        algorithm: params.algorithm || 'tfidf',
        fuzzy: params.fuzzy || 0,
        disableStem: params.disableStem ?? false,
        disableSynonyms: params.disableSynonyms ?? false,
      };
      if (params.algorithm === 'bm25f' && params.fieldWeights) {
        body.fieldWeights = params.fieldWeights;
      }
      break;
    case 'vector':
      endpoint = '/vector-search';
      body = {
        collection: params.collection || 'my-collection',
        query: params.query || '',
        topK: params.topK || 10,
        threshold: params.threshold || 0.0,
        algorithm: params.algorithm || 'flat',
        includeContent: params.includeContent ?? false,
      };
      break;
    case 'hybrid':
      endpoint = '/hybrid-search';
      body = {
        collection: params.collection || 'my-collection',
        query: params.query || '',
        topK: params.topK || 10,
        algorithm: params.algorithm || 'bm25',
        vectorAlgorithm: params.vectorAlgorithm || 'flat',
        alpha: params.alpha ?? 0.5,
        strategy: params.strategy || 'alpha',
        rrfK: params.rrfK || 60,
        fuzzy: params.fuzzy || 0,
        threshold: params.threshold || 0.0,
        includeContent: params.includeContent ?? false,
      };
      break;
    default:
      endpoint = '/search';
      body = {};
  }

  const json = JSON.stringify(body, null, 2);
  return `const response = await fetch("${base}${endpoint}", {
  method: "POST",
  headers: {
    "Content-Type": "application/json",
  },
  body: JSON.stringify(${json}),
});

const result = await response.json();
console.log(result);`;
}

const TABS = [
  { key: 'curl', label: 'curl' },
  { key: 'php', label: 'PHP' },
  { key: 'python', label: 'Python' },
  { key: 'javascript', label: 'JavaScript' },
];

export default function CommandModal({ isOpen, onClose, type, params }) {
  const [activeTab, setActiveTab] = useState('curl');
  const [copied, setCopied] = useState(false);

  if (!isOpen) return null;

  const getCode = () => {
    switch (activeTab) {
      case 'curl': return generateCurl(type, params);
      case 'php': return generatePHP(type, params);
      case 'python': return generatePython(type, params);
      case 'javascript': return generateJavaScript(type, params);
      default: return '';
    }
  };

  const code = getCode();

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(code);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // Fallback
      const textarea = document.createElement('textarea');
      textarea.value = code;
      document.body.appendChild(textarea);
      textarea.select();
      document.execCommand('copy');
      document.body.removeChild(textarea);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }
  };

  return (
    <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50 p-4">
      <div className="bg-white rounded-lg shadow-xl w-full max-w-2xl max-h-[85vh] flex flex-col">
        {/* Header */}
        <div className="flex items-center justify-between p-4 border-b border-gray-200">
          <h2 className="text-lg font-bold text-gray-900">API Command</h2>
          <button
            onClick={onClose}
            className="text-gray-400 hover:text-gray-600 transition-colors"
          >
            <X className="w-5 h-5" />
          </button>
        </div>

        {/* Tabs */}
        <div className="flex items-center space-x-1 px-4 pt-3">
          {TABS.map((tab) => (
            <button
              key={tab.key}
              onClick={() => setActiveTab(tab.key)}
              className={`px-3 py-1.5 rounded-md text-sm font-medium transition-colors ${
                activeTab === tab.key
                  ? 'bg-gray-900 text-white'
                  : 'text-gray-600 hover:text-gray-900 hover:bg-gray-100'
              }`}
            >
              {tab.label}
            </button>
          ))}
        </div>

        {/* Code block */}
        <div className="flex-1 overflow-hidden p-4">
          <div className="relative h-full">
            <button
              onClick={handleCopy}
              className="absolute top-2 right-2 z-10 flex items-center space-x-1 px-2 py-1 rounded text-xs font-medium bg-gray-700 text-gray-300 hover:bg-gray-600 hover:text-white transition-colors"
            >
              {copied ? (
                <>
                  <Check className="w-3 h-3" />
                  <span>Copied</span>
                </>
              ) : (
                <>
                  <Copy className="w-3 h-3" />
                  <span>Copy</span>
                </>
              )}
            </button>
            <pre className="h-full overflow-auto bg-gray-900 text-gray-100 rounded-lg p-4 text-sm font-mono leading-relaxed">
              <code>{code}</code>
            </pre>
          </div>
        </div>
      </div>
    </div>
  );
}
