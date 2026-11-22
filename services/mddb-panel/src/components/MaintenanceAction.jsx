import HelpPopover from './HelpPopover';

/**
 * MaintenanceAction is one action of the Collection Maintenance page: its
 * name, one plain sentence on what it does and when to use it, the
 * technical detail behind a help control, and its controls (`children`).
 */
export default function MaintenanceAction({ title, text, help, children }) {
  return (
    <div className="border-t border-line pt-4 space-y-2">
      <h3 className="text-base font-semibold text-fg flex items-center gap-2">
        {title}
        <HelpPopover label={title}>{help}</HelpPopover>
      </h3>
      <p className="text-sm text-fg-muted">{text}</p>
      <div className="flex flex-wrap gap-3 items-end">{children}</div>
    </div>
  );
}
