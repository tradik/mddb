import { useEffect, useState } from 'react';
import { Users, Clock, Plus, Edit2, Trash2, Key, X } from 'lucide-react';
import { useStore } from '../lib/store';
import mddbClient from '../lib/mddb-client';
import { formatDistanceToNow } from 'date-fns';
import { adminScopeLabel } from '../lib/permissions';
import PermissionScopeHint from './PermissionScopeHint';

export default function GroupsPanel() {
  const {
    groups,
    groupsLoading,
    groupsError,
    users,
    setGroups,
    setGroupsLoading,
    setGroupsError,
  } = useStore();

  const [showCreateModal, setShowCreateModal] = useState(false);
  const [showEditModal, setShowEditModal] = useState(false);
  const [showDeleteModal, setShowDeleteModal] = useState(false);
  const [showPermissionsModal, setShowPermissionsModal] = useState(false);
  const [selectedGroup, setSelectedGroup] = useState(null);

  useEffect(() => {
    loadGroups();
    loadUsersIfNeeded();
  }, []);

  const loadGroups = async () => {
    setGroupsLoading(true);
    setGroupsError(null);
    try {
      const data = await mddbClient.listGroups();
      setGroups(data.groups || []);
    } catch (error) {
      setGroupsError(error.message);
      console.error('Failed to load groups:', error);
    } finally {
      setGroupsLoading(false);
    }
  };

  const loadUsersIfNeeded = async () => {
    if (users.length === 0) {
      try {
        const data = await mddbClient.listUsers();
        useStore.getState().setUsers(data.users || []);
      } catch (error) {
        console.error('Failed to load users:', error);
      }
    }
  };

  const handleDeleteGroup = async () => {
    if (!selectedGroup) return;
    try {
      await mddbClient.deleteGroup(selectedGroup.name);
      setShowDeleteModal(false);
      setSelectedGroup(null);
      loadGroups();
    } catch (error) {
      alert(`Failed to delete group: ${error.message}`);
    }
  };

  const formatDate = (timestamp) => {
    if (!timestamp) return 'N/A';
    const date = new Date(timestamp * 1000);
    return formatDistanceToNow(date, { addSuffix: true });
  };

  if (groupsLoading) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="animate-spin rounded-full h-12 w-12 border-b-2 border-primary-600"></div>
      </div>
    );
  }

  if (groupsError) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="text-center">
          <div className="text-red-600 font-medium mb-2">Failed to load groups</div>
          <div className="text-sm text-gray-500">{groupsError}</div>
          <button
            onClick={loadGroups}
            className="mt-4 px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700"
          >
            Retry
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className="h-full flex flex-col bg-gray-50">
      <div className="bg-white border-b px-6 py-4">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-bold text-gray-900 mb-1">Group Management</h1>
            <p className="text-gray-600">Manage user groups and group permissions</p>
          </div>
          <div className="flex gap-2">
            <button
              onClick={loadGroups}
              className="px-4 py-2 bg-gray-100 text-gray-700 rounded hover:bg-gray-200"
            >
              Refresh
            </button>
            <button
              onClick={() => setShowCreateModal(true)}
              className="px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700 flex items-center gap-2"
            >
              <Plus className="w-4 h-4" />
              Create Group
            </button>
          </div>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto p-6">
        <div className="max-w-6xl mx-auto">
          {groups.length === 0 ? (
            <div className="bg-white rounded-lg shadow p-12 text-center">
              <Users className="w-16 h-16 mx-auto mb-4 text-gray-400" />
              <h3 className="text-lg font-medium text-gray-900 mb-2">No groups yet</h3>
              <p className="text-gray-600 mb-6">
                Create groups to organize users and manage permissions efficiently.
              </p>
              <button
                onClick={() => setShowCreateModal(true)}
                className="px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700"
              >
                Create Your First Group
              </button>
            </div>
          ) : (
            <div className="grid gap-4">
              {groups.map((group) => (
                <div key={group.name} className="bg-white rounded-lg shadow p-6">
                  <div className="flex items-start justify-between mb-4">
                    <div className="flex-1">
                      <h3 className="text-lg font-semibold text-gray-900 mb-1">{group.name}</h3>
                      {group.description && (
                        <p className="text-sm text-gray-600">{group.description}</p>
                      )}
                    </div>
                    <div className="flex items-center gap-2">
                      <div className="flex items-center text-sm text-gray-500 mr-3">
                        <Clock className="w-4 h-4 mr-1" />
                        {formatDate(group.createdAt)}
                      </div>
                      <button
                        onClick={() => {
                          setSelectedGroup(group);
                          setShowPermissionsModal(true);
                        }}
                        className="text-primary-600 hover:text-primary-900 p-1"
                        title="Manage Permissions"
                      >
                        <Key className="w-4 h-4" />
                      </button>
                      <button
                        onClick={() => {
                          setSelectedGroup(group);
                          setShowEditModal(true);
                        }}
                        className="text-blue-600 hover:text-blue-900 p-1"
                        title="Edit Group"
                      >
                        <Edit2 className="w-4 h-4" />
                      </button>
                      <button
                        onClick={() => {
                          setSelectedGroup(group);
                          setShowDeleteModal(true);
                        }}
                        className="text-red-600 hover:text-red-900 p-1"
                        title="Delete Group"
                      >
                        <Trash2 className="w-4 h-4" />
                      </button>
                    </div>
                  </div>

                  <div className="border-t pt-4">
                    <div className="flex items-center justify-between">
                      <div>
                        <span className="text-sm text-gray-600">Members:</span>
                        <span className="ml-2 text-sm font-medium text-gray-900">
                          {group.members?.length || 0}
                        </span>
                      </div>
                      <div className="flex flex-wrap gap-2">
                        {group.members && group.members.length > 0 ? (
                          group.members.slice(0, 5).map((member) => (
                            <span
                              key={member}
                              className="px-2 py-1 text-xs rounded-full bg-primary-100 text-primary-800"
                            >
                              {member}
                            </span>
                          ))
                        ) : (
                          <span className="text-sm text-gray-400">No members</span>
                        )}
                        {group.members && group.members.length > 5 && (
                          <span className="px-2 py-1 text-xs rounded-full bg-gray-100 text-gray-600">
                            +{group.members.length - 5} more
                          </span>
                        )}
                      </div>
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>

      {/* Create Group Modal */}
      {showCreateModal && (
        <CreateGroupModal
          users={users}
          onClose={() => setShowCreateModal(false)}
          onSuccess={() => {
            setShowCreateModal(false);
            loadGroups();
          }}
        />
      )}

      {/* Edit Group Modal */}
      {showEditModal && selectedGroup && (
        <EditGroupModal
          group={selectedGroup}
          users={users}
          onClose={() => {
            setShowEditModal(false);
            setSelectedGroup(null);
          }}
          onSuccess={() => {
            setShowEditModal(false);
            setSelectedGroup(null);
            loadGroups();
          }}
        />
      )}

      {/* Delete Confirmation Modal */}
      {showDeleteModal && selectedGroup && (
        <DeleteConfirmModal
          title="Delete Group"
          message={`Are you sure you want to delete group "${selectedGroup.name}"? This will remove all group permissions but will not delete the users.`}
          onConfirm={handleDeleteGroup}
          onCancel={() => {
            setShowDeleteModal(false);
            setSelectedGroup(null);
          }}
        />
      )}

      {/* Permissions Modal */}
      {showPermissionsModal && selectedGroup && (
        <GroupPermissionsModal
          group={selectedGroup}
          onClose={() => {
            setShowPermissionsModal(false);
            setSelectedGroup(null);
          }}
        />
      )}
    </div>
  );
}

// Create Group Modal Component
function CreateGroupModal({ users, onClose, onSuccess }) {
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [selectedMembers, setSelectedMembers] = useState([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);

  const toggleMember = (username) => {
    setSelectedMembers((prev) =>
      prev.includes(username)
        ? prev.filter((u) => u !== username)
        : [...prev, username]
    );
  };

  const handleSubmit = async (e) => {
    e.preventDefault();
    setError(null);
    setLoading(true);

    try {
      await mddbClient.createGroup({
        name,
        description,
        members: selectedMembers,
      });
      onSuccess();
    } catch (err) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50">
      <div className="bg-white rounded-lg shadow-xl w-full max-w-md p-6 max-h-[80vh] overflow-y-auto">
        <div className="flex items-center justify-between mb-4">
          <h2 className="text-xl font-bold text-gray-900">Create New Group</h2>
          <button onClick={onClose} className="text-gray-400 hover:text-gray-600">
            <X className="w-5 h-5" />
          </button>
        </div>

        <form onSubmit={handleSubmit}>
          <div className="space-y-4">
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">
                Group Name
              </label>
              <input
                type="text"
                value={name}
                onChange={(e) => setName(e.target.value)}
                className="w-full px-3 py-2 border border-gray-300 rounded-md focus:outline-none focus:ring-2 focus:ring-primary-500"
                required
              />
            </div>

            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">
                Description
              </label>
              <textarea
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                rows="3"
                className="w-full px-3 py-2 border border-gray-300 rounded-md focus:outline-none focus:ring-2 focus:ring-primary-500"
              />
            </div>

            <div>
              <label className="block text-sm font-medium text-gray-700 mb-2">
                Members ({selectedMembers.length})
              </label>
              <div className="border border-gray-300 rounded-md max-h-48 overflow-y-auto">
                {users.length === 0 ? (
                  <div className="text-sm text-gray-500 p-3">No users available</div>
                ) : (
                  users.map((user) => (
                    <label
                      key={user.username}
                      className="flex items-center p-2 hover:bg-gray-50 cursor-pointer"
                    >
                      <input
                        type="checkbox"
                        checked={selectedMembers.includes(user.username)}
                        onChange={() => toggleMember(user.username)}
                        className="h-4 w-4 text-blue-600 focus:ring-blue-500 border-gray-300 rounded"
                      />
                      <span className="ml-2 text-sm text-gray-700">{user.username}</span>
                      {user.admin && (
                        <span className="ml-2 text-xs px-2 py-0.5 rounded-full bg-purple-100 text-purple-800">
                          Admin
                        </span>
                      )}
                    </label>
                  ))
                )}
              </div>
            </div>

            {error && (
              <div className="bg-red-50 border border-red-200 text-red-600 px-4 py-2 rounded text-sm">
                {error}
              </div>
            )}
          </div>

          <div className="mt-6 flex gap-3">
            <button
              type="button"
              onClick={onClose}
              className="flex-1 px-4 py-2 border border-gray-300 text-gray-700 rounded-md hover:bg-gray-50"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={loading}
              className="flex-1 px-4 py-2 bg-blue-600 text-white rounded-md hover:bg-blue-700 disabled:opacity-50"
            >
              {loading ? 'Creating...' : 'Create Group'}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

// Edit Group Modal Component
function EditGroupModal({ group, users, onClose, onSuccess }) {
  const [description, setDescription] = useState(group.description || '');
  const [selectedMembers, setSelectedMembers] = useState(group.members || []);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);

  const toggleMember = (username) => {
    setSelectedMembers((prev) =>
      prev.includes(username)
        ? prev.filter((u) => u !== username)
        : [...prev, username]
    );
  };

  const handleSubmit = async (e) => {
    e.preventDefault();
    setError(null);
    setLoading(true);

    try {
      await mddbClient.updateGroup(group.name, {
        description,
        members: selectedMembers,
      });
      onSuccess();
    } catch (err) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50">
      <div className="bg-white rounded-lg shadow-xl w-full max-w-md p-6 max-h-[80vh] overflow-y-auto">
        <div className="flex items-center justify-between mb-4">
          <h2 className="text-xl font-bold text-gray-900">Edit Group: {group.name}</h2>
          <button onClick={onClose} className="text-gray-400 hover:text-gray-600">
            <X className="w-5 h-5" />
          </button>
        </div>

        <form onSubmit={handleSubmit}>
          <div className="space-y-4">
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">
                Description
              </label>
              <textarea
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                rows="3"
                className="w-full px-3 py-2 border border-gray-300 rounded-md focus:outline-none focus:ring-2 focus:ring-primary-500"
              />
            </div>

            <div>
              <label className="block text-sm font-medium text-gray-700 mb-2">
                Members ({selectedMembers.length})
              </label>
              <div className="border border-gray-300 rounded-md max-h-48 overflow-y-auto">
                {users.length === 0 ? (
                  <div className="text-sm text-gray-500 p-3">No users available</div>
                ) : (
                  users.map((user) => (
                    <label
                      key={user.username}
                      className="flex items-center p-2 hover:bg-gray-50 cursor-pointer"
                    >
                      <input
                        type="checkbox"
                        checked={selectedMembers.includes(user.username)}
                        onChange={() => toggleMember(user.username)}
                        className="h-4 w-4 text-blue-600 focus:ring-blue-500 border-gray-300 rounded"
                      />
                      <span className="ml-2 text-sm text-gray-700">{user.username}</span>
                      {user.admin && (
                        <span className="ml-2 text-xs px-2 py-0.5 rounded-full bg-purple-100 text-purple-800">
                          Admin
                        </span>
                      )}
                    </label>
                  ))
                )}
              </div>
            </div>

            {error && (
              <div className="bg-red-50 border border-red-200 text-red-600 px-4 py-2 rounded text-sm">
                {error}
              </div>
            )}
          </div>

          <div className="mt-6 flex gap-3">
            <button
              type="button"
              onClick={onClose}
              className="flex-1 px-4 py-2 border border-gray-300 text-gray-700 rounded-md hover:bg-gray-50"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={loading}
              className="flex-1 px-4 py-2 bg-blue-600 text-white rounded-md hover:bg-blue-700 disabled:opacity-50"
            >
              {loading ? 'Saving...' : 'Save Changes'}
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
    <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50">
      <div className="bg-white rounded-lg shadow-xl w-full max-w-md p-6">
        <h2 className="text-xl font-bold text-gray-900 mb-4">{title}</h2>
        <p className="text-gray-600 mb-6">{message}</p>
        <div className="flex gap-3">
          <button
            onClick={onCancel}
            className="flex-1 px-4 py-2 border border-gray-300 text-gray-700 rounded-md hover:bg-gray-50"
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

// Group Permissions Modal Component
function GroupPermissionsModal({ group, onClose }) {
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
      const data = await mddbClient.getGroupPermissions(group.name);
      setPermissions(data.permissions || []);
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
      await mddbClient.setGroupPermission({
        groupName: group.name,
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

  return (
    <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50">
      <div className="bg-white rounded-lg shadow-xl w-full max-w-2xl p-6 max-h-[80vh] overflow-y-auto">
        <div className="flex items-center justify-between mb-4">
          <h2 className="text-xl font-bold text-gray-900">
            Group Permissions: {group.name}
          </h2>
          <button onClick={onClose} className="text-gray-400 hover:text-gray-600">
            <X className="w-5 h-5" />
          </button>
        </div>

        {/* Existing Permissions */}
        <div className="mb-6">
          <h3 className="text-sm font-medium text-gray-700 mb-2">Current Permissions</h3>
          {loading ? (
            <div className="text-center py-4">Loading...</div>
          ) : permissions.length === 0 ? (
            <div className="text-sm text-gray-500 bg-gray-50 rounded p-4">
              No permissions set
            </div>
          ) : (
            <div className="space-y-2">
              {permissions.map((perm, idx) => (
                <div key={idx} className="bg-gray-50 rounded p-3">
                  <div>
                    <span className="font-medium text-gray-900">{perm.collection}</span>
                    <div className="flex gap-2 mt-1">
                      {perm.read && (
                        <span className="px-2 py-0.5 text-xs bg-green-100 text-green-800 rounded">
                          Read
                        </span>
                      )}
                      {perm.write && (
                        <span className="px-2 py-0.5 text-xs bg-blue-100 text-blue-800 rounded">
                          Write
                        </span>
                      )}
                      {perm.admin && (
                        <span className="px-2 py-0.5 text-xs bg-purple-100 text-purple-800 rounded">
                          {adminScopeLabel(perm.collection)}
                        </span>
                      )}
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>

        {/* Add New Permission */}
        <div>
          <h3 className="text-sm font-medium text-gray-700 mb-2">Add New Permission</h3>
          <form onSubmit={handleAddPermission} className="space-y-4">
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">
                Collection (* for all)
              </label>
              <input
                type="text"
                value={collection}
                onChange={(e) => setCollection(e.target.value)}
                className="w-full px-3 py-2 border border-gray-300 rounded-md focus:outline-none focus:ring-2 focus:ring-primary-500"
                required
              />
            </div>

            <div className="space-y-2">
              <label className="block text-sm font-medium text-gray-700">Permissions</label>
              <div className="flex items-center">
                <input
                  type="checkbox"
                  id="groupRead"
                  checked={read}
                  onChange={(e) => setRead(e.target.checked)}
                  className="h-4 w-4 text-blue-600 focus:ring-blue-500 border-gray-300 rounded"
                />
                <label htmlFor="groupRead" className="ml-2 block text-sm text-gray-700">
                  Read
                </label>
              </div>
              <div className="flex items-center">
                <input
                  type="checkbox"
                  id="groupWrite"
                  checked={write}
                  onChange={(e) => setWrite(e.target.checked)}
                  className="h-4 w-4 text-blue-600 focus:ring-blue-500 border-gray-300 rounded"
                />
                <label htmlFor="groupWrite" className="ml-2 block text-sm text-gray-700">
                  Write
                </label>
              </div>
              <div className="flex items-center">
                <input
                  type="checkbox"
                  id="groupAdmin"
                  checked={admin}
                  onChange={(e) => setAdmin(e.target.checked)}
                  className="h-4 w-4 text-blue-600 focus:ring-blue-500 border-gray-300 rounded"
                />
                <label htmlFor="groupAdmin" className="ml-2 block text-sm text-gray-700">
                  Admin
                </label>
              </div>
              <PermissionScopeHint collection={collection} admin={admin} />
            </div>

            {success && (
              <div className="bg-green-50 border border-green-200 text-green-700 px-4 py-2 rounded text-sm">
                ✓ Permission added successfully!
              </div>
            )}

            {error && (
              <div className="bg-red-50 border border-red-200 text-red-600 px-4 py-2 rounded text-sm">
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
            className="w-full px-4 py-2 border border-gray-300 text-gray-700 rounded-md hover:bg-gray-50"
          >
            Close
          </button>
        </div>
      </div>
    </div>
  );
}
