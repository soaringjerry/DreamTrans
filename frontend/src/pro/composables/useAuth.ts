import { ref, computed, readonly } from 'vue'
import type { User, Tenant, AuthResponse } from '../api/auth'
import * as authApi from '../api/auth'

// Global state (singleton pattern for Vue)
const user = ref<User | null>(null)
const tenant = ref<Tenant | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)
const initialized = ref(false)

export function useAuth() {
  const isAuthenticated = computed(() => !!user.value)
  const isAdmin = computed(() => user.value?.role === 'admin' || user.value?.role === 'super_admin')
  const isSuperAdmin = computed(() => user.value?.role === 'super_admin')

  // Initialize auth state from storage
  async function init(): Promise<void> {
    if (initialized.value) return

    loading.value = true
    error.value = null

    try {
      const storedUser = await authApi.initAuth()
      if (storedUser) {
        user.value = storedUser
        // Load tenant info
        try {
          const profile = await authApi.getProfile()
          if (profile.tenant) {
            tenant.value = profile.tenant
          }
        } catch {
          // Ignore tenant fetch error
        }
      }
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Initialization failed'
    } finally {
      loading.value = false
      initialized.value = true
    }
  }

  // Register a new user
  async function register(
    email: string,
    password: string,
    name: string
  ): Promise<AuthResponse> {
    loading.value = true
    error.value = null

    try {
      const response = await authApi.register(email, password, name)
      user.value = response.user
      return response
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Registration failed'
      throw e
    } finally {
      loading.value = false
    }
  }

  // Login
  async function login(email: string, password: string): Promise<AuthResponse> {
    loading.value = true
    error.value = null

    try {
      const response = await authApi.login(email, password)
      user.value = response.user
      // Load tenant info
      try {
        const profile = await authApi.getProfile()
        if (profile.tenant) {
          tenant.value = profile.tenant
        }
      } catch {
        // Ignore
      }
      return response
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Login failed'
      throw e
    } finally {
      loading.value = false
    }
  }

  // Logout
  async function logout(): Promise<void> {
    loading.value = true
    try {
      await authApi.logout()
    } finally {
      user.value = null
      tenant.value = null
      loading.value = false
    }
  }

  // Update profile
  async function updateProfile(name: string): Promise<void> {
    loading.value = true
    error.value = null

    try {
      const updated = await authApi.updateProfile(name)
      user.value = updated
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Update failed'
      throw e
    } finally {
      loading.value = false
    }
  }

  // Update password
  async function updatePassword(
    currentPassword: string,
    newPassword: string
  ): Promise<void> {
    loading.value = true
    error.value = null

    try {
      await authApi.updatePassword(currentPassword, newPassword)
      // Clear session after password change
      await logout()
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Password update failed'
      throw e
    } finally {
      loading.value = false
    }
  }

  // Refresh user data
  async function refreshUser(): Promise<void> {
    try {
      const profile = await authApi.getProfile()
      user.value = profile.user
      tenant.value = profile.tenant || null
    } catch {
      // Ignore refresh errors
    }
  }

  // Clear error
  function clearError(): void {
    error.value = null
  }

  return {
    // State (readonly)
    user: readonly(user),
    tenant: readonly(tenant),
    loading: readonly(loading),
    error: readonly(error),
    initialized: readonly(initialized),

    // Computed
    isAuthenticated,
    isAdmin,
    isSuperAdmin,

    // Actions
    init,
    register,
    login,
    logout,
    updateProfile,
    updatePassword,
    refreshUser,
    clearError,
  }
}
