// #403: the stored scenarios, one button each; the selected one is marked
// with aria-current and a disabled one with an "off" badge.

export default function ChatScenarioList({ scenarios, selected, onSelect }) {
  if (scenarios.length === 0) return <p className="text-sm text-fg-subtle">No scenarios yet.</p>;
  return (
    <ul className="space-y-1">
      {scenarios.map((scenario) => (
        <li key={scenario.name}>
          <button
            type="button"
            onClick={() => onSelect(scenario)}
            aria-current={selected === scenario.name ? 'true' : undefined}
            className={`w-full rounded-lg px-3 py-2 text-left ${selected === scenario.name ? 'bg-accent-muted text-accent-strong' : 'text-fg-soft hover:bg-surface-subtle'}`}
          >
            <span className="flex items-center justify-between gap-2">
              <span className="truncate text-sm font-medium">{scenario.name}</span>
              {!scenario.enabled && <span className="rounded-full bg-surface-strong px-2 py-0.5 text-xs text-fg-soft">off</span>}
            </span>
            {scenario.description && <span className="block truncate text-xs text-fg-subtle">{scenario.description}</span>}
          </button>
        </li>
      ))}
    </ul>
  );
}
