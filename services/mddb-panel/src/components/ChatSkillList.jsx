import { formatBytes, sourceLabel } from '../lib/chat-skill';

// #404: the stored skills, one button each: name, description, version,
// size, where the text came from and how many scenarios use it.

export default function ChatSkillList({ skills, selected, onSelect }) {
  if (skills.length === 0) return <p className="text-sm text-gray-600">No skills yet.</p>;
  return (
    <ul className="space-y-1">
      {skills.map((skill) => {
        const used = skill.usedBy?.length || 0;
        return (
          <li key={skill.name}>
            <button
              type="button"
              onClick={() => onSelect(skill.name)}
              aria-current={selected === skill.name ? 'true' : undefined}
              className={`w-full rounded-lg px-3 py-2 text-left ${selected === skill.name ? 'bg-primary-100 text-primary-900' : 'text-gray-800 hover:bg-gray-100'}`}
            >
              <span className="flex items-center justify-between gap-2">
                <span className="truncate text-sm font-medium">{skill.name}</span>
                <span className="text-xs text-gray-700">v{skill.version}</span>
              </span>
              {skill.description && <span className="block truncate text-xs text-gray-600">{skill.description}</span>}
              <span className="block truncate text-xs text-gray-600">
                {formatBytes(skill.bytes)} · {sourceLabel(skill.source)} · used by {used} scenario{used === 1 ? '' : 's'}
              </span>
            </button>
          </li>
        );
      })}
    </ul>
  );
}
