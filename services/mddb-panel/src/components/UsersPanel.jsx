import { useEffect, useState } from 'react';
import { Users, Shield, Clock, Plus, Trash2, Key, X } from 'lucide-react';
import { useStore } from '../lib/store';
import mddbClient from '../lib/mddb-client';
import { formatDistanceToNow } from 'date-fns';
import PermissionScopeHint from './PermissionScopeHint';
import PermissionList from './PermissionList';

export default function UsersPanel() {
  const {
    users,
    usersLoading,
    usersError,
    setUsers,
    setUsersLoading,
    setUsersError,
  } = useStore();

  const [showCreateModal, setShowCreateModal] = useState(false);
  const [showDeleteModal, setShowDeleteModal] = useState(false);
  const [showPermissionsModal, setShowPermissionsModal] = useState(false);
  const [selectedUser, setSelectedUser] = useState(null);

  useEffect(() => {
    loadUsers();
  }, []);

  const loadUsers = async () => {
    setUsersLoading(true);
    setUsersError(null);
    try {
      const data = await mddbClient.listUsers();
      setUsers(data.users || []);
    } catch (error) {
      setUsersError(error.message);
      console.error('Failed to load users:', error);
    } finally {
      setUsersLoading(false);
    }
  };

  // Only the confirmation calls it, and that renders only with a user selected.
  const handleDeleteUser = async () => {
    try {
      await mddbClient.deleteUser(selectedUser.username);
      setShowDeleteModal(false);
      setSelectedUser(null);
      loadUsers();
    } catch (error) {
      alert(`Failed to delete user: ${error.message}`);
    }
  };

  const formatDate = (timestamp) => {
    if (!timestamp) return 'N/A';
    const date = new Date(timestamp * 1000);
    return formatDistanceToNow(date, { addSuffix: true });
  };

  if (usersLoading) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="animate-spin rounded-full h-12 w-12 border-b-2 border-blue-600"></div>
      </div>
    );
  }

  if (usersError) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="text-center">
          <div className="text-danger font-medium mb-2">Failed to load users</div>
          <div className="text-sm text-fg-faint">{usersError}</div>
          <button
            onClick={loadUsers}
            className="mt-4 px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700"
          >
            Retry
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className="h-full flex flex-col bg-surface-muted">
      <div className="bg-surface border-b px-6 py-4">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-bold text-fg mb-1">User Management</h1>
            <p className="text-fg-subtle">Manage users and their permissions</p>
          </div>
          <div className="flex gap-2">
            <button
              onClick={loadUsers}
              className="px-4 py-2 bg-surface-subtle text-fg-muted rounded hover:bg-surface-strong"
            >
              Refresh
            </button>
            <button
              onClick={() => setShowCreateModal(true)}
              className="px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700 flex items-center gap-2"
            >
              <Plus className="w-4 h-4" />
              Create User
            </button>
          </div>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto p-6">
        <div className="max-w-6xl mx-auto">
          <div className="bg-surface rounded-lg shadow overflow-hidden">
            <table className="min-w-full divide-y divide-line">
              <thead className="bg-surface-muted">
                <tr>
                  <th className="px-6 py-3 text-left text-xs font-medium text-fg-faint uppercase tracking-wider">
                    Username
                  </th>
                  <th className="px-6 py-3 text-left text-xs font-medium text-fg-faint uppercase tracking-wider">
                    Created
                  </th>
                  <th className="px-6 py-3 text-left text-xs font-medium text-fg-faint uppercase tracking-wider">
                    Role
                  </th>
                  <th className="px-6 py-3 text-left text-xs font-medium text-fg-faint uppercase tracking-wider">
                    Groups
                  </th>
                  <th className="px-6 py-3 text-left text-xs font-medium text-fg-faint uppercase tracking-wider">
                    Status
                  </th>
                  <th className="px-6 py-3 text-right text-xs font-medium text-fg-faint uppercase tracking-wider">
                    Actions
                  </th>
                </tr>
              </thead>
              <tbody className="bg-surface divide-y divide-line">
                {users.length === 0 ? (
                  <tr>
                    <td colSpan="6" className="px-6 py-12 text-center text-fg-faint">
                      <Users className="w-12 h-12 mx-auto mb-2 text-fg-faint" />
                      <div>No users found</div>
                    </td>
                  </tr>
                ) : (
                  users.map((user) => (
                    <tr key={user.username} className="hover:bg-surface-muted">
                      <td className="px-6 py-4 whitespace-nowrap">
                        <div className="flex items-center">
                          <div className="flex-shrink-0 h-8 w-8 bg-info-muted rounded-full flex items-center justify-center">
                            <span className="text-info font-medium text-sm">
                              {user.username.charAt(0).toUpperCase()}
                            </span>
                          </div>
                          <div className="ml-3">
                            <div className="text-sm font-medium text-fg">{user.username}</div>
                          </div>
                        </div>
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap">
                        <div className="flex items-center text-sm text-fg-subtle">
                          <Clock className="w-4 h-4 mr-1" />
                          {formatDate(user.createdAt)}
                        </div>
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap">
                        {user.admin ? (
                          <span className="flex items-center px-2 py-1 text-xs font-medium rounded-full bg-note-muted text-note-strong w-fit">
                            <Shield className="w-3 h-3 mr-1" />
                            Admin
                          </span>
                        ) : (
                          <span className="px-2 py-1 text-xs font-medium rounded-full bg-surface-subtle text-fg-soft">
                            User
                          </span>
                        )}
                      </td>
                      <td className="px-6 py-4">
                        <div className="flex flex-wrap gap-1">
                          {user.groups && user.groups.length > 0 ? (
                            user.groups.map((group) => (
                              <span
                                key={group}
                                className="px-2 py-1 text-xs rounded-full bg-info-muted text-info-strong"
                              >
                                {group}
                              </span>
                            ))
                          ) : (
                            <span className="text-sm text-fg-faint">No groups</span>
                          )}
                        </div>
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap">
                        {user.disabled ? (
                          <span className="px-2 py-1 text-xs font-medium rounded-full bg-danger-muted text-danger-strong">
                            Disabled
                          </span>
                        ) : (
                          <span className="px-2 py-1 text-xs font-medium rounded-full bg-success-muted text-success-strong">
                            Active
                          </span>
                        )}
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap text-right text-sm font-medium">
                        <button
                          onClick={() => {
                            setSelectedUser(user);
                            setShowPermissionsModal(true);
                          }}
                          className="text-info hover:text-info-strong mr-3"
                          title="Manage Permissions"
                        >
                          <Key className="w-4 h-4" />
                        </button>
                        <button
                          onClick={() => {
                            setSelectedUser(user);
                            setShowDeleteModal(true);
                          }}
                          className="text-danger hover:text-danger-strong"
                          title="Delete User"
                        >
                          <Trash2 className="w-4 h-4" />
                        </button>
                      </td>
                    </tr>
                  ))
                )}
              </tbody>
            </table>
          </div>
        </div>
      </div>

      {/* Create User Modal */}
      {showCreateModal && (
        <CreateUserModal
          onClose={() => setShowCreateModal(false)}
          onSuccess={() => {
            setShowCreateModal(false);
            loadUsers();
          }}
        />
      )}

      {/* Delete Confirmation Modal */}
      {showDeleteModal && selectedUser && (
        <DeleteConfirmModal
          title="Delete User"
          message={`Are you sure you want to delete user "${selectedUser.username}"? This action cannot be undone.`}
          onConfirm={handleDeleteUser}
          onCancel={() => {
            setShowDeleteModal(false);
            setSelectedUser(null);
          }}
        />
      )}

      {/* Permissions Modal */}
      {showPermissionsModal && selectedUser && (
        <PermissionsModal
          user={selectedUser}
          onClose={() => {
            setShowPermissionsModal(false);
            setSelectedUser(null);
          }}
        />
      )}
    </div>
  );
}

// Create User Modal Component
function CreateUserModal({ onClose, onSuccess }) {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [isAdmin, setIsAdmin] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);

  const handleSubmit = async (e) => {
    e.preventDefault();
    setError(null);
    setLoading(true);

    try {
      await mddbClient.createUser({ username, password, admin: isAdmin });
      onSuccess();
    } catch (err) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
      <div className="bg-surface rounded-lg shadow-xl w-full max-w-md p-6">
        <div className="flex items-center justify-between mb-4">
          <h2 className="text-xl font-bold text-fg">Create New User</h2>
          <button onClick={onClose} className="text-fg-faint hover:text-fg-subtle">
            <X className="w-5 h-5" />
          </button>
        </div>

        <form onSubmit={handleSubmit}>
          <div className="space-y-4">
            <div>
              <label htmlFor="create-user-username" className="block text-sm font-medium text-fg-muted mb-1">
                Username
              </label>
              <input
                id="create-user-username"
                type="text"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                className="w-full px-3 py-2 border border-line-strong rounded-md focus:outline-none focus:ring-2 focus:ring-focus"
                required
              />
            </div>

            <div>
              <label htmlFor="create-user-password" className="block text-sm font-medium text-fg-muted mb-1">
                Password
              </label>
              <input
                id="create-user-password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                className="w-full px-3 py-2 border border-line-strong rounded-md focus:outline-none focus:ring-2 focus:ring-focus"
                required
                minLength={6}
              />
            </div>

            <div className="flex items-center">
              <input
                type="checkbox"
                id="isAdmin"
                checked={isAdmin}
                onChange={(e) => setIsAdmin(e.target.checked)}
                className="h-4 w-4 text-info focus:ring-focus border-line-strong rounded"
              />
              <label htmlFor="isAdmin" className="ml-2 block text-sm text-fg-muted">
                Administrator privileges (admin on &quot;*&quot;, server-wide)
              </label>
            </div>

            {error && (
              <div className="bg-danger-soft border border-danger-line text-danger px-4 py-2 rounded text-sm">
                {error}
              </div>
            )}
          </div>

          <div className="mt-6 flex gap-3">
            <button
              type="button"
              onClick={onClose}
              className="flex-1 px-4 py-2 border border-line-strong text-fg-muted rounded-md hover:bg-surface-muted"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={loading}
              className="flex-1 px-4 py-2 bg-blue-600 text-white rounded-md hover:bg-blue-700 disabled:opacity-50"
            >
              {loading ? 'Creating...' : 'Create User'}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

// Delete Confirmation Modal Component
function DeleteConfirmModal({ title, message, onConfirm, onCancel }) {
  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
      <div className="bg-surface rounded-lg shadow-xl w-full max-w-md p-6">
        <h2 className="text-xl font-bold text-fg mb-4">{title}</h2>
        <p className="text-fg-subtle mb-6">{message}</p>
        <div className="flex gap-3">
          <button
            onClick={onCancel}
            className="flex-1 px-4 py-2 border border-line-strong text-fg-muted rounded-md hover:bg-surface-muted"
          >
            Cancel
          </button>
          <button
            onClick={onConfirm}
            className="flex-1 px-4 py-2 bg-red-600 text-white rounded-md hover:bg-red-700"
          >
            Delete
          </button>
        </div>
      </div>
    </div>
  );
}

// Permissions Modal Component
function PermissionsModal({ user, onClose }) {
  const [permissions, setPermissions] = useState([]);
  const [loading, setLoading] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [success, setSuccess] = useState(false);
  const [error, setError] = useState(null);
  const [collection, setCollection] = useState('*');
  const [read, setRead] = useState(true);
  const [write, setWrite] = useState(false);
  const [admin, setAdmin] = useState(false);

  useEffect(() => {
    loadPermissions();
  }, []);

  const loadPermissions = async () => {
    setLoading(true);
    try {
      const data = await mddbClient.getUserPermissions(user.username);
      // Backend returns array directly, not wrapped in object
      setPermissions(Array.isArray(data) ? data : (data.permissions || []));
    } catch (error) {
      console.error('Failed to load permissions:', error);
      setError('Failed to load permissions');
    } finally {
      setLoading(false);
    }
  };

  const handleAddPermission = async (e) => {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    setSuccess(false);

    try {
      await mddbClient.setUserPermission({
        username: user.username,
        collection,
        read,
        write,
        admin,
      });

      // Show success message
      setSuccess(true);

      // Reset form
      setCollection('*');
      setRead(true);
      setWrite(false);
      setAdmin(false);

      // Reload permissions
      await loadPermissions();

      // Hide success message after 2 seconds
      setTimeout(() => setSuccess(false), 2000);
    } catch (error) {
      console.error('Permission error:', error);
      setError(error.message || 'Failed to set permission');
    } finally {
      setSubmitting(false);
    }
  };

  // Removes one grant (#438), then lists what is left.
  const handleRemovePermission = async (permCollection) => {
    setError(null);
    try {
      await mddbClient.deleteUserPermission(user.username, permCollection);
      await loadPermissions();
    } catch (error) {
      console.error('Permission error:', error);
      setError(error.message || 'Failed to remove permission');
    }
  };

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
      <div className="bg-surface rounded-lg shadow-xl w-full max-w-2xl p-6 max-h-[80vh] overflow-y-auto">
        <div className="flex items-center justify-between mb-4">
          <h2 className="text-xl font-bold text-fg">
            Manage Permissions: {user.username}
          </h2>
          <button onClick={onClose} className="text-fg-faint hover:text-fg-subtle">
            <X className="w-5 h-5" />
          </button>
        </div>

        {/* Existing Permissions */}
        <div className="mb-6">
          <h3 className="text-sm font-medium text-fg-muted mb-2">Current Permissions</h3>
          <PermissionList permissions={permissions} loading={loading} onRemove={handleRemovePermission} />
        </div>

        {/* Add New Permission */}
        <div>
          <h3 className="text-sm font-medium text-fg-muted mb-2">Add New Permission</h3>
          <form onSubmit={handleAddPermission} className="space-y-4">
            <div>
              <label htmlFor="user-permission-collection" className="block text-sm font-medium text-fg-muted mb-1">
                Collection (* for all)
              </label>
              <input
                id="user-permission-collection"
                type="text"
                value={collection}
                onChange={(e) => setCollection(e.target.value)}
                className="w-full px-3 py-2 border border-line-strong rounded-md focus:outline-none focus:ring-2 focus:ring-focus"
                required
              />
            </div>

            <div className="space-y-2">
              <label className="block text-sm font-medium text-fg-muted">Permissions</label>
              <div className="flex items-center">
                <input
                  type="checkbox"
                  id="read"
                  checked={read}
                  onChange={(e) => setRead(e.target.checked)}
                  className="h-4 w-4 text-info focus:ring-focus border-line-strong rounded"
                />
                <label htmlFor="read" className="ml-2 block text-sm text-fg-muted">
                  Read
                </label>
              </div>
              <div className="flex items-center">
                <input
                  type="checkbox"
                  id="write"
                  checked={write}
                  onChange={(e) => setWrite(e.target.checked)}
                  className="h-4 w-4 text-info focus:ring-focus border-line-strong rounded"
                />
                <label htmlFor="write" className="ml-2 block text-sm text-fg-muted">
                  Write
                </label>
              </div>
              <div className="flex items-center">
                <input
                  type="checkbox"
                  id="admin"
                  checked={admin}
                  onChange={(e) => setAdmin(e.target.checked)}
                  className="h-4 w-4 text-info focus:ring-focus border-line-strong rounded"
                />
                <label htmlFor="admin" className="ml-2 block text-sm text-fg-muted">
                  Admin
                </label>
              </div>
              <PermissionScopeHint collection={collection} admin={admin} />
            </div>

            {success && (
              <div className="bg-success-soft border border-success-line text-success px-4 py-2 rounded text-sm">
                ✓ Permission added successfully!
              </div>
            )}

            {error && (
              <div className="bg-danger-soft border border-danger-line text-danger px-4 py-2 rounded text-sm">
                {error}
              </div>
            )}

            <button
              type="submit"
              disabled={submitting}
              className="w-full px-4 py-2 bg-blue-600 text-white rounded-md hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed"
            >
              {submitting ? 'Adding...' : 'Add Permission'}
            </button>
          </form>
        </div>

        <div className="mt-6">
          <button
            onClick={onClose}
            className="w-full px-4 py-2 border border-line-strong text-fg-muted rounded-md hover:bg-surface-muted"
          >
            Close
          </button>
        </div>
      </div>
    </div>
  );
}
