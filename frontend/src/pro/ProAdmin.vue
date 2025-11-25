<script setup lang="ts">
/**
 * DreamTrans Pro - Admin Panel
 * Full admin dashboard with user management, pricing, and system stats
 */
import { ref, onMounted, computed, reactive } from 'vue'
import {
  Shield, Users, Settings, ChevronLeft, RefreshCw, Trash2, Check, X,
  Plus, DollarSign, BarChart3, Coins, UserPlus, Edit2, Save
} from 'lucide-vue-next'
import { useAuth } from './composables/useAuth'
import * as authApi from './api/auth'

const { user, isAdmin, init: initAuth } = useAuth()

// State
const loading = ref(false)
const activeTab = ref<'stats' | 'users' | 'pricing' | 'settings'>('stats')
const users = ref<any[]>([])
const systemStats = ref<any>(null)
const pricingRules = ref<any[]>([])
const systemSettings = ref<Record<string, string>>({})
const error = ref('')
const successMsg = ref('')

// Create user form
const showCreateUser = ref(false)
const newUser = reactive({
  email: '',
  password: '',
  name: '',
  role: 'user',
  dreampoints: 100
})

// Adjust balance form
const showAdjustBalance = ref(false)
const adjustTarget = ref<any>(null)
const adjustAmount = ref(0)
const adjustDescription = ref('')

// Edit pricing form
const editingRule = ref<any>(null)

// API helpers
async function apiCall(endpoint: string, options: RequestInit = {}) {
  const token = authApi.getAccessToken()
  const res = await fetch(endpoint, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      'Authorization': `Bearer ${token}`,
      ...options.headers,
    },
  })
  if (!res.ok) {
    const data = await res.json().catch(() => ({}))
    throw new Error(data.error || 'Request failed')
  }
  return res.json()
}

// Fetch functions
async function fetchUsers() {
  loading.value = true
  try {
    const res = await apiCall('/api/admin/users')
    users.value = res.users || []
  } catch (e: any) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

async function fetchStats() {
  try {
    systemStats.value = await apiCall('/api/admin/stats')
  } catch (e: any) {
    error.value = e.message
  }
}

async function fetchPricing() {
  try {
    const res = await apiCall('/api/admin/pricing')
    pricingRules.value = res.rules || []
  } catch (e: any) {
    error.value = e.message
  }
}

async function fetchSettings() {
  try {
    systemSettings.value = await apiCall('/api/admin/settings')
  } catch (e: any) {
    error.value = e.message
  }
}

// User actions
async function createUser() {
  try {
    await apiCall('/api/admin/users', {
      method: 'POST',
      body: JSON.stringify(newUser),
    })
    showCreateUser.value = false
    Object.assign(newUser, { email: '', password: '', name: '', role: 'user', dreampoints: 100 })
    await fetchUsers()
    showSuccess('User created successfully')
  } catch (e: any) {
    error.value = e.message
  }
}

async function toggleUserActive(u: any) {
  try {
    await authApi.adminUpdateUser(u.id, { is_active: !u.is_active })
    await fetchUsers()
    showSuccess(`User ${u.is_active ? 'disabled' : 'enabled'}`)
  } catch (e: any) {
    error.value = e.message
  }
}

async function deleteUser(u: any) {
  if (!confirm(`Delete user ${u.email}? This cannot be undone.`)) return
  try {
    await authApi.adminDeleteUser(u.id)
    await fetchUsers()
    showSuccess('User deleted')
  } catch (e: any) {
    error.value = e.message
  }
}

// Balance actions
function openAdjustBalance(u: any) {
  adjustTarget.value = u
  adjustAmount.value = 0
  adjustDescription.value = ''
  showAdjustBalance.value = true
}

async function submitAdjustBalance() {
  if (!adjustTarget.value || adjustAmount.value === 0) return
  try {
    await apiCall('/api/admin/balance', {
      method: 'POST',
      body: JSON.stringify({
        user_id: adjustTarget.value.id,
        amount: adjustAmount.value,
        description: adjustDescription.value || `Admin adjustment: ${adjustAmount.value > 0 ? '+' : ''}${adjustAmount.value}`,
      }),
    })
    showAdjustBalance.value = false
    await fetchUsers()
    showSuccess('Balance updated')
  } catch (e: any) {
    error.value = e.message
  }
}

// Pricing actions
async function savePricingRule(rule: any) {
  try {
    if (rule.id) {
      await apiCall(`/api/admin/pricing/${rule.id}`, {
        method: 'PUT',
        body: JSON.stringify(rule),
      })
    } else {
      await apiCall('/api/admin/pricing', {
        method: 'POST',
        body: JSON.stringify(rule),
      })
    }
    editingRule.value = null
    await fetchPricing()
    showSuccess('Pricing rule saved')
  } catch (e: any) {
    error.value = e.message
  }
}

async function deletePricingRule(rule: any) {
  if (!confirm('Delete this pricing rule?')) return
  try {
    await apiCall(`/api/admin/pricing/${rule.id}`, { method: 'DELETE' })
    await fetchPricing()
    showSuccess('Pricing rule deleted')
  } catch (e: any) {
    error.value = e.message
  }
}

// Settings actions
async function updateSettings() {
  try {
    // Convert all values to strings (number inputs may produce numbers)
    const payload: Record<string, string> = {}
    for (const [key, val] of Object.entries(systemSettings.value)) {
      payload[key] = String(val)
    }
    await apiCall('/api/admin/settings', {
      method: 'PUT',
      body: JSON.stringify(payload),
    })
    showSuccess('Settings updated')
  } catch (e: any) {
    error.value = e.message
  }
}

// Helpers
function showSuccess(msg: string) {
  successMsg.value = msg
  setTimeout(() => successMsg.value = '', 3000)
}

function goBack() {
  window.location.href = '/pro'
}

// Computed
const userCount = computed(() => users.value.length)
const activeUserCount = computed(() => users.value.filter((u: any) => u.is_active).length)
const totalDreampoints = computed(() => systemStats.value?.billing?.total_dreampoints || 0)
const totalUsed = computed(() => systemStats.value?.billing?.total_used || 0)

// Format numbers
function formatNumber(n: number) {
  if (n >= 1000000) return (n / 1000000).toFixed(2) + 'M'
  if (n >= 1000) return (n / 1000).toFixed(1) + 'K'
  return n.toFixed(2)
}

// Init
onMounted(async () => {
  await initAuth()
  if (!isAdmin.value) {
    window.location.href = '/pro'
    return
  }
  await Promise.all([fetchStats(), fetchUsers(), fetchPricing(), fetchSettings()])
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

    <!-- Tabs -->
    <nav class="admin-tabs">
      <button :class="{ active: activeTab === 'stats' }" @click="activeTab = 'stats'">
        <BarChart3 :size="16" /> 统计
      </button>
      <button :class="{ active: activeTab === 'users' }" @click="activeTab = 'users'">
        <Users :size="16" /> 用户
      </button>
      <button :class="{ active: activeTab === 'pricing' }" @click="activeTab = 'pricing'">
        <DollarSign :size="16" /> 计费
      </button>
      <button :class="{ active: activeTab === 'settings' }" @click="activeTab = 'settings'">
        <Settings :size="16" /> 设置
      </button>
    </nav>

    <!-- Main Content -->
    <main class="admin-main">
      <!-- Messages -->
      <div v-if="error" class="alert alert-error">{{ error }} <button @click="error = ''">×</button></div>
      <div v-if="successMsg" class="alert alert-success">{{ successMsg }}</div>

      <!-- Stats Tab -->
      <div v-if="activeTab === 'stats'" class="tab-content">
        <div class="stats-grid">
          <div class="stat-card">
            <div class="stat-icon users"><Users :size="24" /></div>
            <div class="stat-info">
              <span class="stat-value">{{ userCount }}</span>
              <span class="stat-label">总用户</span>
            </div>
          </div>
          <div class="stat-card">
            <div class="stat-icon active"><Check :size="24" /></div>
            <div class="stat-info">
              <span class="stat-value">{{ activeUserCount }}</span>
              <span class="stat-label">活跃用户</span>
            </div>
          </div>
          <div class="stat-card">
            <div class="stat-icon points"><Coins :size="24" /></div>
            <div class="stat-info">
              <span class="stat-value">{{ formatNumber(totalDreampoints) }}</span>
              <span class="stat-label">用户余额总计</span>
            </div>
          </div>
          <div class="stat-card">
            <div class="stat-icon used"><DollarSign :size="24" /></div>
            <div class="stat-info">
              <span class="stat-value">{{ formatNumber(totalUsed) }}</span>
              <span class="stat-label">已使用</span>
            </div>
          </div>
        </div>

        <div class="stats-detail" v-if="systemStats?.billing">
          <h3>用量明细</h3>
          <div class="usage-grid">
            <div class="usage-item" v-for="(cost, action) in systemStats.billing.usage_by_action" :key="action">
              <span class="usage-label">{{ action }}</span>
              <span class="usage-value">{{ formatNumber(cost) }} DP</span>
            </div>
          </div>
        </div>
      </div>

      <!-- Users Tab -->
      <div v-if="activeTab === 'users'" class="tab-content">
        <div class="section-header">
          <h2>用户管理</h2>
          <div class="header-actions">
            <button class="btn-primary" @click="showCreateUser = true">
              <UserPlus :size="16" /> 新建用户
            </button>
            <button class="icon-btn" @click="fetchUsers" :disabled="loading">
              <RefreshCw :size="16" :class="{ spinning: loading }" />
            </button>
          </div>
        </div>

        <div class="users-table">
          <table>
            <thead>
              <tr>
                <th>Email</th>
                <th>名称</th>
                <th>角色</th>
                <th>Dreampoints</th>
                <th>状态</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="u in users" :key="u.id" :class="{ inactive: !u.is_active }">
                <td>{{ u.email }}</td>
                <td>{{ u.name || '-' }}</td>
                <td><span class="badge" :class="u.role">{{ u.role }}</span></td>
                <td>
                  <span class="dreampoints">{{ u.dreampoints?.toFixed(2) || '0.00' }}</span>
                  <button class="mini-btn" @click="openAdjustBalance(u)" title="调整余额">
                    <Edit2 :size="12" />
                  </button>
                </td>
                <td>
                  <span class="status" :class="u.is_active ? 'active' : 'disabled'">
                    {{ u.is_active ? '活跃' : '禁用' }}
                  </span>
                </td>
                <td class="actions">
                  <button class="action-btn" @click="toggleUserActive(u)" :title="u.is_active ? '禁用' : '启用'">
                    <X v-if="u.is_active" :size="14" />
                    <Check v-else :size="14" />
                  </button>
                  <button class="action-btn danger" @click="deleteUser(u)" title="删除" :disabled="u.role === 'super_admin'">
                    <Trash2 :size="14" />
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>

      <!-- Pricing Tab -->
      <div v-if="activeTab === 'pricing'" class="tab-content">
        <div class="section-header">
          <h2>计费规则</h2>
          <button class="btn-primary" @click="editingRule = { rule_type: '', model: '', price_per_unit: 0, unit_type: '', is_active: true, priority: 0 }">
            <Plus :size="16" /> 新建规则
          </button>
        </div>

        <div class="pricing-table">
          <table>
            <thead>
              <tr>
                <th>类型</th>
                <th>模型</th>
                <th>单价</th>
                <th>单位</th>
                <th>优先级</th>
                <th>状态</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="rule in pricingRules" :key="rule.id">
                <td>{{ rule.rule_type }}</td>
                <td>{{ rule.model || '默认' }}</td>
                <td>{{ rule.price_per_unit }}</td>
                <td>{{ rule.unit_type }}</td>
                <td>{{ rule.priority }}</td>
                <td>
                  <span class="status" :class="rule.is_active ? 'active' : 'disabled'">
                    {{ rule.is_active ? '启用' : '禁用' }}
                  </span>
                </td>
                <td class="actions">
                  <button class="action-btn" @click="editingRule = { ...rule }"><Edit2 :size="14" /></button>
                  <button class="action-btn danger" @click="deletePricingRule(rule)"><Trash2 :size="14" /></button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>

      <!-- Settings Tab -->
      <div v-if="activeTab === 'settings'" class="tab-content">
        <div class="section-header">
          <h2>系统设置</h2>
        </div>

        <div class="settings-form">
          <div class="setting-group">
            <label class="setting-item">
              <input type="checkbox" v-model="systemSettings.billing_enabled" true-value="true" false-value="false" />
              <span>启用计费系统</span>
            </label>
            <label class="setting-item">
              <input type="checkbox" v-model="systemSettings.allow_user_api_key" true-value="true" false-value="false" />
              <span>允许用户使用自己的 API Key</span>
            </label>
            <label class="setting-item">
              <input type="checkbox" v-model="systemSettings.allow_negative_balance" true-value="true" false-value="false" />
              <span>允许余额为负</span>
            </label>
          </div>

          <div class="setting-group">
            <label class="setting-label">新用户默认 Dreampoints</label>
            <input type="number" v-model="systemSettings.free_tier_dreampoints" class="input" />
          </div>

          <button class="btn-primary" @click="updateSettings">
            <Save :size="16" /> 保存设置
          </button>
        </div>
      </div>
    </main>

    <!-- Create User Modal -->
    <div v-if="showCreateUser" class="modal-overlay" @click.self="showCreateUser = false">
      <div class="modal">
        <div class="modal-header">
          <h3><UserPlus :size="18" /> 新建用户</h3>
          <button class="close-btn" @click="showCreateUser = false"><X :size="20" /></button>
        </div>
        <div class="modal-body">
          <label class="form-label">Email *</label>
          <input v-model="newUser.email" type="email" class="input" placeholder="user@example.com" />

          <label class="form-label">密码 *</label>
          <input v-model="newUser.password" type="password" class="input" placeholder="至少6位" />

          <label class="form-label">名称</label>
          <input v-model="newUser.name" type="text" class="input" placeholder="用户名称" />

          <label class="form-label">角色</label>
          <select v-model="newUser.role" class="input">
            <option value="user">User</option>
            <option value="admin">Admin</option>
          </select>

          <label class="form-label">初始 Dreampoints</label>
          <input v-model="newUser.dreampoints" type="number" class="input" />
        </div>
        <div class="modal-footer">
          <button class="btn-secondary" @click="showCreateUser = false">取消</button>
          <button class="btn-primary" @click="createUser">创建</button>
        </div>
      </div>
    </div>

    <!-- Adjust Balance Modal -->
    <div v-if="showAdjustBalance" class="modal-overlay" @click.self="showAdjustBalance = false">
      <div class="modal">
        <div class="modal-header">
          <h3><Coins :size="18" /> 调整余额</h3>
          <button class="close-btn" @click="showAdjustBalance = false"><X :size="20" /></button>
        </div>
        <div class="modal-body">
          <p class="user-target">用户: {{ adjustTarget?.email }}</p>
          <p class="current-balance">当前余额: {{ adjustTarget?.dreampoints?.toFixed(2) || '0.00' }} DP</p>

          <label class="form-label">调整金额 (正数增加，负数减少)</label>
          <input v-model.number="adjustAmount" type="number" step="0.01" class="input" />

          <label class="form-label">备注</label>
          <input v-model="adjustDescription" type="text" class="input" placeholder="调整原因" />
        </div>
        <div class="modal-footer">
          <button class="btn-secondary" @click="showAdjustBalance = false">取消</button>
          <button class="btn-primary" @click="submitAdjustBalance">确认</button>
        </div>
      </div>
    </div>

    <!-- Edit Pricing Rule Modal -->
    <div v-if="editingRule" class="modal-overlay" @click.self="editingRule = null">
      <div class="modal">
        <div class="modal-header">
          <h3><DollarSign :size="18" /> {{ editingRule.id ? '编辑' : '新建' }}计费规则</h3>
          <button class="close-btn" @click="editingRule = null"><X :size="20" /></button>
        </div>
        <div class="modal-body">
          <label class="form-label">类型 *</label>
          <select v-model="editingRule.rule_type" class="input">
            <option value="transcription">transcription</option>
            <option value="translation">translation</option>
            <option value="chat">chat</option>
            <option value="summarize">summarize</option>
          </select>

          <label class="form-label">模型 (留空表示默认)</label>
          <input v-model="editingRule.model" type="text" class="input" placeholder="gpt-4o, gpt-4o-mini..." />

          <label class="form-label">单价 *</label>
          <input v-model.number="editingRule.price_per_unit" type="number" step="0.000001" class="input" />

          <label class="form-label">单位类型 *</label>
          <select v-model="editingRule.unit_type" class="input">
            <option value="minute">minute</option>
            <option value="hour">hour</option>
            <option value="input_token">input_token</option>
            <option value="output_token">output_token</option>
          </select>

          <label class="form-label">优先级</label>
          <input v-model.number="editingRule.priority" type="number" class="input" />

          <label class="setting-item">
            <input type="checkbox" v-model="editingRule.is_active" />
            <span>启用</span>
          </label>
        </div>
        <div class="modal-footer">
          <button class="btn-secondary" @click="editingRule = null">取消</button>
          <button class="btn-primary" @click="savePricingRule(editingRule)">保存</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.admin-container {
  min-height: 100vh;
  background: #0a0a0a;
  color: #e8ecf5;
}

.admin-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 16px 24px;
  background: #121212;
  border-bottom: 1px solid #2a2a2a;
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
  color: #22d3ee;
}

.back-btn {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 6px 12px;
  border-radius: 6px;
  border: 1px solid #2a2a2a;
  background: transparent;
  color: #a1a1aa;
  font-size: 12px;
  cursor: pointer;
  transition: all 0.2s;
}

.back-btn:hover {
  background: #1a1a1a;
  color: #e8ecf5;
}

.user-info {
  font-size: 12px;
  color: #a1a1aa;
}

.admin-tabs {
  display: flex;
  gap: 4px;
  padding: 12px 24px;
  background: #121212;
  border-bottom: 1px solid #2a2a2a;
}

.admin-tabs button {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 16px;
  border-radius: 8px;
  border: none;
  background: transparent;
  color: #a1a1aa;
  font-size: 13px;
  cursor: pointer;
  transition: all 0.2s;
}

.admin-tabs button:hover {
  background: #1a1a1a;
}

.admin-tabs button.active {
  background: #8b5cf6;
  color: white;
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
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.alert button {
  background: none;
  border: none;
  color: inherit;
  cursor: pointer;
  font-size: 18px;
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

.stats-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  gap: 16px;
  margin-bottom: 24px;
}

.stat-card {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 20px;
  background: #121212;
  border: 1px solid #2a2a2a;
  border-radius: 12px;
}

.stat-icon {
  width: 48px;
  height: 48px;
  border-radius: 12px;
  display: flex;
  align-items: center;
  justify-content: center;
}

.stat-icon.users { background: rgba(59, 130, 246, 0.2); color: #3b82f6; }
.stat-icon.active { background: rgba(34, 197, 94, 0.2); color: #22c55e; }
.stat-icon.points { background: rgba(168, 85, 247, 0.2); color: #a855f7; }
.stat-icon.used { background: rgba(251, 191, 36, 0.2); color: #fbbf24; }

.stat-info {
  display: flex;
  flex-direction: column;
}

.stat-value {
  font-size: 24px;
  font-weight: 700;
}

.stat-label {
  font-size: 12px;
  color: #a1a1aa;
}

.stats-detail {
  background: #121212;
  border: 1px solid #2a2a2a;
  border-radius: 12px;
  padding: 20px;
}

.stats-detail h3 {
  font-size: 14px;
  margin-bottom: 16px;
}

.usage-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: 12px;
}

.usage-item {
  display: flex;
  justify-content: space-between;
  padding: 12px;
  background: #1a1a1a;
  border-radius: 8px;
}

.usage-label {
  color: #a1a1aa;
  font-size: 12px;
}

.usage-value {
  font-weight: 600;
  color: #a855f7;
}

.section-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 16px;
}

.section-header h2 {
  font-size: 16px;
  font-weight: 600;
}

.header-actions {
  display: flex;
  gap: 8px;
}

.btn-primary {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 16px;
  border-radius: 8px;
  border: none;
  background: #8b5cf6;
  color: white;
  font-size: 13px;
  cursor: pointer;
  transition: all 0.2s;
}

.btn-primary:hover {
  background: #7c3aed;
}

.btn-secondary {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 16px;
  border-radius: 8px;
  border: 1px solid #2a2a2a;
  background: transparent;
  color: #e8ecf5;
  font-size: 13px;
  cursor: pointer;
  transition: all 0.2s;
}

.btn-secondary:hover {
  background: #1a1a1a;
}

.icon-btn {
  width: 36px;
  height: 36px;
  border-radius: 8px;
  border: 1px solid #2a2a2a;
  background: transparent;
  color: #a1a1aa;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
}

.icon-btn:hover {
  background: #1a1a1a;
  color: #e8ecf5;
}

.spinning {
  animation: spin 1s linear infinite;
}

@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}

.users-table, .pricing-table {
  background: #121212;
  border: 1px solid #2a2a2a;
  border-radius: 12px;
  overflow: hidden;
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
  background: #1a1a1a;
  font-weight: 500;
  color: #a1a1aa;
  text-transform: uppercase;
  font-size: 11px;
}

tr {
  border-bottom: 1px solid #2a2a2a;
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

.badge.user { background: rgba(139, 92, 246, 0.2); color: #8b5cf6; }
.badge.admin { background: rgba(34, 211, 238, 0.2); color: #22d3ee; }
.badge.super_admin { background: rgba(251, 191, 36, 0.2); color: #fbbf24; }

.status { font-size: 12px; }
.status.active { color: #22c55e; }
.status.disabled { color: #ef4444; }

.dreampoints {
  font-family: monospace;
  color: #a855f7;
}

.mini-btn {
  margin-left: 8px;
  padding: 2px 4px;
  border: none;
  background: transparent;
  color: #a1a1aa;
  cursor: pointer;
}

.mini-btn:hover {
  color: #e8ecf5;
}

.actions {
  display: flex;
  gap: 8px;
}

.action-btn {
  width: 28px;
  height: 28px;
  border-radius: 4px;
  border: 1px solid #2a2a2a;
  background: transparent;
  color: #a1a1aa;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
}

.action-btn:hover {
  background: #1a1a1a;
  color: #e8ecf5;
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
  background: #121212;
  border: 1px solid #2a2a2a;
  border-radius: 12px;
  padding: 24px;
}

.setting-group {
  margin-bottom: 20px;
}

.setting-item {
  display: flex;
  align-items: center;
  gap: 12px;
  cursor: pointer;
  font-size: 14px;
  margin-bottom: 12px;
}

.setting-item input[type="checkbox"] {
  width: 18px;
  height: 18px;
  accent-color: #8b5cf6;
}

.setting-label {
  display: block;
  font-size: 12px;
  color: #a1a1aa;
  margin-bottom: 6px;
}

.input {
  width: 100%;
  padding: 10px 12px;
  border: 1px solid #2a2a2a;
  border-radius: 8px;
  background: #1a1a1a;
  color: #e8ecf5;
  font-size: 14px;
}

.input:focus {
  outline: none;
  border-color: #8b5cf6;
}

.modal-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.7);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 100;
}

.modal {
  background: #121212;
  border: 1px solid #2a2a2a;
  border-radius: 16px;
  width: 100%;
  max-width: 400px;
  max-height: 90vh;
  overflow: hidden;
}

.modal-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 16px 20px;
  border-bottom: 1px solid #2a2a2a;
}

.modal-header h3 {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 16px;
}

.close-btn {
  padding: 4px;
  border: none;
  background: transparent;
  color: #a1a1aa;
  cursor: pointer;
}

.close-btn:hover {
  color: #e8ecf5;
}

.modal-body {
  padding: 20px;
  max-height: 60vh;
  overflow-y: auto;
}

.modal-body .form-label {
  display: block;
  font-size: 12px;
  color: #a1a1aa;
  margin: 12px 0 6px;
}

.modal-body .form-label:first-child {
  margin-top: 0;
}

.user-target {
  font-weight: 600;
  margin-bottom: 4px;
}

.current-balance {
  color: #a855f7;
  margin-bottom: 16px;
}

.modal-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding: 16px 20px;
  border-top: 1px solid #2a2a2a;
}
</style>
