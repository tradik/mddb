import { useState, useRef } from 'react';
import { Eye, Code, Maximize2, Minimize2 } from 'lucide-react';
import MarkdownToolbar from './MarkdownToolbar';
import MarkdownView from './MarkdownView';
import { getTemplate } from '../lib/markdown-templates';

export default function MarkdownEditor({ value, onChange, placeholder }) {
  const [mode, setMode] = useState('split'); // 'edit', 'preview', 'split'
  const [isFullscreen, setIsFullscreen] = useState(false);
  const textareaRef = useRef(null);

  const handleChange = (e) => {
    if (onChange) {
      onChange(e.target.value);
    }
  };

  const handleInsert = (before, after = '', placeholder = '') => {
    const textarea = textareaRef.current;
    if (!textarea) return;

    const start = textarea.selectionStart;
    const end = textarea.selectionEnd;
    const selectedText = value.substring(start, end);
    const textToInsert = selectedText || placeholder;
    
    const newValue = 
      value.substring(0, start) + 
      before + textToInsert + after + 
      value.substring(end);
    
    if (onChange) {
      onChange(newValue);
    }

    // Set cursor position after insert
    setTimeout(() => {
      const newCursorPos = start + before.length + textToInsert.length;
      textarea.focus();
      textarea.setSelectionRange(newCursorPos, newCursorPos);
    }, 0);
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
      <MarkdownToolbar onInsert={handleInsert} onTemplate={handleTemplate} />

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
          title={isFullscreen ? 'Exit fullscreen' : 'Fullscreen'}
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
              placeholder={placeholder || 'Write your markdown here...'}
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

      {/* Character Count */}
      <div className="border-t border-gray-200 px-4 py-2 bg-gray-50 text-xs text-gray-500">
        {value?.length || 0} characters
        {value && ` • ${value.split('\n').length} lines`}
        {value && ` • ${value.split(/\s+/).filter(w => w).length} words`}
      </div>
    </div>
  );
}
