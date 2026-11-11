import { MessagesSquare, FileCheck, Import, CopyX, UserRound } from 'lucide-react';

/**
 * AdminToolNav adds the sidebar entries of the pages added in 2.19.0 to the
 * Administration list. `NavButton` is the sidebar's own button, so the
 * entries look and behave like the others; `authEnabled` hides pages that
 * only exist with authentication.
 */
export default function AdminToolNav({ NavButton, authEnabled }) {
  return (
    <>
      <NavButton mode="memory" icon={MessagesSquare} label="Agent Memory" />
      <NavButton mode="schemas" icon={FileCheck} label="Collection Schemas" />
      <NavButton mode="import" icon={Import} label="Import" />
      <NavButton mode="duplicates" icon={CopyX} label="Duplicates" />
      {authEnabled && <NavButton mode="account" icon={UserRound} label="My Account" />}
    </>
  );
}
