// The labelled controls of a cron rule's action fields (CronActionFields,
// GitSyncFields). Each control sits inside its label (no ids), because the
// add form and an inline edit row can be open at once.

export const field = 'mt-1 w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm text-gray-900';
export const labelClass = 'block text-xs text-gray-600';

/** A hint under a control, in a colour that keeps WCAG AA contrast. */
export function Hint({ children }) {
  return <span className="mt-0.5 block text-[11px] text-gray-600">{children}</span>;
}

export function Text({ name, value, onChange, placeholder, type = 'text', mono = false, hint }) {
  return (
    <label className={labelClass}>
      {name}
      <input
        type={type}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        className={`${field}${mono ? ' font-mono' : ''}`}
      />
      {hint && <Hint>{hint}</Hint>}
    </label>
  );
}

export function Select({ name, value, onChange, children, hint }) {
  return (
    <label className={labelClass}>
      {name}
      <select value={value} onChange={(e) => onChange(e.target.value)} className={field}>
        {children}
      </select>
      {hint && <Hint>{hint}</Hint>}
    </label>
  );
}

export function TextArea({ name, value, onChange, placeholder, hint, wide = false }) {
  return (
    <label className={`${labelClass}${wide ? ' md:col-span-2' : ''}`}>
      {name}
      <textarea rows={3} value={value} onChange={(e) => onChange(e.target.value)} placeholder={placeholder} className={`${field} font-mono`} />
      {hint && <Hint>{hint}</Hint>}
    </label>
  );
}

export function Check({ checked, onChange, children, wide = false }) {
  return (
    <label className={`${wide ? 'md:col-span-2 ' : ''}flex items-center gap-2 text-sm text-gray-700`}>
      <input type="checkbox" checked={checked} onChange={(e) => onChange(e.target.checked)} className="w-4 h-4 rounded border-gray-300" />
      {children}
    </label>
  );
}
