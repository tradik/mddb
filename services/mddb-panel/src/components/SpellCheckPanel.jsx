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
      <div className="p-4 border-b border-line">
        <div className="flex items-center gap-2 mb-1">
          <BookOpen className="w-5 h-5 text-info" />
          <h2 className="text-lg font-semibold text-fg">Spell Checker</h2>
        </div>
        <p className="text-sm text-fg-faint">
          Test spell corrections and manage custom dictionaries
          {currentCollection && <span className="ml-1 text-info">· {currentCollection}</span>}
        </p>
      </div>

      {/* Tabs */}
      <div className="flex border-b border-line px-4">
        {['test', 'dictionary'].map((tab) => (
          <button
            key={tab}
            onClick={() => {
              setActiveTab(tab);
              if (tab === 'dictionary') loadDictionary();
            }}
            className={`px-4 py-2.5 text-sm font-medium border-b-2 transition-colors capitalize ${
              activeTab === tab
                ? 'border-blue-600 text-info'
                : 'border-transparent text-fg-faint hover:text-fg-muted'
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
                <label className="block text-sm font-medium text-fg-muted mb-1">Text to check</label>
                <textarea
                  value={testText}
                  onChange={(e) => setTestText(e.target.value)}
                  onKeyDown={(e) => { if (e.key === 'Enter' && e.ctrlKey) handleTest(); }}
                  placeholder="Type text with typos to test spell correction..."
                  rows={3}
                  className="w-full px-3 py-2 border border-line-strong rounded-lg focus:outline-none focus:ring-2 focus:ring-focus text-sm resize-none"
                />
              </div>
              <div className="w-32">
                <label className="block text-sm font-medium text-fg-muted mb-1">Language</label>
                <select
                  value={testLang}
                  onChange={(e) => setTestLang(e.target.value)}
                  className="w-full px-2 py-2 border border-line-strong rounded-lg focus:outline-none focus:ring-2 focus:ring-focus text-sm"
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
              <div className="p-3 bg-danger-soft border border-danger-line rounded-lg text-sm text-danger">
                {testError}
              </div>
            )}

            {testResult && (
              <div className="space-y-3">
                <div className="p-3 bg-surface-muted rounded-lg border border-line">
                  <div className="text-xs text-fg-faint mb-1">Original</div>
                  <div className="text-sm text-fg-soft font-mono">{testResult.originalText}</div>
                </div>
                <div className="p-3 bg-success-soft rounded-lg border border-success-line">
                  <div className="text-xs text-success mb-1">Suggested</div>
                  <div className="text-sm text-fg-soft font-mono">{testResult.suggestedText}</div>
                </div>
                {testResult.tokenSuggestions && testResult.tokenSuggestions.length > 0 ? (
                  <div>
                    <div className="text-xs font-medium text-fg-subtle mb-2 uppercase tracking-wide">
                      Token corrections ({testResult.tokenSuggestions.length})
                    </div>
                    <div className="space-y-1">
                      {testResult.tokenSuggestions.map((s, i) => (
                        <div key={i} className="flex items-center gap-2 text-sm py-1.5 px-3 bg-surface border border-line-soft rounded">
                          <span className="text-danger line-through">{s.original}</span>
                          <span className="text-fg-faint">→</span>
                          <span className="text-success font-medium">{s.corrected}</span>
                          <span className="ml-auto text-xs text-fg-faint">
                            {Math.round(s.confidence * 100)}% confidence
                          </span>
                        </div>
                      ))}
                    </div>
                  </div>
                ) : (
                  <div className="text-sm text-fg-faint text-center py-4">
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
                className="px-2 py-2 border border-line-strong rounded-lg focus:outline-none focus:ring-2 focus:ring-focus text-sm"
              >
                {LANGUAGES.map((l) => (
                  <option key={l.value} value={l.value}>{l.label}</option>
                ))}
              </select>
              <button
                onClick={loadDictionary}
                className="px-3 py-2 text-sm text-info hover:text-info-strong border border-info-line rounded-lg hover:bg-info-soft transition-colors"
              >
                Load
              </button>
            </div>

            <p className="text-xs text-fg-faint">
              Custom words added here will never be corrected (treated as valid terms).
              {currentCollection ? ` Scoped to collection "${currentCollection}".` : ' Global (all collections).'}
            </p>

            {dictError && (
              <div className="p-3 bg-danger-soft border border-danger-line rounded-lg text-sm text-danger">
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
                className="flex-1 px-3 py-2 border border-line-strong rounded-lg focus:outline-none focus:ring-2 focus:ring-focus text-sm"
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
              <div className="text-sm text-fg-faint text-center py-8">
                No custom words yet. Add domain-specific terms that should not be spell-corrected.
              </div>
            ) : (
              <div className="space-y-1">
                {dictWords.map((word) => (
                  <div key={word} className="flex items-center justify-between px-3 py-2 bg-surface border border-line-soft rounded-lg group hover:border-line">
                    <span className="text-sm text-fg-soft font-mono">{word}</span>
                    <button
                      onClick={() => handleRemoveWord(word)}
                      className="opacity-0 group-hover:opacity-100 p-1 text-danger hover:bg-danger-soft rounded transition-opacity"
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
