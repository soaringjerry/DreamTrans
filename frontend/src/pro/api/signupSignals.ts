// Optional first-party signup observations; the server treats them as untrusted.
// Coarse cohorts are not unique device IDs and do not use canvas/audio probes.
export function collectSignupSignals() {
  try {
    if (typeof navigator === 'undefined' || typeof screen === 'undefined') return undefined
    return {
      version: 1,
      platform: navigator.platform.slice(0, 64),
      language: navigator.language.slice(0, 32),
      timezone: Intl.DateTimeFormat().resolvedOptions().timeZone.slice(0, 64),
      screen_width: Math.floor(screen.width / 200) * 200,
      screen_height: Math.floor(screen.height / 200) * 200,
      cores: Math.floor((navigator.hardwareConcurrency || 0) / 2) * 2,
      touch_points: navigator.maxTouchPoints || 0,
      webdriver: navigator.webdriver === true,
    }
  } catch {
    return undefined
  }
}
