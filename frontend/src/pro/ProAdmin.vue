<script setup lang="ts">
/**
 * DreamTrans Pro - Admin Panel
 */
import { ref, onMounted, computed } from 'vue'
import { Shield, Users, Settings, ChevronLeft, RefreshCw, Trash2, Check, X } from 'lucide-vue-next'
import { useAuth } from './composables/useAuth'
import * as authApi from './api/auth'

const { user, isAuthenticated, isAdmin, init: initAuth } = useAuth()

// State
const loading = ref(false)
const users = ref<authApi.User[]>([])
const systemSettings = ref({ allow_user_api_key: false })
const error = ref('')
const successMsg = ref('')

// Fetch users
async function fetchUsers() {
  loading.value = true
  error.value = ''
  try {
    const res = await authApi.adminListUsers()
    users.value = res.users || []
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to fetch users'
  } finally {
    loading.value = false
  }
}

// Fetch system settings
async function fetchSystemSettings() {
  try {
    const res = await fetch('/api/system/settings')
    if (res.ok) {
      systemSettings.value = await res.json()
    }
  } catch {
    // ignore
  }
}

// Toggle user active status
async function toggleUserActive(u: authApi.User) {
  try {
    await authApi.adminUpdateUser(u.id, { is_active: !u.is_active })
    await fetchUsers()
    successMsg.value = `User ${u.email} ${u.is_active ? 'disabled' : 'enabled'}`
    setTimeout(() => successMsg.value = '', 3000)
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to update user'
  }
}

// Delete user
async function deleteUser(u: authApi.User) {
  if (!confirm(`Delete user ${u.email}? This cannot be undone.`)) return
  try {
    await authApi.adminDeleteUser(u.id)
    await fetchUsers()
    successMsg.value = `User ${u.email} deleted`
    setTimeout(() => successMsg.value = '', 3000)
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to delete user'
  }
}

// Update system settings
async function updateSystemSettings() {
  try {
    const res = await fetch('/api/admin/settings', {
      method: 'PUT',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${authApi.getAccessToken()}`,
      },
      body: JSON.stringify(systemSettings.value),
    })
    if (!res.ok) throw new Error('Failed to update settings')
    successMsg.value = 'Settings updated'
    setTimeout(() => successMsg.value = '', 3000)
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to update settings'
  }
}

// Navigation
function goBack() {
  window.location.href = '/pro'
}

// Computed
const userCount = computed(() => users.value.length)
const activeUserCount = computed(() => users.value.filter(u => u.is_active).length)

// Init
onMounted(async () => {
  await initAuth()
  if (!isAdmin.value) {
    window.location.href = '/pro'
    return
  }
  await Promise.all([fetchUsers(), fetchSystemSettings()])
})
</script>

<template>
  <div class="admin-container">
    <!-- Header -->
    <header class="admin-header">
      <div class="header-left">
        <button class="back-btn" @click="goBack">
          <ChevronLeft :size="16" />
          <span>返回</span>
        </button>
        <h1>
          <Shield :size="24" />
          管理面板
        </h1>
      </div>
      <div class="header-right">
        <span v-if="user" class="user-info">{{ user.email }}</span>
      </div>
    </header>

    <!-- Main Content -->
    <main class="admin-main">
      <!-- Error/Success Messages -->
      <div v-if="error" class="alert alert-error">{{ error }}</div>
      <div v-if="successMsg" class="alert alert-success">{{ successMsg }}</div>

      <!-- Stats Cards -->
      <div class="stats-row">
        <div class="stat-card">
          <Users :size="24" />
          <div class="stat-info">
            <span class="stat-value">{{ userCount }}</span>
            <span class="stat-label">Total Users</span>
          </div>
        </div>
        <div class="stat-card">
          <Check :size="24" />
          <div class="stat-info">
            <span class="stat-value">{{ activeUserCount }}</span>
            <span class="stat-label">Active Users</span>
          </div>
        </div>
      </div>

      <!-- Users Section -->
      <section class="admin-section">
        <div class="section-header">
          <h2><Users :size="18" /> 用户管理</h2>
          <button class="icon-btn" @click="fetchUsers" :disabled="loading">
            <RefreshCw :size="16" :class="{ spinning: loading }" />
          </button>
        </div>
        <div class="users-table">
          <table>
            <thead>
              <tr>
                <th>Email</th>
                <th>Name</th>
                <th>Role</th>
                <th>Status</th>
                <th>Created</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="u in users" :key="u.id" :class="{ inactive: !u.is_active }">
                <td>{{ u.email }}</td>
                <td>{{ u.name || '-' }}</td>
                <td><span class="badge" :class="u.role">{{ u.role }}</span></td>
                <td>
                  <span class="status" :class="u.is_active ? 'active' : 'disabled'">
                    {{ u.is_active ? 'Active' : 'Disabled' }}
                  </span>
                </td>
                <td>{{ new Date(u.created_at).toLocaleDateString() }}</td>
                <td class="actions">
                  <button
                    class="action-btn"
                    @click="toggleUserActive(u)"
                    :title="u.is_active ? 'Disable' : 'Enable'"
                  >
                    <X v-if="u.is_active" :size="14" />
                    <Check v-else :size="14" />
                  </button>
                  <button
                    class="action-btn danger"
                    @click="deleteUser(u)"
                    title="Delete"
                    :disabled="u.role === 'super_admin'"
                  >
                    <Trash2 :size="14" />
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <!-- System Settings Section -->
      <section class="admin-section">
        <div class="section-header">
          <h2><Settings :size="18" /> 系统设置</h2>
        </div>
        <div class="settings-form">
          <label class="setting-item">
            <input
              type="checkbox"
              v-model="systemSettings.allow_user_api_key"
              @change="updateSystemSettings"
            />
            <span>允许用户使用自己的 API Key</span>
          </label>
        </div>
      </section>
    </main>
  </div>
</template>

<style scoped>
.admin-container {
  min-height: 100vh;
  background: var(--bg);
  color: var(--text);
}

.admin-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 16px 24px;
  background: var(--bg-card);
  border-bottom: 1px solid var(--border);
}

.header-left {
  display: flex;
  align-items: center;
  gap: 16px;
}

.header-left h1 {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 18px;
  font-weight: 600;
  color: var(--cyan);
}

.back-btn {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 6px 12px;
  border-radius: 6px;
  border: 1px solid var(--border);
  background: transparent;
  color: var(--text-muted);
  font-size: 12px;
  cursor: pointer;
  transition: all 0.2s;
}

.back-btn:hover {
  background: var(--bg-hover);
  color: var(--text);
}

.user-info {
  font-size: 12px;
  color: var(--text-muted);
}

.admin-main {
  max-width: 1200px;
  margin: 0 auto;
  padding: 24px;
}

.alert {
  padding: 12px 16px;
  border-radius: 8px;
  margin-bottom: 16px;
  font-size: 14px;
}

.alert-error {
  background: rgba(239, 68, 68, 0.15);
  border: 1px solid rgba(239, 68, 68, 0.3);
  color: #ef4444;
}

.alert-success {
  background: rgba(34, 197, 94, 0.15);
  border: 1px solid rgba(34, 197, 94, 0.3);
  color: #22c55e;
}

.stats-row {
  display: flex;
  gap: 16px;
  margin-bottom: 24px;
}

.stat-card {
  flex: 1;
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 20px;
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 12px;
}

.stat-card svg {
  color: var(--cyan);
}

.stat-info {
  display: flex;
  flex-direction: column;
}

.stat-value {
  font-size: 28px;
  font-weight: 700;
}

.stat-label {
  font-size: 12px;
  color: var(--text-muted);
}

.admin-section {
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 12px;
  margin-bottom: 24px;
  overflow: hidden;
}

.section-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 16px 20px;
  border-bottom: 1px solid var(--border);
}

.section-header h2 {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  font-weight: 600;
}

.icon-btn {
  width: 32px;
  height: 32px;
  border-radius: 6px;
  border: 1px solid var(--border);
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: all 0.2s;
}

.icon-btn:hover {
  background: var(--bg-hover);
  color: var(--text);
}

.icon-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.spinning {
  animation: spin 1s linear infinite;
}

@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}

.users-table {
  overflow-x: auto;
}

table {
  width: 100%;
  border-collapse: collapse;
}

th, td {
  padding: 12px 16px;
  text-align: left;
  font-size: 13px;
}

th {
  background: var(--bg-hover);
  font-weight: 500;
  color: var(--text-muted);
  text-transform: uppercase;
  font-size: 11px;
  letter-spacing: 0.5px;
}

tr {
  border-bottom: 1px solid var(--border);
}

tr:last-child {
  border-bottom: none;
}

tr.inactive {
  opacity: 0.5;
}

.badge {
  padding: 2px 8px;
  border-radius: 4px;
  font-size: 11px;
  font-weight: 500;
}

.badge.user {
  background: rgba(139, 92, 246, 0.2);
  color: var(--purple);
}

.badge.admin {
  background: rgba(34, 211, 238, 0.2);
  color: var(--cyan);
}

.badge.super_admin {
  background: rgba(251, 191, 36, 0.2);
  color: #fbbf24;
}

.status {
  font-size: 12px;
}

.status.active {
  color: #22c55e;
}

.status.disabled {
  color: #ef4444;
}

.actions {
  display: flex;
  gap: 8px;
}

.action-btn {
  width: 28px;
  height: 28px;
  border-radius: 4px;
  border: 1px solid var(--border);
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: all 0.2s;
}

.action-btn:hover {
  background: var(--bg-hover);
  color: var(--text);
}

.action-btn.danger:hover {
  background: rgba(239, 68, 68, 0.15);
  border-color: #ef4444;
  color: #ef4444;
}

.action-btn:disabled {
  opacity: 0.3;
  cursor: not-allowed;
}

.settings-form {
  padding: 20px;
}

.setting-item {
  display: flex;
  align-items: center;
  gap: 12px;
  cursor: pointer;
  font-size: 14px;
}

.setting-item input[type="checkbox"] {
  width: 18px;
  height: 18px;
  accent-color: var(--cyan);
}
</style>
