import { useRef } from 'react';

// #404: the Chat page's tabs — scenarios, skills and (#406) the log — as an ARIA tab list:
// the selected tab is the one in the tab order, the arrow keys, Home and End
// move between tabs, and each tab names the panel it controls.

const chatTabs = [
  { id: 'scenarios', label: 'Scenarios' },
  { id: 'skills', label: 'Skills' },
  { id: 'log', label: 'Log' }, // #406
];

export default function ChatTabs({ tab, onChange }) {
  const refs = useRef({});
  const go = (index) => {
    const next = chatTabs[(index + chatTabs.length) % chatTabs.length];
    onChange(next.id);
    refs.current[next.id]?.focus();
  };
  const onKeyDown = (e, index) => {
    const moves = { ArrowRight: index + 1, ArrowLeft: index - 1, Home: 0, End: chatTabs.length - 1 };
    if (!(e.key in moves)) return;
    e.preventDefault();
    go(moves[e.key]);
  };
  return (
    <div role="tablist" aria-label="Chat" className="mb-4 flex gap-1 border-b border-line-strong">
      {chatTabs.map((t, i) => (
        <button
          key={t.id}
          ref={(el) => { refs.current[t.id] = el; }}
          type="button"
          role="tab"
          id={`chat-tab-${t.id}`}
          aria-selected={tab === t.id}
          aria-controls={`chat-panel-${t.id}`}
          tabIndex={tab === t.id ? 0 : -1}
          onClick={() => onChange(t.id)}
          onKeyDown={(e) => onKeyDown(e, i)}
          className={`-mb-px border-b-2 px-3 py-2 text-sm font-medium focus:outline-none focus:ring-2 focus:ring-focus ${tab === t.id ? 'border-primary-700 text-accent-strong' : 'border-transparent text-fg-muted hover:text-fg'}`}
        >
          {t.label}
        </button>
      ))}
    </div>
  );
}
