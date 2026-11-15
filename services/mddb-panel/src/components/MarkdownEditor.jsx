import { useState, useRef } from 'react';
import { Eye, Code, Maximize2, Minimize2 } from 'lucide-react';
import MarkdownToolbar from './MarkdownToolbar';
import MarkdownView from './MarkdownView';
import CollectionSpelling from './CollectionSpelling';
import { getTemplate } from '../lib/markdown-templates';
import { applyFormat, findFormatAction, indentLines, selectionIsList } from '../lib/markdown-format';
import { editorCommand, isMacPlatform } from '../lib/editor-shortcuts';

/**
 * MarkdownEditor: a Markdown textarea with a toolbar, edit / split / preview
 * modes and fullscreen. Keyboard (FE-016): Ctrl/Cmd+B, I, K format like the
 * toolbar, Ctrl/Cmd+S calls `onSave` (when given), Tab / Shift+Tab indent
 * list items, Esc leaves fullscreen. With `spelling` ({ collection, lang })
 * it offers the collection spelling check (FE-020).
 */
export default function MarkdownEditor({ value, onChange, placeholder, onSave, spelling }) {
  const [mode, setMode] = useState('split'); // 'edit', 'preview', 'split'
  const [isFullscreen, setIsFullscreen] = useState(false);
  const textareaRef = useRef(null);
  const isMac = isMacPlatform();
  const text = value || '';

  const handleChange = (e) => {
    if (onChange) {
      onChange(e.target.value);
    }
  };

  /** Writes `next` and, once React has rendered it, selects [start, end). */
  const replaceText = (next, start, end = start) => {
    if (onChange) onChange(next);
    const textarea = textareaRef.current;
    setTimeout(() => {
      textarea.focus();
      textarea.setSelectionRange(start, end);
    }, 0);
  };

  /** Runs a format action at the selection — the toolbar and the keys call this. */
  const runFormat = (id) => {
    const textarea = textareaRef.current;
    const action = findFormatAction(id);
    if (!textarea || !action) return;
    const result = applyFormat(text, textarea.selectionStart, textarea.selectionEnd, action);
    replaceText(result.value, result.cursor);
  };

  const handleKeyDown = (e) => {
    const command = editorCommand(e, isMac);
    if (!command) return;
    const { selectionStart: start, selectionEnd: end } = e.currentTarget;
    if (command === 'escape') {
      if (isFullscreen) {
        e.preventDefault();
        e.stopPropagation();
        setIsFullscreen(false);
      }
      return;
    }
    if (command === 'indent' || command === 'outdent') {
      // Outside a list Tab keeps moving the focus (no keyboard trap).
      if (!selectionIsList(text, start, end)) return;
      e.preventDefault();
      const result = indentLines(text, start, end, command === 'outdent');
      replaceText(result.value, result.start, result.end);
      return;
    }
    e.preventDefault();
    if (command === 'save') onSave?.();
    else runFormat(command);
  };

  const handleTemplate = (templateType) => {
    const template = getTemplate(templateType);
    if (onChange && template) {
      onChange(template);
    }
  };

  return (
    <div className={`${isFullscreen ? 'fixed inset-0 z-50 bg-white' : 'relative'}`}>
      {/* Markdown Toolbar */}
      <MarkdownToolbar onFormat={runFormat} onTemplate={handleTemplate} isMac={isMac} />

      {/* View Mode Toolbar */}
      <div className="flex items-center justify-between border-b border-gray-200 bg-gray-50 px-4 py-2">
        <div className="flex items-center space-x-2">
          <button
            onClick={() => setMode('edit')}
            className={`flex items-center space-x-1 px-3 py-1.5 rounded text-sm transition-colors ${
              mode === 'edit'
                ? 'bg-primary-600 text-white'
                : 'text-gray-600 hover:bg-gray-200'
            }`}
          >
            <Code className="w-4 h-4" />
            <span>Edit</span>
          </button>
          <button
            onClick={() => setMode('split')}
            className={`flex items-center space-x-1 px-3 py-1.5 rounded text-sm transition-colors ${
              mode === 'split'
                ? 'bg-primary-600 text-white'
                : 'text-gray-600 hover:bg-gray-200'
            }`}
          >
            <span>Split</span>
          </button>
          <button
            onClick={() => setMode('preview')}
            className={`flex items-center space-x-1 px-3 py-1.5 rounded text-sm transition-colors ${
              mode === 'preview'
                ? 'bg-primary-600 text-white'
                : 'text-gray-600 hover:bg-gray-200'
            }`}
          >
            <Eye className="w-4 h-4" />
            <span>Preview</span>
          </button>
        </div>

        <button
          onClick={() => setIsFullscreen(!isFullscreen)}
          className="p-1.5 text-gray-600 hover:bg-gray-200 rounded transition-colors"
          title={isFullscreen ? 'Exit fullscreen (Esc)' : 'Fullscreen'}
          aria-label={isFullscreen ? 'Exit fullscreen' : 'Fullscreen'}
        >
          {isFullscreen ? (
            <Minimize2 className="w-4 h-4" />
          ) : (
            <Maximize2 className="w-4 h-4" />
          )}
        </button>
      </div>

      {/* Editor/Preview Area */}
      <div className={`flex ${isFullscreen ? 'h-[calc(100vh-48px)]' : 'h-96'}`}>
        {/* Editor */}
        {(mode === 'edit' || mode === 'split') && (
          <div className={`${mode === 'split' ? 'w-1/2 border-r border-gray-200' : 'w-full'}`}>
            <textarea
              ref={textareaRef}
              value={value}
              onChange={handleChange}
              onKeyDown={handleKeyDown}
              placeholder={placeholder || 'Write your markdown here...'}
              aria-label="Markdown content"
              className="w-full h-full px-4 py-3 text-sm font-mono resize-none focus:outline-none"
            />
          </div>
        )}

        {/* Preview */}
        {(mode === 'preview' || mode === 'split') && (
          <div className={`${mode === 'split' ? 'w-1/2' : 'w-full'} overflow-y-auto`}>
            <div className="px-4 py-3 prose prose-sm max-w-none">
              <MarkdownView>{value || '*Nothing to preview*'}</MarkdownView>
            </div>
          </div>
        )}
      </div>

      {spelling && (
        <CollectionSpelling collection={spelling.collection} lang={spelling.lang} text={text} onChange={onChange} />
      )}

      {/* Character Count */}
      <div className="border-t border-gray-200 px-4 py-2 bg-gray-50 text-xs text-gray-500">
        {value?.length || 0} characters
        {value && ` • ${value.split('\n').length} lines`}
        {value && ` • ${value.split(/\s+/).filter(w => w).length} words`}
      </div>
    </div>
  );
}
