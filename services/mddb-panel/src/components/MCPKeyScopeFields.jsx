import { inputClass, labelClass } from '../lib/chat-scenario';
import { isLimited } from '../lib/mcp-key-scope';
import CollectionChecklist from './CollectionChecklist';

/**
 * MCPKeyScopeFields are the optional scope of a new MCP API key (#439): the
 * collections it may reach, read-only, and a chat scenario to bind it to.
 * The scenario select is shown when there are scenarios to pick. Once
 * collections are chosen, the ones the key may only read (AGT-007) are
 * picked among them, and a limited key may name the memory namespace it
 * shares with other keys (AGT-008).
 */
export default function MCPKeyScopeFields({ scope, onChange, collections, scenarios }) {
  const set = (field, value) => onChange({ ...scope, [field]: value });
  return (
    <fieldset className="space-y-3 rounded-lg border border-line-strong p-3">
      <legend className="px-1 text-sm font-medium text-fg-soft">Scope (optional)</legend>
      <CollectionChecklist
        collections={collections}
        selected={scope.collections}
        onChange={(v) => set('collections', v)}
        legend="Collections"
        hint="The key may reach only these; a call naming another collection, or none, is refused. None selected: every collection."
      />
      <label className="flex items-center gap-2 text-sm text-fg-soft">
        <input
          type="checkbox"
          checked={scope.readOnly}
          onChange={(e) => set('readOnly', e.target.checked)}
          className="h-4 w-4 rounded border-line-strong text-accent focus:ring-focus"
        />
        <span>Read-only — only the tools read-only mode allows</span>
      </label>
      {scope.collections.length > 0 && !scope.readOnly && (
        <CollectionChecklist
          collections={scope.collections}
          selected={scope.readOnlyCollections.filter((c) => scope.collections.includes(c))}
          onChange={(v) => set('readOnlyCollections', v)}
          legend="Read-only collections"
          hint="The key may search these but not write to them; it writes only to the other collections above."
        />
      )}
      {scenarios.length > 0 && (
        <div>
          <label htmlFor="mcp-key-scenario" className={labelClass}>Chat scenario</label>
          <select id="mcp-key-scenario" value={scope.scenario} onChange={(e) => set('scenario', e.target.value)} className={inputClass}>
            <option value="">None</option>
            {scenarios.map((name) => <option key={name} value={name}>{name}</option>)}
          </select>
          <p className="mt-1 text-xs text-fg-subtle">
            A bound key reaches the scenario&apos;s allowed collections, read-only, and gets its composed prompt as the MCP instructions.
          </p>
        </div>
      )}
      {isLimited(scope) && (
        <div>
          <label htmlFor="mcp-key-memory-namespace" className={labelClass}>Memory namespace</label>
          <input
            id="mcp-key-memory-namespace"
            type="text"
            value={scope.memoryNamespace}
            onChange={(e) => set('memoryNamespace', e.target.value)}
            placeholder="e.g. shop-api"
            maxLength={128}
            className={inputClass}
          />
          <p className="mt-1 text-xs text-fg-subtle">
            The memory tools see only this key&apos;s sessions. Keys given the same name share their sessions; empty keeps them to this key.
          </p>
        </div>
      )}
    </fieldset>
  );
}
