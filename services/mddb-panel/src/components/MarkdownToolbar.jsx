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

export default function MarkdownToolbar({ onInsert, onTemplate }) {
  const insertMarkdown = (before, after = '', placeholder = '') => {
    if (onInsert) {
      onInsert(before, after, placeholder);
    }
  };

  const buttons = [
    {
      icon: Heading1,
      title: 'Heading 1',
      action: () => insertMarkdown('# ', '', 'Heading 1'),
    },
    {
      icon: Heading2,
      title: 'Heading 2',
      action: () => insertMarkdown('## ', '', 'Heading 2'),
    },
    {
      icon: Heading3,
      title: 'Heading 3',
      action: () => insertMarkdown('### ', '', 'Heading 3'),
    },
    { divider: true },
    {
      icon: Bold,
      title: 'Bold',
      action: () => insertMarkdown('**', '**', 'bold text'),
    },
    {
      icon: Italic,
      title: 'Italic',
      action: () => insertMarkdown('*', '*', 'italic text'),
    },
    {
      icon: Strikethrough,
      title: 'Strikethrough',
      action: () => insertMarkdown('~~', '~~', 'strikethrough text'),
    },
    {
      icon: Code,
      title: 'Inline Code',
      action: () => insertMarkdown('`', '`', 'code'),
    },
    { divider: true },
    {
      icon: Link,
      title: 'Link',
      action: () => insertMarkdown('[', '](url)', 'link text'),
    },
    {
      icon: List,
      title: 'Bullet List',
      action: () => insertMarkdown('- ', '', 'list item'),
    },
    {
      icon: ListOrdered,
      title: 'Numbered List',
      action: () => insertMarkdown('1. ', '', 'list item'),
    },
    {
      icon: CheckSquare,
      title: 'Task List',
      action: () => insertMarkdown('- [ ] ', '', 'task item'),
    },
    { divider: true },
    {
      icon: Quote,
      title: 'Blockquote',
      action: () => insertMarkdown('> ', '', 'quote'),
    },
    {
      icon: Table,
      title: 'Table',
      action: () => insertMarkdown(
        '| Header 1 | Header 2 |\n| -------- | -------- |\n| Cell 1   | Cell 2   |\n',
        '',
        ''
      ),
    },
  ];

  return (
    <div className="flex items-center space-x-1 px-2 py-1 bg-gray-50 border-b border-gray-200 flex-wrap">
      {/* Template Dropdown */}
      {onTemplate && (
        <>
          <div className="relative group">
            <button
              className="p-1.5 text-gray-600 hover:bg-gray-200 rounded transition-colors flex items-center space-x-1"
              title="Templates"
            >
              <FileText className="w-4 h-4" />
              <span className="text-xs">Templates</span>
            </button>
            <div className="absolute left-0 top-full mt-1 bg-white border border-gray-200 rounded-lg shadow-lg hidden group-hover:block z-10 min-w-[200px]">
              <button
                onClick={() => onTemplate('blog')}
                className="w-full text-left px-4 py-2 text-sm hover:bg-gray-100 transition-colors"
              >
                📝 Blog Post
              </button>
              <button
                onClick={() => onTemplate('documentation')}
                className="w-full text-left px-4 py-2 text-sm hover:bg-gray-100 transition-colors"
              >
                📚 Documentation
              </button>
              <button
                onClick={() => onTemplate('readme')}
                className="w-full text-left px-4 py-2 text-sm hover:bg-gray-100 transition-colors"
              >
                📄 README
              </button>
              <button
                onClick={() => onTemplate('api')}
                className="w-full text-left px-4 py-2 text-sm hover:bg-gray-100 transition-colors"
              >
                🔌 API Documentation
              </button>
              <button
                onClick={() => onTemplate('changelog')}
                className="w-full text-left px-4 py-2 text-sm hover:bg-gray-100 transition-colors"
              >
                📋 Changelog
              </button>
            </div>
          </div>
          <div className="w-px h-6 bg-gray-300" />
        </>
      )}

      {/* Formatting Buttons */}
      {buttons.map((button, index) => {
        if (button.divider) {
          return <div key={`divider-${index}`} className="w-px h-6 bg-gray-300" />;
        }

        const Icon = button.icon;
        return (
          <button
            key={index}
            onClick={button.action}
            className="p-1.5 text-gray-600 hover:bg-gray-200 rounded transition-colors"
            title={button.title}
          >
            <Icon className="w-4 h-4" />
          </button>
        );
      })}
    </div>
  );
}
