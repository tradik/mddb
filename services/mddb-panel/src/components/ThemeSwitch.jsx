import { Sun, Moon, Monitor } from 'lucide-react';
import { useTheme } from '../lib/theme';

/**
 * ThemeSwitch (FE-015): three buttons in the header — light, dark, and the
 * system's setting. The pressed one is the stored choice (aria-pressed),
 * so the state is not shown by colour alone; each button names its theme
 * for screen readers and in its tooltip.
 */
const OPTIONS = [
  { value: 'light', label: 'Light theme', Icon: Sun },
  { value: 'dark', label: 'Dark theme', Icon: Moon },
  { value: 'system', label: 'System theme', Icon: Monitor },
];

export default function ThemeSwitch() {
  const { theme, setTheme } = useTheme();
  return (
    <div role="group" aria-label="Colour theme" className="flex items-center bg-surface-subtle rounded-lg p-0.5">
      {OPTIONS.map(({ value, label, Icon }) => (
        <button
          key={value}
          type="button"
          onClick={() => setTheme(value)}
          aria-pressed={theme === value}
          aria-label={label}
          title={label}
          className={`p-1.5 rounded-md transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus ${
            theme === value ? 'bg-surface text-fg shadow-sm' : 'text-fg-faint hover:text-fg-muted'
          }`}
        >
          <Icon className="w-4 h-4" aria-hidden="true" />
        </button>
      ))}
    </div>
  );
}
