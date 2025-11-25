/**
 * System Settings Composable for DreamTrans Pro
 *
 * Fetches and caches system-wide settings from the backend.
 */
import { ref, readonly, onMounted } from 'vue'
import { getSystemSettings, type SystemSettings } from '../api/system'

// Global state (singleton pattern)
const settings = ref<SystemSettings | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)
const loaded = ref(false)

export function useSystemSettings() {
  // Load settings if not already loaded
  async function loadSettings(): Promise<void> {
    if (loading.value) return

    loading.value = true
    error.value = null

    try {
      settings.value = await getSystemSettings()
      loaded.value = true
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load settings'
      // Set defaults if fetch fails
      settings.value = {
        allow_user_api_key: false,
      }
    } finally {
      loading.value = false
    }
  }

  // Refresh settings
  async function refreshSettings(): Promise<void> {
    loaded.value = false
    await loadSettings()
  }

  // Check if user API key is allowed
  function allowUserApiKey(): boolean {
    return settings.value?.allow_user_api_key ?? false
  }

  // Auto-load settings on first use
  onMounted(() => {
    if (!loaded.value && !loading.value) {
      loadSettings()
    }
  })

  return {
    settings: readonly(settings),
    loading: readonly(loading),
    error: readonly(error),
    loaded: readonly(loaded),
    loadSettings,
    refreshSettings,
    allowUserApiKey,
  }
}
