import { useState } from 'react';
import { BookOpen, Plus, Trash2, Wand2 } from 'lucide-react';
import { useStore } from '../lib/store';
import mddbClient from '../lib/mddb-client';
import SpellCleanupBox from './SpellCleanupBox';

const LANGUAGES = [
  { value: 'en', label: 'English' },
  { value: 'de', label: 'German' },
  { value: 'fr', label: 'French' },
  { value: 'pl', label: 'Polish' },
  { value: 'es', label: 'Spanish' },
  { value: 'it', label: 'Italian' },
];

export default function SpellCheckPanel() {
  const { currentCollection } = useStore();

  // Test tab
  const [testText, setTestText] = useState('');
  const [testLang, setTestLang] = useState('en');
  const [testResult, setTestResult] = useState(null);
  const [testLoading, setTestLoading] = useState(false);
  const [testError, setTestError] = useState(null);

  // Dictionary tab
  const [dictLang, setDictLang] = useState('en');
  const [dictWords, setDictWords] = useState([]);
  const [dictLoading, setDictLoading] = useState(false);
  const [newWord, setNewWord] = useState('');
  const [addingWord, setAddingWord] = useState(false);
  const [dictError, setDictError] = useState(null);

  const [activeTab, setActiveTab] = useState('test');

  const handleTest = async () => {
    if (!testText.trim()) return;
    setTestLoading(true);
    setTestError(null);
    setTestResult(null);
    try {
      const result = await mddbClient.spellSuggest({
        collection: currentCollection || '',
        text: testText,
        lang: testLang,
        maxSuggestions: 10,
      });
      setTestResult(result);
    } catch (err) {
      setTestError(err.message);
    } finally {
      setTestLoading(false);
    }
  };

  const loadDictionary = async () => {
    setDictLoading(true);
    setDictError(null);
    try {
      const data = await mddbClient.spellDictionary('GET', {
        collection: currentCollection || '',
        lang: dictLang,
      });
      setDictWords(data.words || []);
    } catch (err) {
      setDictError(err.message);
    } finally {
      setDictLoading(false);
    }
  };

  const handleAddWord = async () => {
    if (!newWord.trim()) return;
    setAddingWord(true);
    try {
      await mddbClient.spellDictionary('PUT', {
        collection: currentCollection || '',
        lang: dictLang,
        words: [newWord.trim()],
      });
      setNewWord('');
      await loadDictionary();
    } catch (err) {
      setDictError(err.message);
    } finally {
      setAddingWord(false);
    }
  };

  const handleRemoveWord = async (word) => {
    try {
      await mddbClient.spellDictionary('DELETE', {
        collection: currentCollection || '',
        lang: dictLang,
        words: [word],
      });
      setDictWords((prev) => prev.filter((w) => w !== word));
    } catch (err) {
      setDictError(err.message);
    }
  };

  return (
    <div className="h-full flex flex-col overflow-hidden">
      <div className="p-4 border-b border-gray-200">
        <div className="flex items-center gap-2 mb-1">
          <BookOpen className="w-5 h-5 text-blue-600" />
          <h2 className="text-lg font-semibold text-gray-900">Spell Checker</h2>
        </div>
        <p className="text-sm text-gray-500">
          Test spell corrections and manage custom dictionaries
          {currentCollection && <span className="ml-1 text-blue-600">· {currentCollection}</span>}
        </p>
      </div>

      {/* Tabs */}
      <div className="flex border-b border-gray-200 px-4">
        {['test', 'dictionary'].map((tab) => (
          <button
            key={tab}
            onClick={() => {
              setActiveTab(tab);
              if (tab === 'dictionary') loadDictionary();
            }}
            className={`px-4 py-2.5 text-sm font-medium border-b-2 transition-colors capitalize ${
              activeTab === tab
                ? 'border-blue-600 text-blue-600'
                : 'border-transparent text-gray-500 hover:text-gray-700'
            }`}
          >
            {tab === 'test' ? 'Test Spell Checker' : 'Custom Dictionary'}
          </button>
        ))}
      </div>

      <div className="flex-1 overflow-y-auto p-4">
        {activeTab === 'test' && (
          <div className="space-y-4 max-w-2xl">
            <div className="flex gap-3">
              <div className="flex-1">
                <label className="block text-sm font-medium text-gray-700 mb-1">Text to check</label>
                <textarea
                  value={testText}
                  onChange={(e) => setTestText(e.target.value)}
                  onKeyDown={(e) => { if (e.key === 'Enter' && e.ctrlKey) handleTest(); }}
                  placeholder="Type text with typos to test spell correction..."
                  rows={3}
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm resize-none"
                />
              </div>
              <div className="w-32">
                <label className="block text-sm font-medium text-gray-700 mb-1">Language</label>
                <select
                  value={testLang}
                  onChange={(e) => setTestLang(e.target.value)}
                  className="w-full px-2 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm"
                >
                  {LANGUAGES.map((l) => (
                    <option key={l.value} value={l.value}>{l.label}</option>
                  ))}
                </select>
              </div>
            </div>

            <button
              onClick={handleTest}
              disabled={testLoading || !testText.trim()}
              className="flex items-center gap-2 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed text-sm font-medium transition-colors"
            >
              <Wand2 className="w-4 h-4" />
              {testLoading ? 'Checking...' : 'Check Spelling'}
            </button>

            {/* 2.19.0: apply the corrections (/v1/spell-cleanup) */}
            <SpellCleanupBox collection={currentCollection || ''} text={testText} lang={testLang} />

            {testError && (
              <div className="p-3 bg-red-50 border border-red-200 rounded-lg text-sm text-red-700">
                {testError}
              </div>
            )}

            {testResult && (
              <div className="space-y-3">
                <div className="p-3 bg-gray-50 rounded-lg border border-gray-200">
                  <div className="text-xs text-gray-500 mb-1">Original</div>
                  <div className="text-sm text-gray-800 font-mono">{testResult.originalText}</div>
                </div>
                <div className="p-3 bg-green-50 rounded-lg border border-green-200">
                  <div className="text-xs text-green-600 mb-1">Suggested</div>
                  <div className="text-sm text-gray-800 font-mono">{testResult.suggestedText}</div>
                </div>
                {testResult.tokenSuggestions && testResult.tokenSuggestions.length > 0 ? (
                  <div>
                    <div className="text-xs font-medium text-gray-600 mb-2 uppercase tracking-wide">
                      Token corrections ({testResult.tokenSuggestions.length})
                    </div>
                    <div className="space-y-1">
                      {testResult.tokenSuggestions.map((s, i) => (
                        <div key={i} className="flex items-center gap-2 text-sm py-1.5 px-3 bg-white border border-gray-100 rounded">
                          <span className="text-red-500 line-through">{s.original}</span>
                          <span className="text-gray-400">→</span>
                          <span className="text-green-600 font-medium">{s.corrected}</span>
                          <span className="ml-auto text-xs text-gray-400">
                            {Math.round(s.confidence * 100)}% confidence
                          </span>
                        </div>
                      ))}
                    </div>
                  </div>
                ) : (
                  <div className="text-sm text-gray-500 text-center py-4">
                    No corrections needed — text looks good!
                  </div>
                )}
              </div>
            )}
          </div>
        )}

        {activeTab === 'dictionary' && (
          <div className="space-y-4 max-w-2xl">
            <div className="flex items-center gap-3">
              <select
                value={dictLang}
                onChange={(e) => { setDictLang(e.target.value); }}
                className="px-2 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm"
              >
                {LANGUAGES.map((l) => (
                  <option key={l.value} value={l.value}>{l.label}</option>
                ))}
              </select>
              <button
                onClick={loadDictionary}
                className="px-3 py-2 text-sm text-blue-600 hover:text-blue-800 border border-blue-200 rounded-lg hover:bg-blue-50 transition-colors"
              >
                Load
              </button>
            </div>

            <p className="text-xs text-gray-500">
              Custom words added here will never be corrected (treated as valid terms).
              {currentCollection ? ` Scoped to collection "${currentCollection}".` : ' Global (all collections).'}
            </p>

            {dictError && (
              <div className="p-3 bg-red-50 border border-red-200 rounded-lg text-sm text-red-700">
                {dictError}
              </div>
            )}

            {/* Add word */}
            <div className="flex gap-2">
              <input
                type="text"
                value={newWord}
                onChange={(e) => setNewWord(e.target.value)}
                onKeyDown={(e) => { if (e.key === 'Enter') handleAddWord(); }}
                placeholder="Add a custom word..."
                className="flex-1 px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm"
              />
              <button
                onClick={handleAddWord}
                disabled={addingWord || !newWord.trim()}
                className="flex items-center gap-1.5 px-3 py-2 bg-green-600 text-white rounded-lg hover:bg-green-700 disabled:opacity-50 text-sm font-medium transition-colors"
              >
                <Plus className="w-4 h-4" />
                Add
              </button>
            </div>

            {dictLoading ? (
              <div className="flex justify-center py-8">
                <div className="animate-spin rounded-full h-6 w-6 border-b-2 border-blue-600"></div>
              </div>
            ) : dictWords.length === 0 ? (
              <div className="text-sm text-gray-500 text-center py-8">
                No custom words yet. Add domain-specific terms that should not be spell-corrected.
              </div>
            ) : (
              <div className="space-y-1">
                {dictWords.map((word) => (
                  <div key={word} className="flex items-center justify-between px-3 py-2 bg-white border border-gray-100 rounded-lg group hover:border-gray-200">
                    <span className="text-sm text-gray-800 font-mono">{word}</span>
                    <button
                      onClick={() => handleRemoveWord(word)}
                      className="opacity-0 group-hover:opacity-100 p-1 text-red-500 hover:bg-red-50 rounded transition-opacity"
                      title="Remove word"
                    >
                      <Trash2 className="w-3.5 h-3.5" />
                    </button>
                  </div>
                ))}
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
