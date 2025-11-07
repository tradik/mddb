import { X, Calendar, Tag, FileText, Copy, Check, Edit, Trash2, Loader2, AlertTriangle } from 'lucide-react';
import { useState, Component } from 'react';
import { format } from 'date-fns';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import rehypeRaw from 'rehype-raw';
import rehypeSanitize from 'rehype-sanitize';
import { Prism as SyntaxHighlighter } from 'react-syntax-highlighter';
import { vscDarkPlus } from 'react-syntax-highlighter/dist/esm/styles/prism';
import { useStore } from '../lib/store';
import DocumentEditor from './DocumentEditor';

// Error Boundary component
class ErrorBoundary extends Component {
  constructor(props) {
    super(props);
    this.state = { hasError: false, error: null, showRaw: false };
  }

  static getDerivedStateFromError(error) {
    return { hasError: true, error };
  }

  componentDidCatch(error, errorInfo) {
    console.error('ReactMarkdown Error:', error, errorInfo);
  }

  render() {
    if (this.state.hasError) {
      if (this.state.showRaw) {
        return (
          <div style={{ 
            padding: '16px', 
            backgroundColor: '#f8fafc', 
            border: '1px solid #e2e8f0', 
            borderRadius: '8px'
          }}>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '12px' }}>
              <div style={{ display: 'flex', alignItems: 'center' }}>
                <FileText style={{ width: '20px', height: '20px', marginRight: '8px', color: '#64748b' }} />
                <strong style={{ color: '#475569' }}>Raw Content</strong>
              </div>
              <button
                onClick={() => this.setState({ showRaw: false })}
                style={{
                  padding: '4px 8px',
                  fontSize: '12px',
                  backgroundColor: '#e2e8f0',
                  color: '#475569',
                  border: 'none',
                  borderRadius: '4px',
                  cursor: 'pointer'
                }}
              >
                Back to Markdown
              </button>
            </div>
            <pre style={{ 
              fontSize: '14px', 
              margin: 0,
              padding: '12px', 
              backgroundColor: '#fff', 
              border: '1px solid #e2e8f0',
              borderRadius: '4px',
              overflow: 'auto',
              maxHeight: '400px',
              whiteSpace: 'pre-wrap',
              wordWrap: 'break-word'
            }}>
              {this.props.children?.props?.children}
            </pre>
          </div>
        );
      }

      return (
        <div style={{ 
          padding: '16px', 
          backgroundColor: '#fef2f2', 
          border: '1px solid #fecaca', 
          borderRadius: '8px',
          color: '#dc2626'
        }}>
          <div style={{ display: 'flex', alignItems: 'center', marginBottom: '8px' }}>
            <AlertTriangle style={{ width: '20px', height: '20px', marginRight: '8px' }} />
            <strong>Markdown Rendering Error</strong>
          </div>
          <p style={{ margin: '0 0 12px 0', fontSize: '14px' }}>
            Unable to render this document's content. The markdown format may be invalid.
          </p>
          <div style={{ display: 'flex', gap: '8px' }}>
            <button
              onClick={() => this.setState({ showRaw: true })}
              style={{
                padding: '6px 12px',
                fontSize: '12px',
                backgroundColor: '#dc2626',
                color: 'white',
                border: 'none',
                borderRadius: '4px',
                cursor: 'pointer'
              }}
            >
              Show Raw Content
            </button>
            <details style={{ margin: 0 }}>
              <summary style={{ cursor: 'pointer', fontSize: '12px', padding: '6px 0' }}>Error details</summary>
              <pre style={{ 
                fontSize: '11px', 
                marginTop: '8px', 
                padding: '8px', 
                backgroundColor: '#fff', 
                border: '1px solid #e5e7eb',
                borderRadius: '4px',
                overflow: 'auto',
                maxHeight: '100px'
              }}>
                {this.state.error?.toString()}
              </pre>
            </details>
          </div>
        </div>
      );
    }

    return this.props.children;
  }
}

export default function DocumentViewer() {
  const { currentDocument, clearCurrentDocument, deleteDocument } = useStore();
  const [copied, setCopied] = useState(false);
  const [showEditor, setShowEditor] = useState(false);
  const [deleteConfirm, setDeleteConfirm] = useState(false);

  if (!currentDocument) return null;

  // Show loading state if document doesn't have content yet
  if (!currentDocument.contentMd) {
    return (
      <div style={{ height: '100%', display: 'flex', flexDirection: 'column', backgroundColor: 'white' }}>
        <div style={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
          <div style={{ textAlign: 'center' }}>
            <Loader2 style={{ width: '48px', height: '48px', color: '#2563eb', animation: 'spin 1s linear infinite', margin: '0 auto 16px' }} />
            <p style={{ color: '#6b7280', fontSize: '16px' }}>Loading document content...</p>
            <p style={{ color: '#9ca3af', fontSize: '12px', marginTop: '8px' }}>Document: {currentDocument.key} ({currentDocument.lang})</p>
          </div>
        </div>
      </div>
    );
  }

  const handleCopy = () => {
    navigator.clipboard.writeText(currentDocument.contentMd);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  const handleSaveComplete = () => {
    setShowEditor(false);
    // Optionally refresh the document list here
  };

  const handleDelete = () => {
    setDeleteConfirm(true);
  };

  const confirmDelete = async () => {
    try {
      await deleteDocument(currentDocument.collection, currentDocument.key, currentDocument.lang);
      clearCurrentDocument();
      setDeleteConfirm(false);
    } catch (error) {
      console.error('Failed to delete document:', error);
      // You could add error handling here (toast, alert, etc.)
    }
  };

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', backgroundColor: 'white' }}>
      {/* Header */}
      <div style={{ borderBottom: '1px solid #e5e7eb', padding: '16px' }}>
        <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between' }}>
          <div style={{ flex: 1 }}>
            <h3 style={{ fontSize: '18px', fontWeight: '600', color: '#111827', marginBottom: '4px' }}>
              {currentDocument.key}
            </h3>
            <div style={{ display: 'flex', alignItems: 'center', gap: '16px', fontSize: '14px', color: '#6b7280' }}>
              <span style={{ display: 'flex', alignItems: 'center', gap: '4px' }}>
                <FileText style={{ width: '16px', height: '16px' }} />
                <span>{currentDocument.lang}</span>
              </span>
              {currentDocument.updatedAt && (
                <span style={{ display: 'flex', alignItems: 'center', gap: '4px' }}>
                  <Calendar style={{ width: '16px', height: '16px' }} />
                  <span>{format(new Date(currentDocument.updatedAt), 'PPpp')}</span>
                </span>
              )}
            </div>
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
            <button
              onClick={handleDelete}
              style={{ padding: '8px', color: '#9ca3af', borderRadius: '8px', border: 'none', cursor: 'pointer', backgroundColor: 'transparent' }}
              onMouseOver={(e) => { e.target.style.color = '#dc2626'; e.target.style.backgroundColor = '#fef2f2'; }}
              onMouseOut={(e) => { e.target.style.color = '#9ca3af'; e.target.style.backgroundColor = 'transparent'; }}
              title="Delete document"
            >
              <Trash2 style={{ width: '20px', height: '20px' }} />
            </button>
            <button
              onClick={clearCurrentDocument}
              style={{ padding: '8px', color: '#9ca3af', borderRadius: '8px', border: 'none', cursor: 'pointer', backgroundColor: 'transparent' }}
              onMouseOver={(e) => { e.target.style.color = '#4b5563'; e.target.style.backgroundColor = '#f3f4f6'; }}
              onMouseOut={(e) => { e.target.style.color = '#9ca3af'; e.target.style.backgroundColor = 'transparent'; }}
            >
              <X style={{ width: '20px', height: '20px' }} />
            </button>
          </div>
        </div>
      </div>

      {/* Metadata */}
      {currentDocument.meta && Object.keys(currentDocument.meta).length > 0 && (
        <div style={{ borderBottom: '1px solid #e5e7eb', padding: '16px', backgroundColor: '#f9fafb' }}>
          <h4 style={{ fontSize: '12px', fontWeight: '600', color: '#6b7280', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: '12px', display: 'flex', alignItems: 'center', gap: '8px' }}>
            <Tag style={{ width: '16px', height: '16px' }} />
            <span>Metadata</span>
          </h4>
          <div style={{ display: 'flex', flexDirection: 'column', gap: '8px' }}>
            {Object.entries(currentDocument.meta).map(([key, values]) => (
              <div key={key} style={{ display: 'flex', alignItems: 'flex-start', gap: '8px' }}>
                <span style={{ fontSize: '12px', fontWeight: '500', color: '#374151', minWidth: '80px' }}>
                  {key}:
                </span>
                <div style={{ fontSize: '14px', color: '#111827' }}>
                  {Array.isArray(values) ? values.join(', ') : values}
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Content */}
      <div style={{ flex: 1, overflowY: 'auto', padding: '16px' }}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '16px' }}>
          <h4 style={{ fontSize: '12px', fontWeight: '600', color: '#6b7280', textTransform: 'uppercase', letterSpacing: '0.05em' }}>
            Content
          </h4>
          <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
            <button
              onClick={() => setShowEditor(true)}
              style={{
                display: 'flex', alignItems: 'center', gap: '8px',
                padding: '6px 12px', fontSize: '14px',
                backgroundColor: '#2563eb', color: 'white',
                borderRadius: '8px', border: 'none', cursor: 'pointer'
              }}
            >
              <Edit style={{ width: '16px', height: '16px' }} />
              <span>Edit</span>
            </button>
            <button
              onClick={handleCopy}
              style={{
                display: 'flex', alignItems: 'center', gap: '8px',
                padding: '6px 12px', fontSize: '14px',
                backgroundColor: '#f3f4f6', color: '#374151',
                borderRadius: '8px', border: 'none', cursor: 'pointer'
              }}
            >
              {copied ? (
                <>
                  <Check style={{ width: '16px', height: '16px' }} />
                  <span>Copied!</span>
                </>
              ) : (
                <>
                  <Copy style={{ width: '16px', height: '16px' }} />
                  <span>Copy</span>
                </>
              )}
            </button>
          </div>
        </div>
        
        <div style={{ backgroundColor: 'white', borderRadius: '8px', padding: '16px', border: '1px solid #e5e7eb', margin: '0', maxWidth: '100%', overflow: 'hidden' }}>
          <ErrorBoundary>
            <ReactMarkdown
              remarkPlugins={[remarkGfm]}
              rehypePlugins={[rehypeRaw, rehypeSanitize]}
              components={{
                code: ({ node, inline, className, children, ...props }) => {
                  const match = /language-(\w+)/.exec(className || '');
                  const language = match ? match[1] : '';
                  
                  return !inline && language ? (
                    <SyntaxHighlighter
                      style={vscDarkPlus}
                      language={language}
                      PreTag="div"
                      customStyle={{ margin: '16px 0', borderRadius: '8px', maxWidth: '100%', overflowX: 'auto' }}
                      {...props}
                    >
                      {String(children).replace(/\n$/, '')}
                    </SyntaxHighlighter>
                  ) : inline ? (
                    <code style={{ backgroundColor: '#f3f4f6', color: '#dc2626', padding: '2px 6px', borderRadius: '4px', fontSize: '14px', fontFamily: 'monospace' }} {...props}>
                      {children}
                    </code>
                  ) : (
                    <code style={{ display: 'block', backgroundColor: '#111827', color: '#f9fafb', padding: '16px', borderRadius: '8px', overflowX: 'auto', fontSize: '14px', fontFamily: 'monospace', margin: '16px 0', maxWidth: '100%', wordWrap: 'break-word' }} {...props}>
                      {children}
                    </code>
                  );
                },
              }}
            >
              {currentDocument.contentMd}
            </ReactMarkdown>
          </ErrorBoundary>
        </div>
      </div>

      {/* Footer Info */}
      <div style={{ borderTop: '1px solid #e5e7eb', padding: '16px', backgroundColor: '#f9fafb' }}>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: '16px', fontSize: '12px' }}>
          <div>
            <span style={{ color: '#6b7280' }}>Collection:</span>
            <span style={{ marginLeft: '8px', fontWeight: '500', color: '#111827' }}>
              {currentDocument.collection || 'N/A'}
            </span>
          </div>
          <div>
            <span style={{ color: '#6b7280' }}>Added:</span>
            <span style={{ marginLeft: '8px', fontWeight: '500', color: '#111827' }}>
              {currentDocument.addedAt 
                ? format(new Date(currentDocument.addedAt), 'PP')
                : 'N/A'}
            </span>
          </div>
          <div>
            <span style={{ color: '#6b7280' }}>Revision:</span>
            <span style={{ marginLeft: '8px', fontWeight: '500', color: '#111827' }}>
              {currentDocument.revision || 0}
            </span>
          </div>
        </div>
      </div>

      {/* Editor Modal */}
      {showEditor && (
        <DocumentEditor
          document={currentDocument}
          onClose={() => setShowEditor(false)}
          onSave={handleSaveComplete}
        />
      )}

      {/* Delete Confirmation Modal */}
      {deleteConfirm && (
        <div style={{ 
          position: 'fixed', top: 0, left: 0, right: 0, bottom: 0,
          backgroundColor: 'rgba(0, 0, 0, 0.5)', display: 'flex',
          alignItems: 'center', justifyContent: 'center', zIndex: 50
        }}>
          <div style={{ 
            backgroundColor: 'white', borderRadius: '8px', padding: '24px',
            maxWidth: '400px', width: '90%', margin: '0 16px'
          }}>
            <div style={{ display: 'flex', alignItems: 'center', marginBottom: '16px' }}>
              <Trash2 style={{ width: '24px', height: '24px', color: '#dc2626', marginRight: '12px' }} />
              <h3 style={{ fontSize: '18px', fontWeight: '600', color: '#111827', margin: 0 }}>
                Delete Document
              </h3>
            </div>
            <p style={{ color: '#6b7280', marginBottom: '24px', lineHeight: '1.5' }}>
              Are you sure you want to delete "{currentDocument.key}" ({currentDocument.lang})? This action cannot be undone.
            </p>
            <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '12px' }}>
              <button
                onClick={() => setDeleteConfirm(false)}
                style={{
                  padding: '8px 16px', color: '#374151', backgroundColor: '#f3f4f6',
                  borderRadius: '6px', border: 'none', cursor: 'pointer', fontSize: '14px'
                }}
              >
                Cancel
              </button>
              <button
                onClick={confirmDelete}
                style={{
                  padding: '8px 16px', color: 'white', backgroundColor: '#dc2626',
                  borderRadius: '6px', border: 'none', cursor: 'pointer', fontSize: '14px'
                }}
              >
                Delete
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
