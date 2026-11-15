import { useId } from 'react';

/**
 * JsonTextarea is a labelled, monospace textarea for JSON the operator edits
 * (a schema, metadata, a list of documents). `error` — a parse error from
 * lib/json-text — is shown under the field and tied to it with
 * aria-describedby and aria-invalid; `hint` explains the expected shape.
 */
export default function JsonTextarea({ label, value, onChange, error, hint, rows = 8, placeholder, disabled = false }) {
  const id = useId();
  const hintId = `${id}-hint`;
  const errorId = `${id}-error`;
  const describedBy = [hint && hintId, error && errorId].filter(Boolean).join(' ') || undefined;

  return (
    <div>
      <label htmlFor={id} className="block text-sm font-medium text-fg-soft mb-1">{label}</label>
      <textarea
        id={id}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        rows={rows}
        placeholder={placeholder}
        disabled={disabled}
        spellCheck={false}
        aria-invalid={error ? 'true' : undefined}
        aria-describedby={describedBy}
        className={`w-full px-3 py-2 border rounded-lg font-mono text-xs focus:outline-none focus:ring-2 focus:ring-focus ${error ? 'border-red-600' : 'border-line-strong'}`}
      />
      {hint && <p id={hintId} className="text-xs text-fg-muted mt-1">{hint}</p>}
      {error && <p id={errorId} className="text-xs text-danger mt-1">{error}</p>}
    </div>
  );
}
