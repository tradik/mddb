import { labelClass } from '../lib/chat-scenario';

/**
 * CollectionChecklist is a checkbox per collection, for a scenario's allowed
 * collections (#403) and an MCP key's scope (#439). A selected name that is
 * not a collection here (an alias, a "role:" target) stays listed, so saving
 * does not silently drop it.
 */
export default function CollectionChecklist({ collections, selected, onChange, legend, hint }) {
  const names = [...new Set([...collections, ...selected])].sort((a, b) => a.localeCompare(b));
  const toggle = (name) => onChange(selected.includes(name) ? selected.filter((n) => n !== name) : [...selected, name]);
  return (
    <fieldset>
      <legend className={labelClass}>{legend}</legend>
      {names.length === 0 ? (
        <p className="text-sm text-fg-subtle">This server has no collections yet.</p>
      ) : (
        <div className="max-h-40 overflow-y-auto rounded border border-line-strong p-2 grid gap-1 sm:grid-cols-2">
          {names.map((name) => (
            <label key={name} className="flex items-center gap-2 text-sm text-fg-soft">
              <input
                type="checkbox"
                checked={selected.includes(name)}
                onChange={() => toggle(name)}
                className="h-4 w-4 rounded border-line-strong text-accent focus:ring-focus"
              />
              <span className="truncate">{name}</span>
            </label>
          ))}
        </div>
      )}
      <p className="mt-1 text-xs text-fg-subtle">{hint}</p>
    </fieldset>
  );
}
