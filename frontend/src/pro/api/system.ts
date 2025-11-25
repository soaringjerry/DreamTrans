// System settings API for DreamTrans Pro
const BACKEND_URL = import.meta.env.VITE_BACKEND_URL || 'http://localhost:8080'
const isProduction = BACKEND_URL === '/'
const baseUrl = isProduction ? '' : BACKEND_URL

export interface SystemSettings {
  allow_user_api_key: boolean
}

// Get system settings (public endpoint)
export async function getSystemSettings(): Promise<SystemSettings> {
  const response = await fetch(`${baseUrl}/api/system/settings`)
  if (!response.ok) {
    throw new Error('Failed to fetch system settings')
  }
  return response.json()
}

// Check if user can use their own API key
export async function canUseOwnApiKey(): Promise<boolean> {
  try {
    const settings = await getSystemSettings()
    return settings.allow_user_api_key
  } catch {
    return false
  }
}
