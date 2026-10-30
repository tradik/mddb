// @vitest-environment jsdom
// UsersPanel: the user list (loading, error with retry, empty, rows), creating
// a user, deleting one behind a confirmation, and the permissions modal — its
// list with scoped admin labels (SEC-020), the scope hint, adding a grant,
// failures and the success message that clears after two seconds.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act, within } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: {
    listUsers: vi.fn(),
    createUser: vi.fn(),
    deleteUser: vi.fn(),
    getUserPermissions: vi.fn(),
    setUserPermission: vi.fn(),
  },
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: UsersPanel } = await import('./UsersPanel');

const initialStore = useStore.getState();

const nowSeconds = Math.floor(Date.now() / 1000);
const alice = { username: 'alice', createdAt: nowSeconds - 3600, admin: true, groups: ['ops', 'dev'] };
const bob = { username: 'bob', disabled: true };

/** The rendered table rows, header excluded. */
function bodyRows(container) {
  return [...container.querySelectorAll('tbody tr')];
}

/** The cells of one row, as text. */
function cells(row) {
  return [...row.querySelectorAll('td')].map((td) => td.textContent.trim());
}

/** The modal overlay holding the heading `title`. */
function modal(title) {
  return screen.getByRole('heading', { name: title }).closest('.fixed');
}

/** Renders the panel with `users` and waits for the table. */
async function renderWith(users = [alice, bob]) {
  vi.mocked(mddbClient.listUsers).mockResolvedValue({ users });
  const view = render(<UsersPanel />);
  await screen.findByText('User Management');
  return view;
}

/** Opens the permissions modal for the user in row `index`. */
async function openPermissions(index = 0) {
  fireEvent.click(screen.getAllByTitle('Manage Permissions')[index]);
  await screen.findByText('Current Permissions');
  await screen.findByRole('button', { name: 'Add Permission' });
}

beforeEach(() => {
  useStore.setState(initialStore, true);
  for (const fn of Object.values(mddbClient)) vi.mocked(fn).mockReset();
  vi.mocked(mddbClient.getUserPermissions).mockResolvedValue([]);
  vi.spyOn(console, 'error').mockImplementation(() => {});
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe('UsersPanel list', () => {
  test('shows a spinner while the list loads', () => {
    vi.mocked(mddbClient.listUsers).mockReturnValue(new Promise(() => {}));
    const { container } = render(<UsersPanel />);
    expect(container.querySelector('.animate-spin')).not.toBeNull();
    expect(screen.queryByText('User Management')).toBeNull();
  });

  test('reports a failure and loads again on Retry', async () => {
    vi.mocked(mddbClient.listUsers)
      .mockRejectedValueOnce(new Error('API Error (403): forbidden'))
      .mockResolvedValueOnce({ users: [alice] });
    render(<UsersPanel />);
    expect(await screen.findByText('Failed to load users')).toBeTruthy();
    expect(screen.getByText('API Error (403): forbidden')).toBeTruthy();
    expect(console.error).toHaveBeenCalledWith('Failed to load users:', expect.any(Error));

    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('alice')).toBeTruthy();
    expect(useStore.getState().usersError).toBeNull();
    expect(mddbClient.listUsers).toHaveBeenCalledTimes(2);
  });

  test('says so when there are no users (a response without a list)', async () => {
    vi.mocked(mddbClient.listUsers).mockResolvedValue({});
    render(<UsersPanel />);
    expect(await screen.findByText('No users found')).toBeTruthy();
    expect(useStore.getState().users).toEqual([]);
  });

  test('renders a row per user: initial, created, role, groups and status', async () => {
    const { container } = await renderWith([alice, bob, { ...bob, username: 'carol', groups: [] }]);
    const [a, b, c] = bodyRows(container).map(cells);
    expect(a[0]).toBe('Aalice');
    expect(a[1]).toBe('about 1 hour ago');
    expect(a[2]).toBe('Admin');
    expect(a[3]).toBe('opsdev');
    expect(a[4]).toBe('Active');
    expect(b).toEqual(['Bbob', 'N/A', 'User', 'No groups', 'Disabled', '']);
    expect(c[3]).toBe('No groups');
  });

  test('Refresh asks the server again', async () => {
    await renderWith();
    vi.mocked(mddbClient.listUsers).mockResolvedValue({ users: [bob] });
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    await screen.findByText('User Management');
    expect(await screen.findByText('bob')).toBeTruthy();
    expect(screen.queryByText('alice')).toBeNull();
    expect(mddbClient.listUsers).toHaveBeenCalledTimes(2);
  });
});

describe('UsersPanel create user', () => {
  test('sends username, password and the admin flag, then reloads the list', async () => {
    const { container } = await renderWith();
    fireEvent.click(screen.getByRole('button', { name: 'Create User' }));
    const dialog = modal('Create New User');
    expect(within(dialog).getByText('Administrator privileges (admin on "*", server-wide)')).toBeTruthy();

    // Every field is named by its label (htmlFor/id), for screen readers.
    fireEvent.change(within(dialog).getByLabelText('Username'), { target: { value: 'dave' } });
    fireEvent.change(within(dialog).getByLabelText('Password'), { target: { value: 's3cret!' } });
    fireEvent.click(within(dialog).getByLabelText(/Administrator privileges/));
    let finish;
    vi.mocked(mddbClient.createUser).mockReturnValue(new Promise((resolve) => (finish = resolve)));
    fireEvent.submit(dialog.querySelector('form'));

    expect(await within(dialog).findByRole('button', { name: 'Creating...' })).toBeTruthy();
    expect(within(dialog).getByRole('button', { name: 'Creating...' }).disabled).toBe(true);
    expect(mddbClient.createUser).toHaveBeenCalledWith({ username: 'dave', password: 's3cret!', admin: true });

    vi.mocked(mddbClient.listUsers).mockResolvedValue({ users: [alice, bob, { username: 'dave' }] });
    await act(async () => finish({}));
    expect(await screen.findByText('dave')).toBeTruthy();
    expect(screen.queryByText('Create New User')).toBeNull();
    expect(mddbClient.listUsers).toHaveBeenCalledTimes(2);
    expect(bodyRows(container)).toHaveLength(3);
  });

  test('shows the server error and stays open', async () => {
    await renderWith();
    fireEvent.click(screen.getByRole('button', { name: 'Create User' }));
    const dialog = modal('Create New User');
    vi.mocked(mddbClient.createUser).mockRejectedValue(new Error('user exists'));
    fireEvent.submit(dialog.querySelector('form'));

    expect(await within(dialog).findByText('user exists')).toBeTruthy();
    expect(mddbClient.createUser).toHaveBeenCalledWith({ username: '', password: '', admin: false });
    expect(within(dialog).getByRole('button', { name: 'Create User' }).disabled).toBe(false);
    expect(mddbClient.listUsers).toHaveBeenCalledTimes(1);
  });

  test.each([
    ['Cancel', (dialog) => within(dialog).getByRole('button', { name: 'Cancel' })],
    ['the close icon', (dialog) => dialog.querySelector('h2 + button')],
  ])('%s closes the modal without a request', async (_label, button) => {
    await renderWith();
    fireEvent.click(screen.getByRole('button', { name: 'Create User' }));
    fireEvent.click(button(modal('Create New User')));
    expect(screen.queryByText('Create New User')).toBeNull();
    expect(mddbClient.createUser).not.toHaveBeenCalled();
  });
});

describe('UsersPanel delete user', () => {
  test('asks first, then deletes and reloads', async () => {
    await renderWith();
    fireEvent.click(screen.getAllByTitle('Delete User')[1]);
    const dialog = modal('Delete User');
    expect(within(dialog).getByText('Are you sure you want to delete user "bob"? This action cannot be undone.')).toBeTruthy();

    vi.mocked(mddbClient.deleteUser).mockResolvedValue({});
    vi.mocked(mddbClient.listUsers).mockResolvedValue({ users: [alice] });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }));

    await screen.findByText('alice');
    expect(mddbClient.deleteUser).toHaveBeenCalledWith('bob');
    expect(screen.queryByRole('heading', { name: 'Delete User' })).toBeNull();
    expect(screen.queryByText('bob')).toBeNull();
    expect(mddbClient.listUsers).toHaveBeenCalledTimes(2);
  });

  test('alerts on a failed delete and keeps the confirmation open', async () => {
    const alertSpy = vi.spyOn(window, 'alert').mockImplementation(() => {});
    await renderWith();
    fireEvent.click(screen.getAllByTitle('Delete User')[0]);
    vi.mocked(mddbClient.deleteUser).mockRejectedValue(new Error('cannot delete yourself'));
    fireEvent.click(within(modal('Delete User')).getByRole('button', { name: 'Delete' }));

    await vi.waitFor(() => expect(alertSpy).toHaveBeenCalledWith('Failed to delete user: cannot delete yourself'));
    expect(mddbClient.deleteUser).toHaveBeenCalledWith('alice');
    expect(screen.getByRole('heading', { name: 'Delete User' })).toBeTruthy();
    expect(mddbClient.listUsers).toHaveBeenCalledTimes(1);
  });

  test('Cancel closes the confirmation without deleting', async () => {
    await renderWith();
    fireEvent.click(screen.getAllByTitle('Delete User')[0]);
    fireEvent.click(within(modal('Delete User')).getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('heading', { name: 'Delete User' })).toBeNull();
    expect(mddbClient.deleteUser).not.toHaveBeenCalled();
  });
});

describe('UsersPanel permissions modal', () => {
  test('shows Loading until the permissions arrive', async () => {
    await renderWith();
    vi.mocked(mddbClient.getUserPermissions).mockReturnValue(new Promise(() => {}));
    fireEvent.click(screen.getAllByTitle('Manage Permissions')[0]);
    expect(screen.getByText('Loading...')).toBeTruthy();
    expect(screen.getByText('Manage Permissions: alice')).toBeTruthy();
    expect(mddbClient.getUserPermissions).toHaveBeenCalledWith('alice');
  });

  test.each([
    ['a bare array', [
      { collection: '*', read: true, write: true, admin: true },
      { collection: 'blog', read: false, write: false, admin: true },
    ]],
    ['a wrapped list', { permissions: [
      { collection: '*', read: true, write: true, admin: true },
      { collection: 'blog', read: false, write: false, admin: true },
    ] }],
  ])('lists grants from %s with scoped admin labels', async (_label, response) => {
    await renderWith();
    vi.mocked(mddbClient.getUserPermissions).mockResolvedValue(response);
    await openPermissions();
    const dialog = modal('Manage Permissions: alice');
    const grants = [...dialog.querySelectorAll('.space-y-2 > .bg-gray-50')].map((g) => g.textContent);
    expect(grants).toEqual(['*ReadWriteAdmin (global)', 'blogAdmin (this collection)']);
  });

  test.each([
    ['an object without a list', {}],
    ['an empty array', []],
  ])('says "No permissions set" for %s', async (_label, response) => {
    await renderWith();
    vi.mocked(mddbClient.getUserPermissions).mockResolvedValue(response);
    await openPermissions();
    expect(screen.getByText('No permissions set')).toBeTruthy();
  });

  test('reports a failed load', async () => {
    await renderWith();
    vi.mocked(mddbClient.getUserPermissions).mockRejectedValue(new Error('boom'));
    fireEvent.click(screen.getAllByTitle('Manage Permissions')[0]);
    expect(await screen.findByText('Failed to load permissions')).toBeTruthy();
    expect(console.error).toHaveBeenCalledWith('Failed to load permissions:', expect.any(Error));
  });

  test('the scope hint warns about admin on a named collection only', async () => {
    await renderWith();
    await openPermissions();
    const dialog = modal('Manage Permissions: alice');
    const warning = () => dialog.querySelector('strong');
    expect(warning()).toBeNull();

    fireEvent.click(within(dialog).getByLabelText('Admin'));
    expect(warning()).toBeNull();

    fireEvent.change(within(dialog).getByRole('textbox'), { target: { value: 'blog' } });
    expect(warning().textContent).toBe('Admin on "blog" covers that collection only — not server-wide.');
  });

  test('adds a grant, resets the form, reloads, and clears the success note after 2 s', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    await renderWith();
    await openPermissions();
    const dialog = modal('Manage Permissions: alice');

    fireEvent.change(within(dialog).getByRole('textbox'), { target: { value: 'blog' } });
    fireEvent.click(within(dialog).getByLabelText('Read'));
    fireEvent.click(within(dialog).getByLabelText('Write'));
    fireEvent.click(within(dialog).getByLabelText('Admin'));
    let finish;
    vi.mocked(mddbClient.setUserPermission).mockReturnValue(new Promise((resolve) => (finish = resolve)));
    vi.mocked(mddbClient.getUserPermissions).mockResolvedValue([
      { collection: 'blog', read: false, write: true, admin: true },
    ]);
    fireEvent.submit(dialog.querySelector('form'));

    expect(within(dialog).getByRole('button', { name: 'Adding...' }).disabled).toBe(true);
    expect(mddbClient.setUserPermission).toHaveBeenCalledWith({
      username: 'alice',
      collection: 'blog',
      read: false,
      write: true,
      admin: true,
    });

    await act(async () => finish({}));
    expect(await within(dialog).findByText('✓ Permission added successfully!')).toBeTruthy();
    expect(within(dialog).getByText('Admin (this collection)')).toBeTruthy();
    expect(within(dialog).getByRole('textbox').value).toBe('*');
    expect(within(dialog).getByLabelText('Read').checked).toBe(true);
    expect(within(dialog).getByLabelText('Write').checked).toBe(false);
    expect(within(dialog).getByLabelText('Admin').checked).toBe(false);
    expect(mddbClient.getUserPermissions).toHaveBeenCalledTimes(2);

    await act(async () => vi.advanceTimersByTime(2000));
    expect(within(dialog).queryByText('✓ Permission added successfully!')).toBeNull();
  });

  test.each([
    ['the server message', new Error('API Error (400): bad collection'), 'API Error (400): bad collection'],
    ['a fallback for an empty message', new Error(''), 'Failed to set permission'],
  ])('shows %s when the grant fails', async (_label, failure, shown) => {
    await renderWith();
    await openPermissions();
    const dialog = modal('Manage Permissions: alice');
    vi.mocked(mddbClient.setUserPermission).mockRejectedValue(failure);
    fireEvent.submit(dialog.querySelector('form'));

    expect(await within(dialog).findByText(shown)).toBeTruthy();
    expect(console.error).toHaveBeenCalledWith('Permission error:', failure);
    expect(within(dialog).getByRole('button', { name: 'Add Permission' }).disabled).toBe(false);
    expect(within(dialog).queryByText('✓ Permission added successfully!')).toBeNull();
  });

  test.each([
    ['Close', (dialog) => within(dialog).getByRole('button', { name: 'Close' })],
    ['the close icon', (dialog) => dialog.querySelector('h2 + button')],
  ])('%s closes the modal', async (_label, button) => {
    await renderWith();
    await openPermissions(1);
    fireEvent.click(button(modal('Manage Permissions: bob')));
    expect(screen.queryByText('Manage Permissions: bob')).toBeNull();
    expect(mddbClient.getUserPermissions).toHaveBeenCalledWith('bob');
  });
});
