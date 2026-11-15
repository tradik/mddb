import { Fragment } from 'react';
import {
  Bold,
  Italic,
  Strikethrough,
  Code,
  Link,
  List,
  ListOrdered,
  Quote,
  Heading1,
  Heading2,
  Heading3,
  Table,
  CheckSquare,
  FileText
} from 'lucide-react';
import { formatActions } from '../lib/markdown-format';
import { actionTitle } from '../lib/editor-shortcuts';

/** The icon of each format action of markdown-format.js. */
const icons = {
  h1: Heading1,
  h2: Heading2,
  h3: Heading3,
  bold: Bold,
  italic: Italic,
  strike: Strikethrough,
  code: Code,
  link: Link,
  ul: List,
  ol: ListOrdered,
  task: CheckSquare,
  quote: Quote,
  table: Table,
};

/**
 * MarkdownToolbar: the templates menu and one button per format action.
 * A button calls `onFormat(actionId)` — the entry point the editor's
 * keyboard shortcuts use too (FE-016) — and its tooltip names the shortcut.
 */
export default function MarkdownToolbar({ onFormat, onTemplate, isMac = false }) {
  return (
    <div className="flex items-center space-x-1 px-2 py-1 bg-surface-muted border-b border-line flex-wrap">
      {/* Template Dropdown */}
      {onTemplate && (
        <>
          <div className="relative group">
            <button
              className="p-1.5 text-fg-subtle hover:bg-surface-strong rounded transition-colors flex items-center space-x-1"
              title="Templates"
            >
              <FileText className="w-4 h-4" />
              <span className="text-xs">Templates</span>
            </button>
            <div className="absolute left-0 top-full mt-1 bg-surface border border-line rounded-lg shadow-lg hidden group-hover:block z-10 min-w-[200px]">
              <button
                onClick={() => onTemplate('blog')}
                className="w-full text-left px-4 py-2 text-sm hover:bg-surface-subtle transition-colors"
              >
                📝 Blog Post
              </button>
              <button
                onClick={() => onTemplate('documentation')}
                className="w-full text-left px-4 py-2 text-sm hover:bg-surface-subtle transition-colors"
              >
                📚 Documentation
              </button>
              <button
                onClick={() => onTemplate('readme')}
                className="w-full text-left px-4 py-2 text-sm hover:bg-surface-subtle transition-colors"
              >
                📄 README
              </button>
              <button
                onClick={() => onTemplate('api')}
                className="w-full text-left px-4 py-2 text-sm hover:bg-surface-subtle transition-colors"
              >
                🔌 API Documentation
              </button>
              <button
                onClick={() => onTemplate('changelog')}
                className="w-full text-left px-4 py-2 text-sm hover:bg-surface-subtle transition-colors"
              >
                📋 Changelog
              </button>
            </div>
          </div>
          <div className="w-px h-6 bg-line" />
        </>
      )}

      {/* Formatting Buttons: a divider between groups */}
      {formatActions.map((action, index) => {
        const Icon = icons[action.id];
        const title = actionTitle(action, isMac);
        return (
          <Fragment key={action.id}>
            {index > 0 && action.group !== formatActions[index - 1].group && (
              <div className="w-px h-6 bg-line" />
            )}
            <button
              type="button"
              onClick={() => onFormat?.(action.id)}
              className="p-1.5 text-fg-subtle hover:bg-surface-strong rounded transition-colors"
              title={title}
              aria-label={title}
            >
              <Icon className="w-4 h-4" aria-hidden="true" />
            </button>
          </Fragment>
        );
      })}
    </div>
  );
}
