// permissions: the label of an admin grant says whether it is server-wide
// (admin on "*") or limited to one collection (SEC-020, 2.18.10).
import { describe, test, expect } from 'vitest';
import { adminScopeLabel } from './permissions.js';

describe('adminScopeLabel', () => {
  test.each([
    ['*', 'Admin (global)'],
    ['blog', 'Admin (this collection)'],
    ['', 'Admin (this collection)'],
    [' * ', 'Admin (this collection)'],
  ])('admin on %j reads %j', (collection, label) => {
    expect(adminScopeLabel(collection)).toBe(label);
  });
});
