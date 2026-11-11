import MemoryPanel from './MemoryPanel';
import SchemasPanel from './SchemasPanel';
import ImportPanel from './ImportPanel';
import DuplicatesPanel from './DuplicatesPanel';
import AccountPanel from './AccountPanel';
import DocumentViewer from './DocumentViewer';

/** The pages added in 2.19.0, by view mode. */
const PAGES = {
  memory: MemoryPanel,
  schemas: SchemasPanel,
  import: ImportPanel,
  duplicates: DuplicatesPanel,
  account: AccountPanel,
};

/** Pages whose documents open in the viewer beside them. */
const WITH_VIEWER = new Set(['duplicates']);

/**
 * AdminToolViews renders the page of a view mode added in 2.19.0 in the main
 * area — with the document viewer beside it when the page opened a document
 * — or nothing for any other mode.
 */
export default function AdminToolViews({ viewMode, currentDocument }) {
  const Page = PAGES[viewMode];
  if (!Page) return null;
  return (
    <>
      <div className="flex-1 border-l border-gray-200">
        <Page />
      </div>
      {WITH_VIEWER.has(viewMode) && currentDocument && (
        <div className="flex-1 border-l border-gray-200">
          <DocumentViewer />
        </div>
      )}
    </>
  );
}
