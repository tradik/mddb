import { toolHints, toolInactiveReason } from '../lib/chat-mcp';

// MCP-005: the tools of one external MCP server, as the last "Test
// connection" read them. A tool the server does not mark read-only (or marks
// destructive) cannot be checked, and says why. Each tool says whether the
// model sees it, or the first switch that hides it. An allowed tool the
// server no longer offers stays listed, so it can be unchecked; before a
// test the allowed names are listed as they are stored.

function Badge({ tone, children }) {
  const tones = {
    green: 'bg-green-100 text-green-900',
    red: 'bg-red-100 text-red-900',
    gray: 'bg-gray-200 text-gray-800',
  };
  return <span className={`rounded-full px-2 py-0.5 text-xs ${tones[tone]}`}>{children}</span>;
}

export default function ChatMcpToolList({ id, tools, tested, allowed, onChange, featureOff, scenarioEnabled, serverEnabled }) {
  const offered = new Set(tools.map((t) => t.name));
  const rows = [...tools, ...allowed.filter((n) => !offered.has(n)).map((name) => ({ name, missing: tested, allowable: true }))];
  const toggle = (name) => onChange(allowed.includes(name) ? allowed.filter((n) => n !== name) : [...allowed, name]);
  return (
    <ul className="space-y-1 rounded border border-gray-300 p-2" aria-label="Tools of this server">
      {rows.map((tool) => {
        const { readOnly, destructive } = toolHints(tool);
        const checked = allowed.includes(tool.name);
        const blocked = tool.missing ? 'the server no longer offers it' : !tool.allowable && tool.reason;
        const inactive = toolInactiveReason({ featureOff, scenarioEnabled, serverEnabled, allowed: checked });
        const boxId = `${id}-tool-${tool.name}`;
        return (
          <li key={tool.name} className="text-sm text-gray-800">
            <div className="flex flex-wrap items-center gap-2">
              <input
                id={boxId}
                type="checkbox"
                checked={checked}
                disabled={Boolean(blocked) && !checked}
                onChange={() => toggle(tool.name)}
                className="h-4 w-4 rounded border-gray-400 text-primary-700 focus:ring-primary-500"
              />
              <label htmlFor={boxId} className="font-medium">{tool.name}</label>
              {readOnly && <Badge tone="green">read-only</Badge>}
              {destructive && <Badge tone="red">destructive</Badge>}
              {tool.annotations && !readOnly && !destructive && <Badge tone="gray">may write</Badge>}
              <span className="text-xs text-gray-700">{inactive ? `Inactive: ${inactive}` : 'Active'}</span>
            </div>
            {tool.description && <p className="ml-6 text-xs text-gray-600">{tool.description}</p>}
            {blocked && <p className="ml-6 text-xs text-red-800">Cannot be allowed: {blocked}</p>}
          </li>
        );
      })}
    </ul>
  );
}
