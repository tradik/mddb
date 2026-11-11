import { useState } from 'react';
import { Copy, Check } from 'lucide-react';

/**
 * OneTimeSecret shows a newly created API key — the only time the server
 * returns it — with a copy button and a Dismiss. Used by the MCP keys and
 * the personal API keys; "Copied" is announced to screen readers.
 */
export default function OneTimeSecret({ secret, onDismiss }) {
  const [copied, setCopied] = useState(false);

  const copy = async () => {
    await navigator.clipboard.writeText(secret);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div className="p-3 bg-green-50 border border-green-300 rounded-lg">
      <p className="text-sm font-medium text-green-900 mb-1">Key created. Copy it now — it will not be shown again.</p>
      <div className="flex items-center space-x-2">
        <code className="flex-1 px-2 py-1 bg-white border rounded text-sm font-mono select-all break-all">{secret}</code>
        <button
          type="button"
          onClick={copy}
          aria-label="Copy key"
          className="p-1.5 text-green-800 hover:text-green-950"
        >
          {copied ? <Check className="w-4 h-4" aria-hidden="true" /> : <Copy className="w-4 h-4" aria-hidden="true" />}
        </button>
      </div>
      <p role="status" className="text-xs text-green-900 mt-1">{copied ? 'Copied to the clipboard.' : ''}</p>
      <button type="button" onClick={onDismiss} className="text-xs text-green-900 underline">Dismiss</button>
    </div>
  );
}
