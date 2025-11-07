import { useState, useRef } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import rehypeRaw from 'rehype-raw';
import rehypeSanitize from 'rehype-sanitize';
import { Prism as SyntaxHighlighter } from 'react-syntax-highlighter';
import { vscDarkPlus } from 'react-syntax-highlighter/dist/esm/styles/prism';
import { Eye, Code, Maximize2, Minimize2 } from 'lucide-react';
import MarkdownToolbar from './MarkdownToolbar';
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
              <ReactMarkdown
                remarkPlugins={[remarkGfm]}
                rehypePlugins={[rehypeRaw, rehypeSanitize]}
                components={{
                  // Custom styling for markdown elements
                  h1: ({ node, ...props }) => (
                    <h1 className="text-3xl font-bold mb-4 mt-6 text-gray-900" {...props} />
                  ),
                  h2: ({ node, ...props }) => (
                    <h2 className="text-2xl font-bold mb-3 mt-5 text-gray-900" {...props} />
                  ),
                  h3: ({ node, ...props }) => (
                    <h3 className="text-xl font-bold mb-2 mt-4 text-gray-900" {...props} />
                  ),
                  h4: ({ node, ...props }) => (
                    <h4 className="text-lg font-semibold mb-2 mt-3 text-gray-900" {...props} />
                  ),
                  p: ({ node, ...props }) => (
                    <p className="mb-4 text-gray-700 leading-relaxed" {...props} />
                  ),
                  a: ({ node, ...props }) => (
                    <a className="text-primary-600 hover:text-primary-700 underline" {...props} />
                  ),
                  ul: ({ node, ...props }) => (
                    <ul className="list-disc list-inside mb-4 space-y-1" {...props} />
                  ),
                  ol: ({ node, ...props }) => (
                    <ol className="list-decimal list-inside mb-4 space-y-1" {...props} />
                  ),
                  li: ({ node, ...props }) => (
                    <li className="text-gray-700" {...props} />
                  ),
                  blockquote: ({ node, ...props }) => (
                    <blockquote className="border-l-4 border-gray-300 pl-4 italic my-4 text-gray-600" {...props} />
                  ),
                  code: ({ node, inline, className, children, ...props }) => {
                    const match = /language-(\w+)/.exec(className || '');
                    const language = match ? match[1] : '';
                    
                    return !inline && language ? (
                      <SyntaxHighlighter
                        style={vscDarkPlus}
                        language={language}
                        PreTag="div"
                        className="my-4 rounded-lg"
                        {...props}
                      >
                        {String(children).replace(/\n$/, '')}
                      </SyntaxHighlighter>
                    ) : inline ? (
                      <code className="bg-gray-100 text-red-600 px-1.5 py-0.5 rounded text-sm font-mono" {...props}>
                        {children}
                      </code>
                    ) : (
                      <code className="block bg-gray-900 text-gray-100 p-4 rounded-lg overflow-x-auto text-sm font-mono my-4" {...props}>
                        {children}
                      </code>
                    );
                  },
                  pre: ({ node, ...props }) => (
                    <div {...props} />
                  ),
                  table: ({ node, ...props }) => (
                    <div className="overflow-x-auto my-4">
                      <table className="min-w-full divide-y divide-gray-200 border border-gray-200" {...props} />
                    </div>
                  ),
                  thead: ({ node, ...props }) => (
                    <thead className="bg-gray-50" {...props} />
                  ),
                  th: ({ node, ...props }) => (
                    <th className="px-4 py-2 text-left text-xs font-medium text-gray-500 uppercase tracking-wider" {...props} />
                  ),
                  td: ({ node, ...props }) => (
                    <td className="px-4 py-2 text-sm text-gray-700 border-t border-gray-200" {...props} />
                  ),
                  hr: ({ node, ...props }) => (
                    <hr className="my-6 border-gray-300" {...props} />
                  ),
                  img: ({ node, ...props }) => (
                    <img className="max-w-full h-auto rounded-lg my-4" {...props} />
                  ),
                }}
              >
                {value || '*Nothing to preview*'}
              </ReactMarkdown>
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
