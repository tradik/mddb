import { useEffect, useState } from 'react';
import { FileText, Calendar, Tag, Trash2, Upload, ChevronLeft, ChevronRight, Search, Terminal } from 'lucide-react';
import { format } from 'date-fns';
import { useStore } from '../lib/store';
import mddbClient from '../lib/mddb-client';
import UploadModal from './UploadModal';
import CommandModal from './CommandModal';
import ReadOnlyNotice from './ReadOnlyNotice';

export default function DocumentList({ sseRefreshKey } = {}) {
  const [deleteConfirm, setDeleteConfirm] = useState(null);
  const [showUploadModal, setShowUploadModal] = useState(false);
  const [showCommand, setShowCommand] = useState(false);
  const [offset, setOffset] = useState(0);
  const [totalCount, setTotalCount] = useState(0);
  const [filterText, setFilterText] = useState('');
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
    setStats,
    setCurrentCollection,
    collectionConfigs,
  } = useStore();
  // AGT-005: a corpus-pack collection refuses writes; its upload and delete
  // controls are off and the notice says why.
  const collectionConfig = collectionConfigs?.[currentCollection];
  const readOnly = collectionConfig?.readOnly === true;

  useEffect(() => {
    setOffset(0);
  }, [currentCollection, filters, sortBy, sortAsc, limit]);

  useEffect(() => {
    if (currentCollection) {
      loadDocuments();
    }
  }, [currentCollection, filters, sortBy, sortAsc, limit, offset, sseRefreshKey]);

  const loadDocuments = async () => {
    setDocumentsLoading(true);
    setDocumentsError(null);
    try {
      const { documents: docs, totalCount: total } = await mddbClient.search({
        collection: currentCollection,
        filterMeta: filters,
        sort: sortBy,
        asc: sortAsc,
        limit,
        offset,
      });
      const documentsWithCollection = docs.map(doc => ({ ...doc, collection: currentCollection }));
      setDocuments(documentsWithCollection);
      setTotalCount(total);
    } catch (error) {
      if (error.message.includes('invalid character')) {
        setDocumentsError('Collection contains corrupted data. This collection may need to be recreated.');
      } else {
        setDocumentsError(error.message);
      }
      console.error('Failed to load documents:', error);
      setDocuments([]);
      setTotalCount(0);
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
          <div className="animate-spin rounded-full h-12 w-12 border-b-2 border-blue-600 mx-auto mb-4"></div>
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

  const currentPage = Math.floor(offset / limit) + 1;
  const totalPages = Math.ceil(totalCount / limit);
  const rangeStart = totalCount === 0 ? 0 : offset + 1;
  const rangeEnd = Math.min(offset + limit, totalCount);

  return (
    <div className="h-full flex flex-col">
      <div className="p-4 border-b border-gray-200">
        <div className="flex items-center justify-between">
          <h3 className="text-sm font-semibold text-gray-700">
            {totalCount > 0 ? (
              <>{rangeStart}–{rangeEnd} of {totalCount.toLocaleString()}</>
            ) : (
              <>0 Documents</>
            )}
          </h3>
          <div className="flex items-center gap-2">
            <button
              onClick={() => setShowCommand(true)}
              className="px-3 py-1.5 bg-gray-100 text-gray-700 text-xs rounded hover:bg-gray-200 flex items-center gap-1.5"
            >
              <Terminal className="w-3.5 h-3.5" />
              Command
            </button>
            <button
              onClick={() => setShowUploadModal(true)}
              disabled={readOnly}
              title={readOnly ? 'Read-only collection: uploads are refused' : undefined}
              className="px-3 py-1.5 bg-blue-600 text-white text-xs rounded hover:bg-blue-700 flex items-center gap-1.5 disabled:opacity-50 disabled:cursor-not-allowed"
            >
              <Upload className="w-3.5 h-3.5" />
              Upload
            </button>
            <button
              onClick={loadDocuments}
              className="text-xs text-blue-600 hover:text-blue-700"
            >
              Refresh
            </button>
          </div>
        </div>
        <div className="relative mt-2">
          <Search className="w-3.5 h-3.5 absolute left-2.5 top-1/2 -translate-y-1/2 text-gray-400" />
          <input
            type="text"
            value={filterText}
            onChange={(e) => setFilterText(e.target.value)}
            placeholder="Filter by key..."
            className="w-full pl-8 pr-3 py-1.5 border border-gray-300 rounded text-sm focus:outline-none focus:ring-2 focus:ring-primary-500"
          />
        </div>
        {readOnly && (
          <div className="mt-2">
            <ReadOnlyNotice config={collectionConfig} compact />
          </div>
        )}
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
            {documents.filter(doc => !filterText || doc.key.toLowerCase().includes(filterText.toLowerCase())).map((doc) => (
              <button
                key={`${doc.key}-${doc.lang}`}
                onClick={() => handleDocumentClick(doc)}
                className={`w-full text-left p-4 hover:bg-gray-50 transition-colors ${
                  currentDocument?.key === doc.key && currentDocument?.lang === doc.lang
                    ? 'bg-blue-50 border-l-4 border-blue-600'
                    : ''
                }`}
              >
                <div className="flex items-start justify-between mb-2">
                  <h4 className="font-medium text-gray-900 truncate flex-1">
                    {doc.key}
                  </h4>
                  <div className="flex items-center gap-2">
                    <span className="text-xs text-gray-500">{doc.lang}</span>
                    {!readOnly && <button
                      onClick={(e) => handleDelete(doc, e)}
                      className="p-1 text-gray-400 hover:text-red-600 hover:bg-red-50 rounded transition-colors"
                      title="Delete document"
                    >
                      <Trash2 className="w-4 h-4" />
                    </button>}
                  </div>
                </div>
                
                <div className="flex items-center space-x-3 text-xs text-gray-500">
                  <div className="flex items-center space-x-1">
                    <Calendar className="w-3 h-3" />
                    <span>
                      {doc.updatedAt ? format(new Date(doc.updatedAt * 1000), 'MMM d, yyyy') : 'N/A'}
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

      {/* Pagination */}
      {totalPages > 1 && (
        <div className="p-3 border-t border-gray-200 flex items-center justify-between">
          <span className="text-xs text-gray-500">
            Page {currentPage} of {totalPages}
          </span>
          <div className="flex items-center gap-1">
            <button
              onClick={() => setOffset(Math.max(0, offset - limit))}
              disabled={offset === 0}
              className="p-1.5 rounded hover:bg-gray-100 disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
            >
              <ChevronLeft className="w-4 h-4" />
            </button>
            <button
              onClick={() => setOffset(offset + limit)}
              disabled={rangeEnd >= totalCount}
              className="p-1.5 rounded hover:bg-gray-100 disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
            >
              <ChevronRight className="w-4 h-4" />
            </button>
          </div>
        </div>
      )}

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

      {/* Upload Modal */}
      {showUploadModal && (
        <UploadModal
          collection={currentCollection}
          onClose={() => setShowUploadModal(false)}
          onSuccess={async (uploadedCollection) => {
            setShowUploadModal(false);

            // Refresh stats to show new collection in sidebar
            try {
              const stats = await mddbClient.getStats();
              setStats(stats);

              // If no collection was selected, select the uploaded collection
              if (!currentCollection && uploadedCollection) {
                setCurrentCollection(uploadedCollection);
              } else {
                // Refresh current collection's documents
                loadDocuments();
              }
            } catch (error) {
              console.error('Failed to refresh stats:', error);
              // Still try to load documents even if stats refresh fails
              loadDocuments();
            }
          }}
        />
      )}

      {/* Command Modal */}
      <CommandModal
        isOpen={showCommand}
        onClose={() => setShowCommand(false)}
        type="search"
        params={{
          collection: currentCollection,
          filterMeta: filters,
          sort: sortBy,
          asc: sortAsc,
          limit,
          offset,
        }}
      />
    </div>
  );
}
