/**
 * SEC-020 (2.18.10): admin on one collection no longer makes a global admin;
 * only admin on "*" does. The label of an admin grant says which it is.
 */
export function adminScopeLabel(collection) {
  return collection === '*' ? 'Admin (global)' : 'Admin (this collection)';
}
