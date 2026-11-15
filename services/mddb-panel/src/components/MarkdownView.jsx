import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import rehypeRaw from 'rehype-raw';
import rehypeSanitize from 'rehype-sanitize';
import { createContext, useContext } from 'react';
import { Prism as SyntaxHighlighter } from 'react-syntax-highlighter';
import { vscDarkPlus } from 'react-syntax-highlighter/dist/esm/styles/prism';
import a11yOneLight from 'react-syntax-highlighter/dist/esm/styles/prism/a11y-one-light';
import { useTheme } from '../lib/theme';

/**
 * MarkdownView renders a document's Markdown with the panel's styling: GFM,
 * raw HTML passed through the sanitizer, highlighted fenced code. The editor
 * preview and the document viewer both render through it, so a heading looks
 * the same in both (2.19.6: the viewer used to show headings as plain text).
 */

/**
 * The code highlighting theme of each mode (FE-015) — the one place it is
 * chosen: an accessible light theme on light pages, vscDarkPlus on dark ones.
 */
const CODE_THEMES = { light: a11yOneLight, dark: vscDarkPlus };

/** The mode the code blocks below render in; MarkdownView provides it. */
const SchemeContext = createContext('light');

/**
 * CodeBlock styles the three shapes of `code`. react-markdown 9+ no longer
 * says whether code is inline: a fenced block carries a language class or
 * ends in a newline, inline code does neither.
 */
function CodeBlock({ node, className, children, ...props }) {
  const text = String(children);
  const scheme = useContext(SchemeContext);
  const language = /language-(\w+)/.exec(className || '')?.[1];
  if (language) {
    return (
      <SyntaxHighlighter style={CODE_THEMES[scheme]} language={language} PreTag="div" className="my-4 rounded-lg max-w-full overflow-x-auto border border-line" {...props}>
        {text.replace(/\n$/, '')}
      </SyntaxHighlighter>
    );
  }
  if (text.includes('\n')) {
    return (
      <code className="block bg-surface-subtle text-fg border border-line p-4 rounded-lg overflow-x-auto text-sm font-mono my-4 whitespace-pre" {...props}>
        {children}
      </code>
    );
  }
  return (
    <code className="bg-surface-subtle text-danger px-1.5 py-0.5 rounded text-sm font-mono break-words" {...props}>
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
      <table className="min-w-full divide-y divide-line border border-line" {...props} />
    </div>
  );
}

const components = {
  h1: styled('h1', 'text-3xl font-bold mb-4 mt-6 text-fg'),
  h2: styled('h2', 'text-2xl font-bold mb-3 mt-5 text-fg'),
  h3: styled('h3', 'text-xl font-bold mb-2 mt-4 text-fg'),
  h4: styled('h4', 'text-lg font-semibold mb-2 mt-3 text-fg'),
  p: styled('p', 'mb-4 text-fg-muted leading-relaxed'),
  a: styled('a', 'text-accent hover:text-accent-strong underline break-words'),
  ul: styled('ul', 'list-disc list-inside mb-4 space-y-1'),
  ol: styled('ol', 'list-decimal list-inside mb-4 space-y-1'),
  li: styled('li', 'text-fg-muted'),
  blockquote: styled('blockquote', 'border-l-4 border-line-strong pl-4 italic my-4 text-fg-subtle'),
  code: CodeBlock,
  pre: styled('div', 'max-w-full'),
  table: Table,
  thead: styled('thead', 'bg-surface-muted'),
  th: styled('th', 'px-4 py-2 text-left text-xs font-medium text-fg-subtle uppercase tracking-wider'),
  td: styled('td', 'px-4 py-2 text-sm text-fg-muted border-t border-line'),
  hr: styled('hr', 'my-6 border-line-strong'),
  img: styled('img', 'max-w-full h-auto rounded-lg my-4'),
};

const remarkPlugins = [remarkGfm];
const rehypePlugins = [rehypeRaw, rehypeSanitize];

/**
 * `scheme` forces a mode ("light" for the print view and the HTML export,
 * which are always light); without it the code follows the panel's theme.
 */
export default function MarkdownView({ children, scheme }) {
  const { resolved } = useTheme();
  return (
    <SchemeContext.Provider value={scheme || resolved}>
      <div className="min-w-0 break-words">
        <ReactMarkdown remarkPlugins={remarkPlugins} rehypePlugins={rehypePlugins} components={components}>
          {children}
        </ReactMarkdown>
      </div>
    </SchemeContext.Provider>
  );
}
