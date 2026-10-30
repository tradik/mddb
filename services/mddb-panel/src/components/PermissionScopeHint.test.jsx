// @vitest-environment jsdom
// PermissionScopeHint: the note under the permission forms — always says only
// admin on "*" is global, and warns when admin is ticked on a named collection.
import { describe, test, expect, afterEach } from 'vitest';
import { render, cleanup } from '@testing-library/react';
import PermissionScopeHint from './PermissionScopeHint';

const globalNote = 'Only admin on "*" is global';

afterEach(cleanup);

describe('PermissionScopeHint', () => {
  test.each([
    ['blog', 'Admin on "blog" covers that collection only — not server-wide.'],
    ['  blog  ', 'Admin on "blog" covers that collection only — not server-wide.'],
    ['   ', 'Admin on "…" covers that collection only — not server-wide.'],
  ])('warns when admin is ticked on %j', (collection, warning) => {
    const { container } = render(<PermissionScopeHint collection={collection} admin />);
    expect(container.querySelector('strong').textContent).toBe(warning);
    expect(container.textContent).toContain(globalNote);
  });

  test.each([
    ['*', true],
    [' * ', true],
    ['blog', false],
  ])('no warning for collection %j with admin=%s', (collection, admin) => {
    const { container } = render(<PermissionScopeHint collection={collection} admin={admin} />);
    expect(container.querySelector('strong')).toBeNull();
    expect(container.textContent).toContain(globalNote);
    expect(container.textContent).toContain('A request that names no collection needs its permission on "*".');
  });
});
