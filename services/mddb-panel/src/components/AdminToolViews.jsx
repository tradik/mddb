import MemoryPanel from './MemoryPanel';
import SchemasPanel from './SchemasPanel';

/** The pages added in 2.19.0, by view mode. */
const PAGES = {
  memory: MemoryPanel,
  schemas: SchemasPanel,
};

/**
 * AdminToolViews renders the page of a view mode added in 2.19.0 in the main
 * area, or nothing for any other mode.
 */
export default function AdminToolViews({ viewMode }) {
  const Page = PAGES[viewMode];
  if (!Page) return null;
  return (
    <div className="flex-1 border-l border-gray-200">
      <Page />
    </div>
  );
}
