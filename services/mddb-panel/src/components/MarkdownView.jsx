import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import rehypeRaw from 'rehype-raw';
import rehypeSanitize from 'rehype-sanitize';
import { Prism as SyntaxHighlighter } from 'react-syntax-highlighter';
import { vscDarkPlus } from 'react-syntax-highlighter/dist/esm/styles/prism';

/**
 * MarkdownView renders a document's Markdown with the panel's styling: GFM,
 * raw HTML passed through the sanitizer, highlighted fenced code. The editor
 * preview and the document viewer both render through it, so a heading looks
 * the same in both (2.19.6: the viewer used to show headings as plain text).
 */

/**
 * CodeBlock styles the three shapes of `code`. react-markdown 9+ no longer
 * says whether code is inline: a fenced block carries a language class or
 * ends in a newline, inline code does neither.
 */
function CodeBlock({ node, className, children, ...props }) {
  const text = String(children);
  const language = /language-(\w+)/.exec(className || '')?.[1];
  if (language) {
    return (
      <SyntaxHighlighter style={vscDarkPlus} language={language} PreTag="div" className="my-4 rounded-lg max-w-full overflow-x-auto" {...props}>
        {text.replace(/\n$/, '')}
      </SyntaxHighlighter>
    );
  }
  if (text.includes('\n')) {
    return (
      <code className="block bg-gray-900 text-gray-100 p-4 rounded-lg overflow-x-auto text-sm font-mono my-4 whitespace-pre" {...props}>
        {children}
      </code>
    );
  }
  return (
    <code className="bg-gray-100 text-red-700 px-1.5 py-0.5 rounded text-sm font-mono break-words" {...props}>
      {children}
    </code>
  );
}

/** styled returns a renderer of `tag` with `className`, dropping the hast node. */
function styled(Tag, className) {
  function Styled({ node, ...props }) {
    return <Tag className={className} {...props} />;
  }
  Styled.displayName = `Markdown.${Tag}`;
  return Styled;
}

function Table({ node, ...props }) {
  return (
    <div className="overflow-x-auto my-4">
      <table className="min-w-full divide-y divide-gray-200 border border-gray-200" {...props} />
    </div>
  );
}

const components = {
  h1: styled('h1', 'text-3xl font-bold mb-4 mt-6 text-gray-900'),
  h2: styled('h2', 'text-2xl font-bold mb-3 mt-5 text-gray-900'),
  h3: styled('h3', 'text-xl font-bold mb-2 mt-4 text-gray-900'),
  h4: styled('h4', 'text-lg font-semibold mb-2 mt-3 text-gray-900'),
  p: styled('p', 'mb-4 text-gray-700 leading-relaxed'),
  a: styled('a', 'text-primary-700 hover:text-primary-800 underline break-words'),
  ul: styled('ul', 'list-disc list-inside mb-4 space-y-1'),
  ol: styled('ol', 'list-decimal list-inside mb-4 space-y-1'),
  li: styled('li', 'text-gray-700'),
  blockquote: styled('blockquote', 'border-l-4 border-gray-300 pl-4 italic my-4 text-gray-600'),
  code: CodeBlock,
  pre: styled('div', 'max-w-full'),
  table: Table,
  thead: styled('thead', 'bg-gray-50'),
  th: styled('th', 'px-4 py-2 text-left text-xs font-medium text-gray-600 uppercase tracking-wider'),
  td: styled('td', 'px-4 py-2 text-sm text-gray-700 border-t border-gray-200'),
  hr: styled('hr', 'my-6 border-gray-300'),
  img: styled('img', 'max-w-full h-auto rounded-lg my-4'),
};

const remarkPlugins = [remarkGfm];
const rehypePlugins = [rehypeRaw, rehypeSanitize];

export default function MarkdownView({ children }) {
  return (
    <div className="min-w-0 break-words">
      <ReactMarkdown remarkPlugins={remarkPlugins} rehypePlugins={rehypePlugins} components={components}>
        {children}
      </ReactMarkdown>
    </div>
  );
}
