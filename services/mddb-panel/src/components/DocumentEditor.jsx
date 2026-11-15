import { useState, useEffect } from 'react';
import { X, Save, AlertCircle, Check } from 'lucide-react';
import { useStore } from '../lib/store';
import mddbClient from '../lib/mddb-client';
import { readDraft, removeDraft, draftDiffers, sameMeta } from '../lib/drafts';
import { useDraftAutosave } from '../lib/use-draft';
import MarkdownEditor from './MarkdownEditor';
import DraftRestoreBanner from './DraftRestoreBanner';
import RevisionConflictNotice from './RevisionConflictNotice';

/** A stored draft of `document` worth offering: one that differs from it. */
function offeredDraft(document) {
  if (!document) return null;
  const draft = readDraft(document);
  return draft && draftDiffers(draft, document) ? draft : null;
}

/**
 * DocumentEditor: the full-page editor of one document's metadata and
 * Markdown. FE-019: unsaved changes are kept as a draft (see use-draft.js);
 * a draft found on opening is offered for restore; a save is conditional on
 * the revision the edit started from, so a newer change on the server is
 * reported (409) instead of overwritten. Ctrl/Cmd+S in the editor saves.
 */
export default function DocumentEditor({ document, onClose, onSave }) {
  const { setCurrentDocument } = useStore();
  const [contentMd, setContentMd] = useState(document?.contentMd || '');
  const [metadata, setMetadata] = useState(document?.meta || {});
  const [newMetaKey, setNewMetaKey] = useState('');
  const [newMetaValue, setNewMetaValue] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState(null);
  const [success, setSuccess] = useState(false);
  const [baseRevision, setBaseRevision] = useState(document?.revision || '');
  const [pendingDraft, setPendingDraft] = useState(() => offeredDraft(document));
  const [conflict, setConflict] = useState(null);

  useEffect(() => {
    if (document) {
      setContentMd(document.contentMd || '');
      setMetadata(document.meta || {});
      setBaseRevision(document.revision || '');
    }
  }, [document]);

  const docRef = { collection: document?.collection, key: document?.key, lang: document?.lang };
  const dirty = Boolean(document) && (contentMd !== (document.contentMd || '') || !sameMeta(metadata, document.meta));
  const { discard } = useDraftAutosave(
    docRef,
    { contentMd, meta: metadata, baseRevision },
    { dirty, paused: Boolean(pendingDraft) || !document },
  );

  const restoreDraft = () => {
    setContentMd(pendingDraft.contentMd);
    setMetadata(pendingDraft.meta);
    setBaseRevision(pendingDraft.baseRevision);
    setPendingDraft(null);
  };

  const discardDraft = () => {
    removeDraft(docRef);
    setPendingDraft(null);
  };

  const handleAddMetadata = () => {
    if (newMetaKey && newMetaValue) {
      setMetadata({
        ...metadata,
        [newMetaKey]: [newMetaValue],
      });
      setNewMetaKey('');
      setNewMetaValue('');
    }
  };

  const handleRemoveMetadata = (key) => {
    const updated = { ...metadata };
    delete updated[key];
    setMetadata(updated);
  };

  const handleUpdateMetadata = (key, value) => {
    setMetadata({
      ...metadata,
      [key]: [value],
    });
  };

  /**
   * Saves the edit, conditional on `expected` (by default the revision the
   * edit started from). A 409 keeps the edit and shows the conflict.
   */
  const handleSave = async (expected = baseRevision) => {
    if (saving) return;
    setSaving(true);
    setError(null);
    setSuccess(false);
    setConflict(null);

    try {
      await mddbClient.addDocument({
        collection: document.collection,
        key: document.key,
        lang: document.lang,
        meta: metadata,
        contentMd: contentMd,
        expectedRevision: expected,
      });
      discard();

      // Refresh the document in the store so viewer shows updated content
      const refreshed = await mddbClient.getDocument({
        collection: document.collection,
        key: document.key,
        lang: document.lang,
      });
      setCurrentDocument({ ...refreshed, collection: document.collection });

      setSuccess(true);
      setTimeout(() => {
        setSuccess(false);
        if (onSave) onSave();
      }, 1500);
    } catch (err) {
      if (err.status === 409) setConflict({ expected, current: err.body?.currentRevision || '' });
      else setError(err.message);
    } finally {
      setSaving(false);
    }
  };

  if (!document) return null;

  return (
    <div className="fixed inset-0 bg-surface z-50 flex flex-col">
      {/* Header */}
      <div className="border-b border-line p-4 flex-shrink-0">
        <div className="flex items-center justify-between">
          <div>
            <h2 className="text-xl font-semibold text-fg">
              Edit Document
            </h2>
            <p className="text-sm text-fg-faint mt-1">
              {document.collection} / {document.key} ({document.lang})
            </p>
          </div>
          <button
            onClick={onClose}
            aria-label="Close editor"
            className="p-2 text-fg-faint hover:text-fg-subtle rounded-lg hover:bg-surface-subtle"
          >
            <X className="w-5 h-5" />
          </button>
        </div>
      </div>

      {/* Content */}
      <div className="flex-1 overflow-y-auto p-6 space-y-6">
        {pendingDraft && (
          <DraftRestoreBanner draft={pendingDraft} document={document} onRestore={restoreDraft} onDiscard={discardDraft} />
        )}

        {conflict && (
          <RevisionConflictNotice
            expected={conflict.expected}
            current={conflict.current}
            busy={saving}
            onOverwrite={() => handleSave(conflict.current)}
          />
        )}

        {/* Error Message */}
        {error && (
          <div className="bg-danger-soft border border-danger-line rounded-lg p-4 flex items-start space-x-3">
            <AlertCircle className="w-5 h-5 text-danger flex-shrink-0 mt-0.5" />
            <div>
              <h4 className="text-sm font-medium text-danger-strong">Error saving document</h4>
              <p className="text-sm text-danger mt-1">{error}</p>
            </div>
          </div>
        )}

        {/* Success Message */}
        {success && (
          <div className="bg-success-soft border border-success-line rounded-lg p-4 flex items-center space-x-3">
            <Check className="w-5 h-5 text-success" />
            <p className="text-sm font-medium text-success-strong">Document saved successfully!</p>
          </div>
        )}

        {/* Metadata Section */}
        <div>
          <h3 className="text-sm font-semibold text-fg-muted mb-3">Metadata</h3>

          {/* Existing Metadata */}
          <div className="space-y-2 mb-4">
            {Object.entries(metadata).map(([key, values]) => (
              <div key={key} className="flex items-center space-x-2">
                <input
                  type="text"
                  value={key}
                  disabled
                  className="flex-1 px-3 py-2 border border-line-strong rounded-lg text-sm bg-surface-muted"
                />
                <input
                  type="text"
                  value={Array.isArray(values) ? values.join(', ') : values}
                  onChange={(e) => handleUpdateMetadata(key, e.target.value)}
                  className="flex-1 px-3 py-2 border border-line-strong rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-focus"
                />
                <button
                  onClick={() => handleRemoveMetadata(key)}
                  className="px-3 py-2 text-danger hover:bg-danger-soft rounded-lg transition-colors"
                >
                  Remove
                </button>
              </div>
            ))}
          </div>

          {/* Add New Metadata */}
          <div className="flex items-center space-x-2">
            <input
              type="text"
              placeholder="Key (e.g., author)"
              value={newMetaKey}
              onChange={(e) => setNewMetaKey(e.target.value)}
              className="flex-1 px-3 py-2 border border-line-strong rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-focus"
            />
            <input
              type="text"
              placeholder="Value"
              value={newMetaValue}
              onChange={(e) => setNewMetaValue(e.target.value)}
              onKeyPress={(e) => e.key === 'Enter' && handleAddMetadata()}
              className="flex-1 px-3 py-2 border border-line-strong rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-focus"
            />
            <button
              onClick={handleAddMetadata}
              className="px-4 py-2 bg-primary-600 text-white rounded-lg hover:bg-primary-700 transition-colors text-sm font-medium"
            >
              Add
            </button>
          </div>
        </div>

        {/* Content Section */}
        <div className="flex-1 flex flex-col">
          <h3 className="text-sm font-semibold text-fg-muted mb-3">Content (Markdown)</h3>
          <div className="border border-line-strong rounded-lg overflow-hidden flex-1">
            <MarkdownEditor
              value={contentMd}
              onChange={setContentMd}
              onSave={() => handleSave()}
              spelling={{ collection: document.collection, lang: document.lang }}
              placeholder="Enter markdown content..."
            />
          </div>
        </div>
      </div>

      {/* Footer */}
      <div className="border-t border-line p-4 flex items-center justify-end space-x-3 flex-shrink-0">
        <button
          onClick={onClose}
          className="px-4 py-2 text-fg-muted bg-surface-subtle rounded-lg hover:bg-surface-strong transition-colors"
        >
          Cancel
        </button>
        <button
          onClick={() => handleSave()}
          disabled={saving}
          className="flex items-center space-x-2 px-4 py-2 bg-primary-600 text-white rounded-lg hover:bg-primary-700 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {saving ? (
            <>
              <div className="animate-spin rounded-full h-4 w-4 border-b-2 border-white"></div>
              <span>Saving...</span>
            </>
          ) : (
            <>
              <Save className="w-4 h-4" />
              <span>Save Changes</span>
            </>
          )}
        </button>
      </div>
    </div>
  );
}
