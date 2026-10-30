// @vitest-environment jsdom
// GroupsPanel: the group cards (loading, error with retry, empty, members),
// loading users for the member pickers, creating and editing a group, deleting
// one behind a confirmation, and the group permissions modal — scoped admin
// labels (SEC-020), the scope hint, adding a grant and its failures.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act, within } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: {
    listGroups: vi.fn(),
    listUsers: vi.fn(),
    createGroup: vi.fn(),
    updateGroup: vi.fn(),
    deleteGroup: vi.fn(),
    getGroupPermissions: vi.fn(),
    setGroupPermission: vi.fn(),
  },
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: GroupsPanel } = await import('./GroupsPanel');

const initialStore = useStore.getState();

const nowSeconds = Math.floor(Date.now() / 1000);
const users = [{ username: 'alice', admin: true }, { username: 'bob' }, { username: 'carol' }];
const ops = {
  name: 'ops',
  description: 'Operations team',
  createdAt: nowSeconds - 2 * 86400,
  members: ['alice', 'bob', 'carol', 'dave', 'erin', 'frank', 'gina'],
};
const empty = { name: 'empty' };

/** The card of the group named `name`. */
function card(name) {
  return screen.getByRole('heading', { name, level: 3 }).closest('.shadow');
}

/** The modal overlay holding the heading `title`. */
function modal(title) {
  return screen.getByRole('heading', { name: title }).closest('.fixed');
}

/** The usernames ticked in a member picker. */
function ticked(dialog) {
  return [...dialog.querySelectorAll('label.cursor-pointer')]
    .filter((label) => label.querySelector('input').checked)
    .map((label) => label.querySelector('span').textContent);
}

/** Ticks or unticks `username` in a member picker. */
function toggle(dialog, username) {
  const label = [...dialog.querySelectorAll('label.cursor-pointer')].find(
    (l) => l.querySelector('span').textContent === username,
  );
  fireEvent.click(label.querySelector('input'));
}

/** Renders the panel with `groups` (and users already in the store) and waits for it. */
async function renderWith(groups = [ops, empty], storeUsers = users) {
  useStore.setState({ users: storeUsers });
  vi.mocked(mddbClient.listGroups).mockResolvedValue({ groups });
  const view = render(<GroupsPanel />);
  await screen.findByText('Group Management');
  return view;
}

/** Opens the permissions modal for the group named `name`. */
async function openPermissions(name = 'ops') {
  fireEvent.click(within(card(name)).getByTitle('Manage Permissions'));
  await screen.findByRole('button', { name: 'Add Permission' });
}

beforeEach(() => {
  useStore.setState(initialStore, true);
  for (const fn of Object.values(mddbClient)) vi.mocked(fn).mockReset();
  vi.mocked(mddbClient.getGroupPermissions).mockResolvedValue({ permissions: [] });
  vi.spyOn(console, 'error').mockImplementation(() => {});
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe('GroupsPanel list', () => {
  test('shows a spinner while the list loads', () => {
    vi.mocked(mddbClient.listGroups).mockReturnValue(new Promise(() => {}));
    vi.mocked(mddbClient.listUsers).mockReturnValue(new Promise(() => {}));
    const { container } = render(<GroupsPanel />);
    expect(container.querySelector('.animate-spin')).not.toBeNull();
    expect(screen.queryByText('Group Management')).toBeNull();
  });

  test('reports a failure and loads again on Retry', async () => {
    vi.mocked(mddbClient.listGroups)
      .mockRejectedValueOnce(new Error('API Error (500): down'))
      .mockResolvedValueOnce({ groups: [ops] });
    vi.mocked(mddbClient.listUsers).mockResolvedValue({ users });
    render(<GroupsPanel />);
    expect(await screen.findByText('Failed to load groups')).toBeTruthy();
    expect(screen.getByText('API Error (500): down')).toBeTruthy();
    expect(console.error).toHaveBeenCalledWith('Failed to load groups:', expect.any(Error));

    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('Operations team')).toBeTruthy();
    expect(mddbClient.listGroups).toHaveBeenCalledTimes(2);
  });

  test.each([
    ['loads users when the store has none', { users }, users],
    ['keeps an empty list when the response has none', {}, []],
  ])('%s', async (_label, response, stored) => {
    vi.mocked(mddbClient.listGroups).mockResolvedValue({});
    vi.mocked(mddbClient.listUsers).mockResolvedValue(response);
    render(<GroupsPanel />);
    expect(await screen.findByText('No groups yet')).toBeTruthy();
    await vi.waitFor(() => expect(mddbClient.listUsers).toHaveBeenCalledTimes(1));
    await vi.waitFor(() => expect(useStore.getState().users).toEqual(stored));
  });

  test('logs a failed user load and still shows the groups', async () => {
    vi.mocked(mddbClient.listGroups).mockResolvedValue({ groups: [ops] });
    vi.mocked(mddbClient.listUsers).mockRejectedValue(new Error('forbidden'));
    render(<GroupsPanel />);
    expect(await screen.findByText('Operations team')).toBeTruthy();
    await vi.waitFor(() => expect(console.error).toHaveBeenCalledWith('Failed to load users:', expect.any(Error)));
  });

  test('skips the user request when the store already has users', async () => {
    await renderWith();
    expect(mddbClient.listUsers).not.toHaveBeenCalled();
  });

  test('renders a card per group: description, age, member count and the first five members', async () => {
    await renderWith([ops, empty, { name: 'none', members: [] }]);
    const opsCard = card('ops');
    expect(within(opsCard).getByText('Operations team')).toBeTruthy();
    expect(within(opsCard).getByText('2 days ago')).toBeTruthy();
    expect(within(opsCard).getByText('7')).toBeTruthy();
    const chips = [...opsCard.querySelectorAll('.rounded-full')].map((c) => c.textContent);
    expect(chips).toEqual(['alice', 'bob', 'carol', 'dave', 'erin', '+2 more']);

    for (const name of ['empty', 'none']) {
      const other = card(name);
      expect(within(other).getByText('N/A')).toBeTruthy();
      expect(within(other).getByText('0')).toBeTruthy();
      expect(within(other).getByText('No members')).toBeTruthy();
      expect(other.querySelector('p')).toBeNull();
    }
  });

  test('Refresh asks the server again', async () => {
    await renderWith();
    vi.mocked(mddbClient.listGroups).mockResolvedValue({ groups: [empty] });
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    await screen.findByText('Group Management');
    await vi.waitFor(() => expect(screen.queryByText('Operations team')).toBeNull());
    expect(mddbClient.listGroups).toHaveBeenCalledTimes(2);
  });
});

describe('GroupsPanel create group', () => {
  test('the empty state offers a first group', async () => {
    await renderWith([]);
    expect(screen.getByText('No groups yet')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Create Your First Group' }));
    expect(screen.getByRole('heading', { name: 'Create New Group' })).toBeTruthy();
  });

  test('sends name, description and the ticked members, then reloads', async () => {
    await renderWith();
    fireEvent.click(screen.getByRole('button', { name: 'Create Group' }));
    const dialog = modal('Create New Group');
    expect(within(dialog).getByText('Admin')).toBeTruthy();

    fireEvent.change(dialog.querySelector('input[type="text"]'), { target: { value: 'writers' } });
    fireEvent.change(dialog.querySelector('textarea'), { target: { value: 'Blog writers' } });
    toggle(dialog, 'alice');
    toggle(dialog, 'carol');
    toggle(dialog, 'alice');
    toggle(dialog, 'bob');
    expect(ticked(dialog)).toEqual(['bob', 'carol']);
    expect(within(dialog).getByText('Members (2)')).toBeTruthy();

    let finish;
    vi.mocked(mddbClient.createGroup).mockReturnValue(new Promise((resolve) => (finish = resolve)));
    fireEvent.submit(dialog.querySelector('form'));
    expect(within(dialog).getByRole('button', { name: 'Creating...' }).disabled).toBe(true);
    expect(mddbClient.createGroup).toHaveBeenCalledWith({
      name: 'writers',
      description: 'Blog writers',
      members: ['carol', 'bob'],
    });

    vi.mocked(mddbClient.listGroups).mockResolvedValue({ groups: [ops, { name: 'writers' }] });
    await act(async () => finish({}));
    expect(await screen.findByRole('heading', { name: 'writers', level: 3 })).toBeTruthy();
    expect(screen.queryByRole('heading', { name: 'Create New Group' })).toBeNull();
    expect(mddbClient.listGroups).toHaveBeenCalledTimes(2);
  });

  test('shows the server error, and an empty picker without users', async () => {
    vi.mocked(mddbClient.listUsers).mockResolvedValue({ users: [] });
    await renderWith([ops], []);
    fireEvent.click(screen.getByRole('button', { name: 'Create Group' }));
    const dialog = modal('Create New Group');
    expect(within(dialog).getByText('No users available')).toBeTruthy();

    vi.mocked(mddbClient.createGroup).mockRejectedValue(new Error('group exists'));
    fireEvent.submit(dialog.querySelector('form'));
    expect(await within(dialog).findByText('group exists')).toBeTruthy();
    expect(mddbClient.createGroup).toHaveBeenCalledWith({ name: '', description: '', members: [] });
    expect(within(dialog).getByRole('button', { name: 'Create Group' }).disabled).toBe(false);
  });

  test.each([
    ['Cancel', (dialog) => within(dialog).getByRole('button', { name: 'Cancel' })],
    ['the close icon', (dialog) => dialog.querySelector('h2 + button')],
  ])('%s closes the modal without a request', async (_label, button) => {
    await renderWith();
    fireEvent.click(screen.getByRole('button', { name: 'Create Group' }));
    fireEvent.click(button(modal('Create New Group')));
    expect(screen.queryByRole('heading', { name: 'Create New Group' })).toBeNull();
    expect(mddbClient.createGroup).not.toHaveBeenCalled();
  });
});

describe('GroupsPanel edit group', () => {
  test('starts from the group, sends the new description and members, then reloads', async () => {
    await renderWith();
    fireEvent.click(within(card('ops')).getByTitle('Edit Group'));
    const dialog = modal('Edit Group: ops');
    expect(dialog.querySelector('textarea').value).toBe('Operations team');
    expect(ticked(dialog)).toEqual(['alice', 'bob', 'carol']);
    expect(within(dialog).getByText('Members (7)')).toBeTruthy();

    fireEvent.change(dialog.querySelector('textarea'), { target: { value: 'Ops' } });
    toggle(dialog, 'bob');
    let finish;
    vi.mocked(mddbClient.updateGroup).mockReturnValue(new Promise((resolve) => (finish = resolve)));
    fireEvent.submit(dialog.querySelector('form'));
    expect(within(dialog).getByRole('button', { name: 'Saving...' }).disabled).toBe(true);
    expect(mddbClient.updateGroup).toHaveBeenCalledWith('ops', {
      description: 'Ops',
      members: ['alice', 'carol', 'dave', 'erin', 'frank', 'gina'],
    });

    vi.mocked(mddbClient.listGroups).mockResolvedValue({ groups: [{ ...ops, description: 'Ops' }] });
    await act(async () => finish({}));
    expect(await screen.findByText('Ops')).toBeTruthy();
    expect(screen.queryByRole('heading', { name: 'Edit Group: ops' })).toBeNull();
    expect(mddbClient.listGroups).toHaveBeenCalledTimes(2);
  });

  test('a group without description or members starts empty; adding a member and a failure', async () => {
    await renderWith();
    fireEvent.click(within(card('empty')).getByTitle('Edit Group'));
    const dialog = modal('Edit Group: empty');
    expect(dialog.querySelector('textarea').value).toBe('');
    expect(ticked(dialog)).toEqual([]);
    toggle(dialog, 'carol');

    vi.mocked(mddbClient.updateGroup).mockRejectedValue(new Error('not found'));
    fireEvent.submit(dialog.querySelector('form'));
    expect(await within(dialog).findByText('not found')).toBeTruthy();
    expect(mddbClient.updateGroup).toHaveBeenCalledWith('empty', { description: '', members: ['carol'] });
    expect(within(dialog).getByRole('button', { name: 'Save Changes' }).disabled).toBe(false);
  });

  test('shows "No users available" without users', async () => {
    vi.mocked(mddbClient.listUsers).mockResolvedValue({});
    await renderWith([empty], []);
    fireEvent.click(within(card('empty')).getByTitle('Edit Group'));
    expect(within(modal('Edit Group: empty')).getByText('No users available')).toBeTruthy();
  });

  test.each([
    ['Cancel', (dialog) => within(dialog).getByRole('button', { name: 'Cancel' })],
    ['the close icon', (dialog) => dialog.querySelector('h2 + button')],
  ])('%s closes the modal without a request', async (_label, button) => {
    await renderWith();
    fireEvent.click(within(card('ops')).getByTitle('Edit Group'));
    fireEvent.click(button(modal('Edit Group: ops')));
    expect(screen.queryByRole('heading', { name: 'Edit Group: ops' })).toBeNull();
    expect(mddbClient.updateGroup).not.toHaveBeenCalled();
  });
});

describe('GroupsPanel delete group', () => {
  test('asks first, then deletes and reloads', async () => {
    await renderWith();
    fireEvent.click(within(card('empty')).getByTitle('Delete Group'));
    const dialog = modal('Delete Group');
    expect(
      within(dialog).getByText(
        'Are you sure you want to delete group "empty"? This will remove all group permissions but will not delete the users.',
      ),
    ).toBeTruthy();

    vi.mocked(mddbClient.deleteGroup).mockResolvedValue({});
    vi.mocked(mddbClient.listGroups).mockResolvedValue({ groups: [ops] });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }));

    await vi.waitFor(() => expect(screen.queryByRole('heading', { name: 'empty', level: 3 })).toBeNull());
    expect(mddbClient.deleteGroup).toHaveBeenCalledWith('empty');
    expect(screen.queryByRole('heading', { name: 'Delete Group' })).toBeNull();
    expect(mddbClient.listGroups).toHaveBeenCalledTimes(2);
  });

  test('alerts on a failed delete and keeps the confirmation open', async () => {
    const alertSpy = vi.spyOn(window, 'alert').mockImplementation(() => {});
    await renderWith();
    fireEvent.click(within(card('ops')).getByTitle('Delete Group'));
    vi.mocked(mddbClient.deleteGroup).mockRejectedValue(new Error('in use'));
    fireEvent.click(within(modal('Delete Group')).getByRole('button', { name: 'Delete' }));

    await vi.waitFor(() => expect(alertSpy).toHaveBeenCalledWith('Failed to delete group: in use'));
    expect(mddbClient.deleteGroup).toHaveBeenCalledWith('ops');
    expect(screen.getByRole('heading', { name: 'Delete Group' })).toBeTruthy();
  });

  test('Cancel closes the confirmation without deleting', async () => {
    await renderWith();
    fireEvent.click(within(card('ops')).getByTitle('Delete Group'));
    fireEvent.click(within(modal('Delete Group')).getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('heading', { name: 'Delete Group' })).toBeNull();
    expect(mddbClient.deleteGroup).not.toHaveBeenCalled();
  });
});

describe('GroupsPanel permissions modal', () => {
  test('shows Loading until the permissions arrive', async () => {
    await renderWith();
    vi.mocked(mddbClient.getGroupPermissions).mockReturnValue(new Promise(() => {}));
    fireEvent.click(within(card('ops')).getByTitle('Manage Permissions'));
    expect(screen.getByText('Loading...')).toBeTruthy();
    expect(screen.getByRole('heading', { name: 'Group Permissions: ops' })).toBeTruthy();
    expect(mddbClient.getGroupPermissions).toHaveBeenCalledWith('ops');
  });

  test('lists grants with scoped admin labels', async () => {
    await renderWith();
    vi.mocked(mddbClient.getGroupPermissions).mockResolvedValue({
      permissions: [
        { collection: '*', read: true, write: true, admin: true },
        { collection: 'blog', read: false, write: false, admin: true },
        { collection: 'docs', read: true, write: false, admin: false },
      ],
    });
    await openPermissions();
    const dialog = modal('Group Permissions: ops');
    const grants = [...dialog.querySelectorAll('.space-y-2 > .bg-gray-50')].map((g) => g.textContent);
    expect(grants).toEqual(['*ReadWriteAdmin (global)', 'blogAdmin (this collection)', 'docsRead']);
  });

  test('says "No permissions set" when the response has no list', async () => {
    await renderWith();
    vi.mocked(mddbClient.getGroupPermissions).mockResolvedValue({});
    await openPermissions();
    expect(screen.getByText('No permissions set')).toBeTruthy();
  });

  test('reports a failed load', async () => {
    await renderWith();
    vi.mocked(mddbClient.getGroupPermissions).mockRejectedValue(new Error('boom'));
    fireEvent.click(within(card('ops')).getByTitle('Manage Permissions'));
    expect(await screen.findByText('Failed to load permissions')).toBeTruthy();
    expect(console.error).toHaveBeenCalledWith('Failed to load permissions:', expect.any(Error));
  });

  test('the scope hint warns about admin on a named collection only', async () => {
    await renderWith();
    await openPermissions();
    const dialog = modal('Group Permissions: ops');
    fireEvent.click(within(dialog).getByLabelText('Admin'));
    expect(dialog.querySelector('strong')).toBeNull();
    fireEvent.change(within(dialog).getByRole('textbox'), { target: { value: 'docs' } });
    expect(dialog.querySelector('strong').textContent).toBe(
      'Admin on "docs" covers that collection only — not server-wide.',
    );
  });

  test('adds a grant, resets the form, reloads, and clears the success note after 2 s', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    await renderWith();
    await openPermissions();
    const dialog = modal('Group Permissions: ops');

    fireEvent.change(within(dialog).getByRole('textbox'), { target: { value: 'blog' } });
    fireEvent.click(within(dialog).getByLabelText('Read'));
    fireEvent.click(within(dialog).getByLabelText('Write'));
    fireEvent.click(within(dialog).getByLabelText('Admin'));
    let finish;
    vi.mocked(mddbClient.setGroupPermission).mockReturnValue(new Promise((resolve) => (finish = resolve)));
    vi.mocked(mddbClient.getGroupPermissions).mockResolvedValue({
      permissions: [{ collection: 'blog', read: false, write: true, admin: true }],
    });
    fireEvent.submit(dialog.querySelector('form'));

    expect(within(dialog).getByRole('button', { name: 'Adding...' }).disabled).toBe(true);
    expect(mddbClient.setGroupPermission).toHaveBeenCalledWith({
      groupName: 'ops',
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
    expect(mddbClient.getGroupPermissions).toHaveBeenCalledTimes(2);

    await act(async () => vi.advanceTimersByTime(2000));
    expect(within(dialog).queryByText('✓ Permission added successfully!')).toBeNull();
  });

  test.each([
    ['the server message', new Error('API Error (400): bad collection'), 'API Error (400): bad collection'],
    ['a fallback for an empty message', new Error(''), 'Failed to set permission'],
  ])('shows %s when the grant fails', async (_label, failure, shown) => {
    await renderWith();
    await openPermissions();
    const dialog = modal('Group Permissions: ops');
    vi.mocked(mddbClient.setGroupPermission).mockRejectedValue(failure);
    fireEvent.submit(dialog.querySelector('form'));

    expect(await within(dialog).findByText(shown)).toBeTruthy();
    expect(console.error).toHaveBeenCalledWith('Permission error:', failure);
    expect(within(dialog).getByRole('button', { name: 'Add Permission' }).disabled).toBe(false);
  });

  test.each([
    ['Close', (dialog) => within(dialog).getByRole('button', { name: 'Close' })],
    ['the close icon', (dialog) => dialog.querySelector('h2 + button')],
  ])('%s closes the modal', async (_label, button) => {
    await renderWith();
    await openPermissions('empty');
    fireEvent.click(button(modal('Group Permissions: empty')));
    expect(screen.queryByRole('heading', { name: 'Group Permissions: empty' })).toBeNull();
    expect(mddbClient.getGroupPermissions).toHaveBeenCalledWith('empty');
  });
});
