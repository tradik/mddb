/**
 * Global state management with Zustand
 */
import { create } from 'zustand';

export const useStore = create((set, get) => ({
  // Server stats
  stats: null,
  statsLoading: false,
  statsError: null,

  // Current collection
  currentCollection: null,
  
  // Documents
  documents: [],
  documentsLoading: false,
  documentsError: null,
  
  // Current document
  currentDocument: null,
  currentDocumentLoading: false,
  currentDocumentError: null,
  
  // Filters
  filters: {},
  sortBy: 'addedAt',
  sortAsc: false,
  limit: 100,

  // Actions
  setStats: (stats) => set({ stats }),
  setStatsLoading: (loading) => set({ statsLoading: loading }),
  setStatsError: (error) => set({ statsError: error }),

  setCurrentCollection: (collection) => set({ currentCollection: collection }),
  
  setDocuments: (documents) => set({ documents }),
  setDocumentsLoading: (loading) => set({ documentsLoading: loading }),
  setDocumentsError: (error) => set({ documentsError: error }),
  
  setCurrentDocument: (doc) => set({ currentDocument: doc }),
  setCurrentDocumentLoading: (loading) => set({ currentDocumentLoading: loading }),
  setCurrentDocumentError: (error) => set({ currentDocumentError: error }),
  
  setFilters: (filters) => set({ filters }),
  setSortBy: (sortBy) => set({ sortBy }),
  setSortAsc: (asc) => set({ sortAsc: asc }),
  setLimit: (limit) => set({ limit }),

  // Clear current document
  clearCurrentDocument: () => set({ 
    currentDocument: null, 
    currentDocumentError: null 
  }),

  // Delete document
  deleteDocument: async (collection, key, lang) => {
    try {
      const mddbClient = await import('../lib/mddb-client').then(m => m.default);
      await mddbClient.deleteDocument({ collection, key, lang });
      
      // Remove from documents list
      const { documents, currentDocument } = get();
      const updatedDocuments = documents.filter(doc => 
        !(doc.key === key && doc.lang === lang)
      );
      set({ documents: updatedDocuments });
      
      // Clear current document if it was the deleted one
      if (currentDocument?.key === key && currentDocument?.lang === lang) {
        set({ currentDocument: null });
      }
      
      return true;
    } catch (error) {
      console.error('Failed to delete document:', error);
      throw error;
    }
  },
}));
