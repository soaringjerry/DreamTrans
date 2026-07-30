import { checkSpeechmaticsPreflight } from '../../pro/api/auth'

function errorMessage(reason: unknown): string {
  if (reason instanceof Error) return reason.message.trim()
  return String(reason ?? '').trim()
}

export function speechmaticsPreflightErrorMessage(reason: unknown): string {
  const message = errorMessage(reason)
  const normalized = message.toLowerCase()

  if (
    normalized.includes('insufficient balance')
    || normalized.includes('balance is insufficient')
  ) {
    return 'Dreampoints 余额不足，无法开始转录；请充值，或联系管理员关闭计费。'
  }
  if (normalized.includes('websocket origin not allowed')) {
    return '转录连接被反向代理的 Origin 校验拒绝；管理员需保留公网 Host，或把当前站点加入 CORS_ALLOWED_ORIGINS。'
  }
  if (
    normalized.includes('transcription quota exceeded')
    || normalized.includes('monthly api quota exceeded')
  ) {
    return '本月转录额度已用尽；请联系管理员调整套餐，或等待额度重置。'
  }
  if (
    normalized.includes('session expired')
    || normalized.includes('not authenticated')
    || normalized.includes('authentication state changed')
    || normalized.includes('authentication required')
    || normalized.includes('invalid or expired access token')
    || normalized.includes('invalid credentials')
  ) {
    return '登录状态已失效；请重新登录后再开始转录。'
  }
  if (
    normalized.includes('rate limit exceeded')
    || normalized.includes('too many active websocket connections')
  ) {
    return '请求过于频繁或转录连接数已满；请稍后再试。'
  }
  if (
    normalized.includes('service unavailable')
    || normalized.includes('temporarily unavailable')
    || normalized.includes('preflight returned an invalid response')
  ) {
    return '转录服务暂时不可用；请稍后重试，若持续出现请联系管理员检查服务状态。'
  }

  return message
    ? `转录服务预检失败：${message}`
    : '转录服务预检失败；请稍后重试。'
}

export async function ensureSpeechmaticsPreflight(): Promise<void> {
  try {
    const clientOrigin = typeof window === 'undefined' ? '' : window.location.origin
    await checkSpeechmaticsPreflight(clientOrigin)
  } catch (reason) {
    throw new Error(speechmaticsPreflightErrorMessage(reason), {
      cause: reason,
    })
  }
}
