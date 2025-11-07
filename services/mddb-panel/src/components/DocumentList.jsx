import { useEffect, useState } from 'react';
import { FileText, Calendar, Tag, Trash2, X } from 'lucide-react';
import { format } from 'date-fns';
import { useStore } from '../lib/store';
import mddbClient from '../lib/mddb-client';

export default function DocumentList() {
  const [deleteConfirm, setDeleteConfirm] = useState(null);
  const {
    currentCollection,
    documents,
    documentsLoading,
    documentsError,
    setDocuments,
    setDocumentsLoading,
    setDocumentsError,
    currentDocument,
    setCurrentDocument,
    filters,
    sortBy,
    sortAsc,
    limit,
    deleteDocument,
  } = useStore();

  useEffect(() => {
    if (currentCollection) {
      loadDocuments();
    }
  }, [currentCollection, filters, sortBy, sortAsc, limit]);

  const loadDocuments = async () => {
    setDocumentsLoading(true);
    setDocumentsError(null);
    try {
      const data = await mddbClient.search({
        collection: currentCollection,
        filterMeta: filters,
        sort: sortBy,
        asc: sortAsc,
        limit,
      });
      // API returns array directly, not { documents: [...] }
      // Add collection field to each document for editing
      const documentsWithCollection = Array.isArray(data) 
        ? data.map(doc => ({ ...doc, collection: currentCollection }))
        : [];
      setDocuments(documentsWithCollection);
    } catch (error) {
      // Handle corrupted data errors
      if (error.message.includes('invalid character')) {
        setDocumentsError('Collection contains corrupted data. This collection may need to be recreated.');
      } else {
        setDocumentsError(error.message);
      }
      console.error('Failed to load documents:', error);
      setDocuments([]);
    } finally {
      setDocumentsLoading(false);
    }
  };

  const handleDocumentClick = async (doc) => {
    // Set the document immediately with basic info
    const initialDocument = {
      ...doc,
      collection: currentCollection,
      contentMd: doc.contentMd || 'Loading content...'
    };
    
    setCurrentDocument(initialDocument);
    
    // Then try to fetch full content in background
    try {
      const fullDocument = await mddbClient.getDocument({
        collection: currentCollection,
        key: doc.key,
        lang: doc.lang
      });
      
      // Update with full content if different
      if (fullDocument.contentMd && fullDocument.contentMd !== doc.contentMd) {
        const documentWithCollection = {
          ...fullDocument,
          collection: currentCollection
        };
        setCurrentDocument(documentWithCollection);
      }
    } catch (error) {
      console.error('Failed to load full document content:', error);
      // Update with error message
      const errorDocument = {
        ...initialDocument,
        contentMd: `Error loading document content: ${error.message}. Please try again.`
      };
      setCurrentDocument(errorDocument);
    }
  };

  const handleDelete = async (doc, e) => {
    e.stopPropagation(); // Prevent document click
    setDeleteConfirm(doc);
  };

  const confirmDelete = async (doc) => {
    try {
      await deleteDocument(currentCollection, doc.key, doc.lang);
      setDeleteConfirm(null);
    } catch (error) {
      console.error('Failed to delete document:', error);
      // You could add error handling here (toast, alert, etc.)
    }
  };

  if (documentsLoading) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="text-center">
          <div className="animate-spin rounded-full h-12 w-12 border-b-2 border-primary-600 mx-auto mb-4"></div>
          <p className="text-gray-500">Loading documents...</p>
        </div>
      </div>
    );
  }

  if (documentsError) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="text-center">
          <p className="text-red-600 mb-2">Error loading documents</p>
          <p className="text-sm text-gray-500">{documentsError}</p>
        </div>
      </div>
    );
  }

  return (
    <div className="h-full flex flex-col">
      <div className="p-4 border-b border-gray-200">
        <div className="flex items-center justify-between">
          <h3 className="text-sm font-semibold text-gray-700">
            {documents.length} Documents
          </h3>
          <button
            onClick={loadDocuments}
            className="text-xs text-primary-600 hover:text-primary-700"
          >
            Refresh
          </button>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto">
        {documents.length === 0 ? (
          <div className="flex items-center justify-center h-full">
            <div className="text-center">
              <FileText className="w-12 h-12 text-gray-400 mx-auto mb-2" />
              <p className="text-gray-500">No documents found</p>
            </div>
          </div>
        ) : (
          <div className="divide-y divide-gray-200">
            {documents.map((doc) => (
              <button
                key={`${doc.key}-${doc.lang}`}
                onClick={() => handleDocumentClick(doc)}
                className={`w-full text-left p-4 hover:bg-gray-50 transition-colors ${
                  currentDocument?.key === doc.key && currentDocument?.lang === doc.lang
                    ? 'bg-primary-50 border-l-4 border-primary-600'
                    : ''
                }`}
              >
                <div className="flex items-start justify-between mb-2">
                  <h4 className="font-medium text-gray-900 truncate flex-1">
                    {doc.key}
                  </h4>
                  <div className="flex items-center gap-2">
                    <span className="text-xs text-gray-500">{doc.lang}</span>
                    <button
                      onClick={(e) => handleDelete(doc, e)}
                      className="p-1 text-gray-400 hover:text-red-600 hover:bg-red-50 rounded transition-colors"
                      title="Delete document"
                    >
                      <Trash2 className="w-4 h-4" />
                    </button>
                  </div>
                </div>
                
                <div className="flex items-center space-x-3 text-xs text-gray-500">
                  <div className="flex items-center space-x-1">
                    <Calendar className="w-3 h-3" />
                    <span>
                      {doc.updatedAt ? format(new Date(doc.updatedAt), 'MMM d, yyyy') : 'N/A'}
                    </span>
                  </div>
                  {doc.meta && Object.keys(doc.meta).length > 0 && (
                    <div className="flex items-center space-x-1">
                      <Tag className="w-3 h-3" />
                      <span>{Object.keys(doc.meta).length} tags</span>
                    </div>
                  )}
                </div>

                {doc.meta && Object.entries(doc.meta).slice(0, 2).map(([key, values]) => (
                  <div key={key} className="mt-2">
                    <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-gray-100 text-gray-800">
                      {key}: {Array.isArray(values) ? values.join(', ') : values}
                    </span>
                  </div>
                ))}
              </button>
            ))}
          </div>
        )}
      </div>

      {/* Delete Confirmation Modal */}
      {deleteConfirm && (
        <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50">
          <div className="bg-white rounded-lg p-6 max-w-sm mx-4">
            <div className="flex items-center mb-4">
              <Trash2 className="w-6 h-6 text-red-600 mr-3" />
              <h3 className="text-lg font-semibold text-gray-900">Delete Document</h3>
            </div>
            <p className="text-gray-600 mb-6">
              Are you sure you want to delete "{deleteConfirm.key}" ({deleteConfirm.lang})? This action cannot be undone.
            </p>
            <div className="flex justify-end gap-3">
              <button
                onClick={() => setDeleteConfirm(null)}
                className="px-4 py-2 text-gray-700 bg-gray-100 hover:bg-gray-200 rounded-lg transition-colors"
              >
                Cancel
              </button>
              <button
                onClick={() => confirmDelete(deleteConfirm)}
                className="px-4 py-2 text-white bg-red-600 hover:bg-red-700 rounded-lg transition-colors"
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
