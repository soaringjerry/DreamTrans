<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import {
  Users,
  Building2,
  FileText,
  BarChart3,
  RefreshCw,
  Shield,
  ShieldCheck,
  UserCog,
  Trash2,
  Edit,
  ChevronLeft,
  ChevronRight,
  Loader2,
  X,
  Check,
} from 'lucide-vue-next'
import * as adminApi from './api'
import type { User, Tenant, GlobalStats, UsageSummary } from './api'

const props = defineProps<{
  onClose?: () => void
}>()

// State
const loading = ref(false)
const error = ref('')
const activeTab = ref<'dashboard' | 'users' | 'tenants'>('dashboard')

// Dashboard data
const stats = ref<GlobalStats | null>(null)
const usage = ref<UsageSummary | null>(null)

// Users data
const users = ref<User[]>([])
const usersTotal = ref(0)
const usersPage = ref(1)

// Tenants data
const tenants = ref<Tenant[]>([])
const tenantsTotal = ref(0)
const tenantsPage = ref(1)

// Edit modal
const editingUser = ref<User | null>(null)
const editingTenant = ref<Tenant | null>(null)
const editForm = ref<Record<string, unknown>>({})
const saving = ref(false)

// Load data
async function loadStats() {
  try {
    stats.value = await adminApi.getGlobalStats()
    usage.value = await adminApi.getUsage()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load stats'
  }
}

async function loadUsers() {
  loading.value = true
  try {
    const result = await adminApi.listUsers(usersPage.value, 10)
    users.value = result.users
    usersTotal.value = result.total
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load users'
  } finally {
    loading.value = false
  }
}

async function loadTenants() {
  loading.value = true
  try {
    const result = await adminApi.listTenants(tenantsPage.value, 10)
    tenants.value = result.tenants
    tenantsTotal.value = result.total
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load tenants'
  } finally {
    loading.value = false
  }
}

async function refresh() {
  error.value = ''
  if (activeTab.value === 'dashboard') {
    await loadStats()
  } else if (activeTab.value === 'users') {
    await loadUsers()
  } else if (activeTab.value === 'tenants') {
    await loadTenants()
  }
}

// User actions
function editUser(user: User) {
  editingUser.value = user
  editForm.value = {
    name: user.name,
    role: user.role,
    is_active: user.is_active,
  }
}

async function saveUser() {
  if (!editingUser.value) return
  saving.value = true
  try {
    await adminApi.updateUser(editingUser.value.id, editForm.value as {
      name?: string
      role?: string
      is_active?: boolean
    })
    editingUser.value = null
    await loadUsers()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to save user'
  } finally {
    saving.value = false
  }
}

async function deleteUserAction(user: User) {
  if (!confirm(`Delete user ${user.email}?`)) return
  try {
    await adminApi.deleteUser(user.id)
    await loadUsers()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to delete user'
  }
}

// Tenant actions
function editTenant(tenant: Tenant) {
  editingTenant.value = tenant
  editForm.value = {
    name: tenant.name,
    plan: tenant.plan,
    api_quota_monthly: tenant.api_quota_monthly,
    storage_quota_gb: tenant.storage_quota_gb,
    max_sessions: tenant.max_sessions,
  }
}

async function saveTenant() {
  if (!editingTenant.value) return
  saving.value = true
  try {
    await adminApi.updateTenant(editingTenant.value.id, editForm.value as {
      name?: string
      plan?: string
      api_quota_monthly?: number
      storage_quota_gb?: number
      max_sessions?: number
    })
    editingTenant.value = null
    await loadTenants()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to save tenant'
  } finally {
    saving.value = false
  }
}

// Pagination
const usersTotalPages = computed(() => Math.ceil(usersTotal.value / 10))
const tenantsTotalPages = computed(() => Math.ceil(tenantsTotal.value / 10))

function usersNextPage() {
  if (usersPage.value < usersTotalPages.value) {
    usersPage.value++
    loadUsers()
  }
}

function usersPrevPage() {
  if (usersPage.value > 1) {
    usersPage.value--
    loadUsers()
  }
}

function tenantsNextPage() {
  if (tenantsPage.value < tenantsTotalPages.value) {
    tenantsPage.value++
    loadTenants()
  }
}

function tenantsPrevPage() {
  if (tenantsPage.value > 1) {
    tenantsPage.value--
    loadTenants()
  }
}

// Tab change
function switchTab(tab: 'dashboard' | 'users' | 'tenants') {
  activeTab.value = tab
  error.value = ''
  if (tab === 'dashboard') {
    loadStats()
  } else if (tab === 'users') {
    loadUsers()
  } else if (tab === 'tenants') {
    loadTenants()
  }
}

// Role badge
function getRoleBadgeClass(role: string): string {
  switch (role) {
    case 'super_admin':
      return 'badge-super-admin'
    case 'admin':
      return 'badge-admin'
    default:
      return 'badge-user'
  }
}

// Plan badge
function getPlanBadgeClass(plan: string): string {
  switch (plan) {
    case 'enterprise':
      return 'badge-enterprise'
    case 'pro':
      return 'badge-pro'
    default:
      return 'badge-free'
  }
}

onMounted(() => {
  loadStats()
})
</script>

<template>
  <div class="admin-dashboard">
    <!-- Header -->
    <div class="admin-header">
      <div class="admin-title">
        <Shield :size="24" />
        <h1>Admin Dashboard</h1>
      </div>
      <div class="admin-actions">
        <button class="btn-icon" @click="refresh" :disabled="loading">
          <RefreshCw :size="18" :class="{ spinning: loading }" />
        </button>
        <button v-if="props.onClose" class="btn-icon" @click="props.onClose">
          <X :size="18" />
        </button>
      </div>
    </div>

    <!-- Error -->
    <div v-if="error" class="admin-error">
      {{ error }}
      <button @click="error = ''"><X :size="14" /></button>
    </div>

    <!-- Tabs -->
    <div class="admin-tabs">
      <button
        :class="['tab-btn', { active: activeTab === 'dashboard' }]"
        @click="switchTab('dashboard')"
      >
        <BarChart3 :size="16" />
        Dashboard
      </button>
      <button
        :class="['tab-btn', { active: activeTab === 'users' }]"
        @click="switchTab('users')"
      >
        <Users :size="16" />
        Users
      </button>
      <button
        :class="['tab-btn', { active: activeTab === 'tenants' }]"
        @click="switchTab('tenants')"
      >
        <Building2 :size="16" />
        Tenants
      </button>
    </div>

    <!-- Dashboard Tab -->
    <div v-if="activeTab === 'dashboard'" class="admin-content">
      <div class="stats-grid">
        <div class="stat-card">
          <Users class="stat-icon" :size="24" />
          <div class="stat-value">{{ stats?.user_count || 0 }}</div>
          <div class="stat-label">Total Users</div>
        </div>
        <div class="stat-card">
          <Building2 class="stat-icon" :size="24" />
          <div class="stat-value">{{ stats?.tenant_count || 0 }}</div>
          <div class="stat-label">Tenants</div>
        </div>
        <div class="stat-card">
          <FileText class="stat-icon" :size="24" />
          <div class="stat-value">{{ stats?.session_count || 0 }}</div>
          <div class="stat-label">Sessions</div>
        </div>
        <div class="stat-card">
          <BarChart3 class="stat-icon" :size="24" />
          <div class="stat-value">{{ stats?.transcript_count || 0 }}</div>
          <div class="stat-label">Transcripts</div>
        </div>
      </div>

      <div v-if="usage" class="usage-section">
        <h3>Current Month Usage</h3>
        <div class="usage-grid">
          <div class="usage-item">
            <div class="usage-label">Transcription</div>
            <div class="usage-bar">
              <div
                class="usage-fill"
                :style="{
                  width: usage.limits.transcription_minutes < 0
                    ? '10%'
                    : `${Math.min(100, (usage.transcription_minutes / usage.limits.transcription_minutes) * 100)}%`
                }"
              />
            </div>
            <div class="usage-text">
              {{ usage.transcription_minutes.toFixed(1) }} min
              <span v-if="usage.limits.transcription_minutes >= 0">
                / {{ usage.limits.transcription_minutes }} min
              </span>
              <span v-else>/ Unlimited</span>
            </div>
          </div>
          <div class="usage-item">
            <div class="usage-label">RAG Queries</div>
            <div class="usage-bar">
              <div
                class="usage-fill"
                :style="{
                  width: usage.limits.rag_queries < 0
                    ? '10%'
                    : `${Math.min(100, (usage.rag_query_count / usage.limits.rag_queries) * 100)}%`
                }"
              />
            </div>
            <div class="usage-text">
              {{ usage.rag_query_count }}
              <span v-if="usage.limits.rag_queries >= 0">/ {{ usage.limits.rag_queries }}</span>
              <span v-else>/ Unlimited</span>
            </div>
          </div>
          <div class="usage-item">
            <div class="usage-label">Provider API Requests</div>
            <div class="usage-bar">
              <div
                class="usage-fill"
                :style="{
                  width: usage.api_quota_monthly < 0
                    ? '10%'
                    : usage.api_quota_monthly === 0
                      ? (usage.api_request_count > 0 ? '100%' : '0%')
                      : `${Math.min(100, (usage.api_request_count / usage.api_quota_monthly) * 100)}%`
                }"
              />
            </div>
            <div class="usage-text">
              {{ usage.api_request_count }}
              <span v-if="usage.api_quota_monthly >= 0">/ {{ usage.api_quota_monthly }}</span>
              <span v-else>/ Unlimited</span>
            </div>
          </div>
          <div class="usage-item">
            <div class="usage-label">Cloud Transcript Storage</div>
            <div class="usage-bar">
              <div
                class="usage-fill"
                :style="{
                  width: usage.limits.storage_gb < 0
                    ? '10%'
                    : usage.limits.storage_gb === 0
                      ? (usage.storage_mb > 0 ? '100%' : '0%')
                      : `${Math.min(100, (usage.storage_mb / 1024 / usage.limits.storage_gb) * 100)}%`
                }"
              />
            </div>
            <div class="usage-text">
              {{ usage.storage_mb.toFixed(2) }} MiB
              <span v-if="usage.limits.storage_gb >= 0">/ {{ usage.limits.storage_gb }} GiB</span>
              <span v-else>/ Unlimited</span>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Users Tab -->
    <div v-if="activeTab === 'users'" class="admin-content">
      <div v-if="loading" class="loading-state">
        <Loader2 class="spinning" :size="24" />
        Loading users...
      </div>
      <table v-else class="data-table">
        <thead>
          <tr>
            <th>Email</th>
            <th>Name</th>
            <th>Role</th>
            <th>Status</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="user in users" :key="user.id">
            <td>{{ user.email }}</td>
            <td>{{ user.name }}</td>
            <td>
              <span :class="['badge', getRoleBadgeClass(user.role)]">
                {{ user.role }}
              </span>
            </td>
            <td>
              <span :class="['badge', user.is_active ? 'badge-active' : 'badge-inactive']">
                {{ user.is_active ? 'Active' : 'Inactive' }}
              </span>
            </td>
            <td class="actions-cell">
              <button class="btn-icon-sm" @click="editUser(user)" title="Edit">
                <Edit :size="14" />
              </button>
              <button
                class="btn-icon-sm danger"
                @click="deleteUserAction(user)"
                title="Delete"
                :disabled="user.role === 'super_admin'"
              >
                <Trash2 :size="14" />
              </button>
            </td>
          </tr>
        </tbody>
      </table>
      <div class="pagination">
        <button @click="usersPrevPage" :disabled="usersPage <= 1">
          <ChevronLeft :size="16" />
        </button>
        <span>Page {{ usersPage }} of {{ usersTotalPages || 1 }}</span>
        <button @click="usersNextPage" :disabled="usersPage >= usersTotalPages">
          <ChevronRight :size="16" />
        </button>
      </div>
    </div>

    <!-- Tenants Tab -->
    <div v-if="activeTab === 'tenants'" class="admin-content">
      <div v-if="loading" class="loading-state">
        <Loader2 class="spinning" :size="24" />
        Loading tenants...
      </div>
      <table v-else class="data-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Slug</th>
            <th>Plan</th>
            <th>Sessions</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="tenant in tenants" :key="tenant.id">
            <td>{{ tenant.name }}</td>
            <td><code>{{ tenant.slug }}</code></td>
            <td>
              <span :class="['badge', getPlanBadgeClass(tenant.plan)]">
                {{ tenant.plan }}
              </span>
            </td>
            <td>{{ tenant.max_sessions < 0 ? '∞' : tenant.max_sessions }}</td>
            <td class="actions-cell">
              <button class="btn-icon-sm" @click="editTenant(tenant)" title="Edit">
                <Edit :size="14" />
              </button>
            </td>
          </tr>
        </tbody>
      </table>
      <div class="pagination">
        <button @click="tenantsPrevPage" :disabled="tenantsPage <= 1">
          <ChevronLeft :size="16" />
        </button>
        <span>Page {{ tenantsPage }} of {{ tenantsTotalPages || 1 }}</span>
        <button @click="tenantsNextPage" :disabled="tenantsPage >= tenantsTotalPages">
          <ChevronRight :size="16" />
        </button>
      </div>
    </div>

    <!-- Edit User Modal -->
    <div v-if="editingUser" class="modal-overlay" @click.self="editingUser = null">
      <div class="modal">
        <div class="modal-header">
          <UserCog :size="20" />
          <h3>Edit User</h3>
          <button class="btn-icon" @click="editingUser = null"><X :size="18" /></button>
        </div>
        <div class="modal-body">
          <div class="form-group">
            <label>Name</label>
            <input v-model="editForm.name" type="text" />
          </div>
          <div class="form-group">
            <label>Role</label>
            <select v-model="editForm.role">
              <option value="user">User</option>
              <option value="admin">Admin</option>
              <option value="super_admin">Super Admin</option>
            </select>
          </div>
          <div class="form-group">
            <label class="checkbox-label">
              <input v-model="editForm.is_active" type="checkbox" />
              Active
            </label>
          </div>
        </div>
        <div class="modal-footer">
          <button class="btn-secondary" @click="editingUser = null">Cancel</button>
          <button class="btn-primary" @click="saveUser" :disabled="saving">
            <Loader2 v-if="saving" class="spinning" :size="16" />
            <Check v-else :size="16" />
            Save
          </button>
        </div>
      </div>
    </div>

    <!-- Edit Tenant Modal -->
    <div v-if="editingTenant" class="modal-overlay" @click.self="editingTenant = null">
      <div class="modal">
        <div class="modal-header">
          <Building2 :size="20" />
          <h3>Edit Tenant</h3>
          <button class="btn-icon" @click="editingTenant = null"><X :size="18" /></button>
        </div>
        <div class="modal-body">
          <div class="form-group">
            <label>Name</label>
            <input v-model="editForm.name" type="text" />
          </div>
          <div class="form-group">
            <label>Plan</label>
            <select v-model="editForm.plan">
              <option value="free">Free</option>
              <option value="pro">Pro</option>
              <option value="enterprise">Enterprise</option>
            </select>
          </div>
          <div class="form-group">
            <label>API Quota (monthly, -1 for unlimited)</label>
            <input v-model.number="editForm.api_quota_monthly" type="number" />
          </div>
          <div class="form-group">
            <label>Storage (GB)</label>
            <input v-model.number="editForm.storage_quota_gb" type="number" />
          </div>
          <div class="form-group">
            <label>Max Sessions (-1 for unlimited)</label>
            <input v-model.number="editForm.max_sessions" type="number" />
          </div>
        </div>
        <div class="modal-footer">
          <button class="btn-secondary" @click="editingTenant = null">Cancel</button>
          <button class="btn-primary" @click="saveTenant" :disabled="saving">
            <Loader2 v-if="saving" class="spinning" :size="16" />
            <Check v-else :size="16" />
            Save
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.admin-dashboard {
  background: rgba(20, 20, 30, 0.95);
  backdrop-filter: blur(20px);
  border-radius: 16px;
  border: 1px solid rgba(255, 255, 255, 0.08);
  min-height: 500px;
  max-height: 80vh;
  overflow: hidden;
  display: flex;
  flex-direction: column;
}

.admin-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 1rem 1.5rem;
  border-bottom: 1px solid rgba(255, 255, 255, 0.08);
}

.admin-title {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  color: #fff;
}

.admin-title h1 {
  font-size: 1.25rem;
  font-weight: 600;
  margin: 0;
}

.admin-actions {
  display: flex;
  gap: 0.5rem;
}

.btn-icon {
  padding: 0.5rem;
  background: rgba(255, 255, 255, 0.05);
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 8px;
  color: rgba(255, 255, 255, 0.7);
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: all 0.2s;
}

.btn-icon:hover {
  background: rgba(255, 255, 255, 0.1);
  color: #fff;
}

.btn-icon:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.admin-error {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.75rem 1rem;
  margin: 0.5rem 1rem;
  background: rgba(239, 68, 68, 0.1);
  border: 1px solid rgba(239, 68, 68, 0.3);
  border-radius: 8px;
  color: #f87171;
  font-size: 0.875rem;
}

.admin-error button {
  background: none;
  border: none;
  color: inherit;
  cursor: pointer;
  padding: 4px;
}

.admin-tabs {
  display: flex;
  gap: 0.25rem;
  padding: 0 1rem;
  border-bottom: 1px solid rgba(255, 255, 255, 0.08);
}

.tab-btn {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.75rem 1rem;
  background: none;
  border: none;
  color: rgba(255, 255, 255, 0.5);
  font-size: 0.875rem;
  cursor: pointer;
  border-bottom: 2px solid transparent;
  transition: all 0.2s;
}

.tab-btn:hover {
  color: rgba(255, 255, 255, 0.8);
}

.tab-btn.active {
  color: #818cf8;
  border-bottom-color: #818cf8;
}

.admin-content {
  flex: 1;
  overflow: auto;
  padding: 1.5rem;
}

.loading-state {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0.75rem;
  padding: 3rem;
  color: rgba(255, 255, 255, 0.5);
}

/* Stats Grid */
.stats-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: 1rem;
  margin-bottom: 2rem;
}

.stat-card {
  background: rgba(255, 255, 255, 0.03);
  border: 1px solid rgba(255, 255, 255, 0.06);
  border-radius: 12px;
  padding: 1.25rem;
  text-align: center;
}

.stat-icon {
  color: #818cf8;
  margin-bottom: 0.75rem;
}

.stat-value {
  font-size: 2rem;
  font-weight: 700;
  color: #fff;
  margin-bottom: 0.25rem;
}

.stat-label {
  font-size: 0.8rem;
  color: rgba(255, 255, 255, 0.5);
}

/* Usage Section */
.usage-section h3 {
  font-size: 1rem;
  font-weight: 500;
  color: #fff;
  margin: 0 0 1rem;
}

.usage-grid {
  display: grid;
  gap: 1rem;
}

.usage-item {
  background: rgba(255, 255, 255, 0.03);
  border-radius: 8px;
  padding: 1rem;
}

.usage-label {
  font-size: 0.8rem;
  color: rgba(255, 255, 255, 0.5);
  margin-bottom: 0.5rem;
}

.usage-bar {
  height: 8px;
  background: rgba(255, 255, 255, 0.1);
  border-radius: 4px;
  overflow: hidden;
  margin-bottom: 0.5rem;
}

.usage-fill {
  height: 100%;
  background: linear-gradient(90deg, #6366f1, #8b5cf6);
  border-radius: 4px;
  transition: width 0.3s;
}

.usage-text {
  font-size: 0.875rem;
  color: #fff;
}

.usage-text span {
  color: rgba(255, 255, 255, 0.5);
}

/* Data Table */
.data-table {
  width: 100%;
  border-collapse: collapse;
}

.data-table th,
.data-table td {
  padding: 0.75rem 1rem;
  text-align: left;
  border-bottom: 1px solid rgba(255, 255, 255, 0.06);
}

.data-table th {
  font-size: 0.75rem;
  font-weight: 600;
  text-transform: uppercase;
  color: rgba(255, 255, 255, 0.5);
}

.data-table td {
  font-size: 0.875rem;
  color: #fff;
}

.data-table code {
  padding: 0.2rem 0.4rem;
  background: rgba(255, 255, 255, 0.05);
  border-radius: 4px;
  font-size: 0.8rem;
}

.actions-cell {
  display: flex;
  gap: 0.5rem;
}

.btn-icon-sm {
  padding: 0.375rem;
  background: rgba(255, 255, 255, 0.05);
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 6px;
  color: rgba(255, 255, 255, 0.7);
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: all 0.2s;
}

.btn-icon-sm:hover {
  background: rgba(255, 255, 255, 0.1);
  color: #fff;
}

.btn-icon-sm.danger:hover {
  background: rgba(239, 68, 68, 0.2);
  border-color: rgba(239, 68, 68, 0.4);
  color: #f87171;
}

.btn-icon-sm:disabled {
  opacity: 0.3;
  cursor: not-allowed;
}

/* Badges */
.badge {
  display: inline-block;
  padding: 0.2rem 0.5rem;
  font-size: 0.7rem;
  font-weight: 500;
  border-radius: 4px;
  text-transform: uppercase;
}

.badge-user {
  background: rgba(100, 116, 139, 0.2);
  color: #94a3b8;
}

.badge-admin {
  background: rgba(59, 130, 246, 0.2);
  color: #60a5fa;
}

.badge-super-admin {
  background: rgba(168, 85, 247, 0.2);
  color: #c084fc;
}

.badge-active {
  background: rgba(34, 197, 94, 0.2);
  color: #4ade80;
}

.badge-inactive {
  background: rgba(239, 68, 68, 0.2);
  color: #f87171;
}

.badge-free {
  background: rgba(100, 116, 139, 0.2);
  color: #94a3b8;
}

.badge-pro {
  background: rgba(59, 130, 246, 0.2);
  color: #60a5fa;
}

.badge-enterprise {
  background: rgba(234, 179, 8, 0.2);
  color: #fbbf24;
}

/* Pagination */
.pagination {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 1rem;
  padding: 1rem;
  border-top: 1px solid rgba(255, 255, 255, 0.06);
}

.pagination button {
  padding: 0.5rem;
  background: rgba(255, 255, 255, 0.05);
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 6px;
  color: rgba(255, 255, 255, 0.7);
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
}

.pagination button:hover:not(:disabled) {
  background: rgba(255, 255, 255, 0.1);
  color: #fff;
}

.pagination button:disabled {
  opacity: 0.3;
  cursor: not-allowed;
}

.pagination span {
  font-size: 0.875rem;
  color: rgba(255, 255, 255, 0.5);
}

/* Modal */
.modal-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.6);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
}

.modal {
  width: 100%;
  max-width: 400px;
  background: rgba(30, 30, 40, 0.98);
  backdrop-filter: blur(20px);
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 16px;
  overflow: hidden;
}

.modal-header {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 1rem 1.5rem;
  border-bottom: 1px solid rgba(255, 255, 255, 0.08);
  color: #fff;
}

.modal-header h3 {
  flex: 1;
  margin: 0;
  font-size: 1rem;
  font-weight: 600;
}

.modal-body {
  padding: 1.5rem;
}

.form-group {
  margin-bottom: 1rem;
}

.form-group label {
  display: block;
  font-size: 0.875rem;
  color: rgba(255, 255, 255, 0.7);
  margin-bottom: 0.5rem;
}

.form-group input[type="text"],
.form-group input[type="number"],
.form-group select {
  width: 100%;
  padding: 0.625rem 0.875rem;
  background: rgba(255, 255, 255, 0.05);
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 8px;
  color: #fff;
  font-size: 0.875rem;
}

.form-group input:focus,
.form-group select:focus {
  outline: none;
  border-color: #6366f1;
}

.checkbox-label {
  display: flex !important;
  align-items: center;
  gap: 0.5rem;
  cursor: pointer;
}

.checkbox-label input {
  width: auto !important;
}

.modal-footer {
  display: flex;
  justify-content: flex-end;
  gap: 0.75rem;
  padding: 1rem 1.5rem;
  border-top: 1px solid rgba(255, 255, 255, 0.08);
}

.btn-secondary,
.btn-primary {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.625rem 1rem;
  border-radius: 8px;
  font-size: 0.875rem;
  cursor: pointer;
  transition: all 0.2s;
}

.btn-secondary {
  background: rgba(255, 255, 255, 0.05);
  border: 1px solid rgba(255, 255, 255, 0.1);
  color: rgba(255, 255, 255, 0.7);
}

.btn-secondary:hover {
  background: rgba(255, 255, 255, 0.1);
  color: #fff;
}

.btn-primary {
  background: linear-gradient(135deg, #6366f1, #8b5cf6);
  border: none;
  color: #fff;
}

.btn-primary:hover {
  transform: translateY(-1px);
  box-shadow: 0 4px 12px rgba(99, 102, 241, 0.4);
}

.btn-primary:disabled {
  opacity: 0.5;
  cursor: not-allowed;
  transform: none;
}

.spinning {
  animation: spin 1s linear infinite;
}

@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}
</style>
