import { useEffect, useId, useRef, useState } from 'react';
import { CircleHelp } from 'lucide-react';

/**
 * The panel's one help control (FE-021): a small "?" button next to a plain
 * text that opens the technical detail behind it — the endpoint, the
 * environment variable, a link to the docs. Texts on a page stay short and
 * plain; whoever wants the detail opens it.
 *
 * A disclosure, not a dialog: the button carries aria-expanded and
 * aria-controls, the panel follows it in the DOM so Tab reaches its links.
 * Escape closes it and puts focus back on the button; a click outside, or
 * focus leaving it, closes it too. Under the md breakpoint the panel sits
 * fixed at the bottom of the window, so it never runs off a phone screen.
 */
export default function HelpPopover({ label, children, className = '' }) {
  const [open, setOpen] = useState(false);
  const panelId = useId();
  const rootRef = useRef(null);
  const buttonRef = useRef(null);

  useEffect(() => {
    if (!open) return undefined;
    const onKey = (e) => {
      if (e.key !== 'Escape') return;
      e.stopPropagation();
      setOpen(false);
      buttonRef.current.focus();
    };
    const onPointer = (e) => {
      if (!rootRef.current.contains(e.target)) setOpen(false);
    };
    document.addEventListener('keydown', onKey);
    document.addEventListener('mousedown', onPointer);
    return () => {
      document.removeEventListener('keydown', onKey);
      document.removeEventListener('mousedown', onPointer);
    };
  }, [open]);

  // Tabbing out of the button and its panel closes the panel.
  const onBlur = (e) => {
    if (e.relatedTarget && !rootRef.current.contains(e.relatedTarget)) setOpen(false);
  };

  return (
    <div ref={rootRef} onBlur={onBlur} className={`relative inline-flex align-middle ${className}`}>
      <button
        ref={buttonRef}
        type="button"
        aria-expanded={open}
        aria-controls={panelId}
        aria-label={`About ${label}`}
        title={`About ${label}`}
        onClick={() => setOpen((v) => !v)}
        className="rounded-full p-0.5 text-fg-subtle hover:bg-surface-subtle hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-focus"
      >
        <CircleHelp className="h-4 w-4" aria-hidden="true" />
      </button>
      <div
        id={panelId}
        hidden={!open}
        className="absolute left-0 top-full z-30 mt-1 w-72 max-md:fixed max-md:inset-x-4 max-md:bottom-4 max-md:top-auto max-md:w-auto space-y-1 rounded-lg border border-line-strong bg-surface p-3 text-left text-xs font-normal normal-case tracking-normal text-fg-muted shadow-lg"
      >
        {children}
      </div>
    </div>
  );
}
