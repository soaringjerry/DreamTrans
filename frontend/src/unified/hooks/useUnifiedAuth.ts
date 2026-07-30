import { useCallback, useEffect, useRef, useState } from 'react'
import {
  getSystemAccess,
  getUserBalance,
  type UserBalance,
} from '../../api'
import {
  AUTH_STATE_CHANGED_EVENT,
  getStoredUser,
  initAuth,
  login as loginRequest,
  logout as logoutRequest,
  register as registerRequest,
  type User,
} from '../../pro/api/auth'
import { getSystemSettings } from '../../pro/api/system'

export interface RegisterInput {
  email: string
  password: string
  name: string
  inviteCode?: string
}

export interface UnifiedAuthState {
  user: User | null
  balance: UserBalance | null
  checking: boolean
  submitting: boolean
  anonymousAllowed: boolean
  ragEnabled: boolean
  registrationEnabled: boolean
  allowUserApiKey: boolean
  error: string | null
  login: (email: string, password: string) => Promise<boolean>
  register: (input: RegisterInput) => Promise<boolean>
  logout: () => Promise<void>
  clearError: () => void
  refreshBalance: () => Promise<void>
}

export function useUnifiedAuth(): UnifiedAuthState {
  const [user, setUser] = useState<User | null>(null)
  const [balance, setBalance] = useState<UserBalance | null>(null)
  const [checking, setChecking] = useState(true)
  const [submitting, setSubmitting] = useState(false)
  const [anonymousAllowed, setAnonymousAllowed] = useState(false)
  const [ragEnabled, setRagEnabled] = useState(false)
  const [registrationEnabled, setRegistrationEnabled] = useState(false)
  const [allowUserApiKey, setAllowUserApiKey] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const balanceRequestRef = useRef(0)

  const refreshBalance = useCallback(async () => {
    const request = ++balanceRequestRef.current
    const ownerId = getStoredUser()?.id ?? null
    if (!ownerId) {
      if (request === balanceRequestRef.current) setBalance(null)
      return
    }
    try {
      const nextBalance = await getUserBalance()
      if (
        request !== balanceRequestRef.current
        || getStoredUser()?.id !== ownerId
      ) {
        return
      }
      if (nextBalance.user_id !== ownerId) {
        setBalance(null)
        return
      }
      setBalance(nextBalance)
    } catch {
      if (
        request === balanceRequestRef.current
        && getStoredUser()?.id === ownerId
      ) {
        setBalance(null)
      }
    }
  }, [])

  const initialize = useCallback(async () => {
    setChecking(true)
    try {
      const [authenticatedUser, access, systemSettings] = await Promise.all([
        initAuth(),
        getSystemAccess(),
        getSystemSettings().catch(() => ({ allow_user_api_key: false })),
      ])
      setUser(authenticatedUser)
      setRagEnabled(access.ragEnabled)
      setRegistrationEnabled(access.registrationEnabled)
      setAllowUserApiKey(systemSettings.allow_user_api_key === true)
      if (authenticatedUser) {
        setAnonymousAllowed(false)
        await refreshBalance()
      } else {
        setBalance(null)
        setAnonymousAllowed(access.anonymousAPIEnabled)
      }
    } finally {
      setChecking(false)
    }
  }, [refreshBalance])

  useEffect(() => {
    void initialize()
    const refreshAnonymousAccess = () => {
      void getSystemAccess().then((access) => {
        setAnonymousAllowed(access.anonymousAPIEnabled)
        setRagEnabled(access.ragEnabled)
        setRegistrationEnabled(access.registrationEnabled)
      })
      void getSystemSettings()
        .then((settings) => setAllowUserApiKey(settings.allow_user_api_key === true))
        .catch(() => setAllowUserApiKey(false))
    }
    const handleAuthChanged = () => {
      // Storage/Broadcast events run before the next render. Adopting the
      // stored identity synchronously prevents the workspace from retaining
      // account A as owner while authenticated requests already use B's token.
      const nextUser = getStoredUser()
      balanceRequestRef.current += 1
      setUser(nextUser)
      setBalance(null)
      if (nextUser) {
        setAnonymousAllowed(false)
        void refreshBalance()
      } else {
        refreshAnonymousAccess()
      }
    }
    window.addEventListener(AUTH_STATE_CHANGED_EVENT, handleAuthChanged)
    return () => {
      window.removeEventListener(AUTH_STATE_CHANGED_EVENT, handleAuthChanged)
    }
  }, [initialize, refreshBalance])

  const login = useCallback(async (email: string, password: string) => {
    setSubmitting(true)
    setError(null)
    try {
      const response = await loginRequest(email.trim(), password)
      setUser(response.user)
      setAnonymousAllowed(false)
      void refreshBalance()
      return true
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '登录失败')
      return false
    } finally {
      setSubmitting(false)
    }
  }, [refreshBalance])

  const register = useCallback(async (input: RegisterInput) => {
    if (!registrationEnabled) {
      setError('当前服务器未开放自主注册，请联系管理员创建账户。')
      return false
    }
    setSubmitting(true)
    setError(null)
    try {
      const response = await registerRequest(
        input.email.trim(),
        input.password,
        input.name.trim(),
        input.inviteCode?.trim() || undefined,
      )
      setUser(response.user)
      setAnonymousAllowed(false)
      void refreshBalance()
      return true
    } catch (reason) {
      const message = reason instanceof Error ? reason.message : '注册失败'
      if (/self-registration is disabled/i.test(message)) {
        setError('当前服务器未开放自主注册，请联系管理员创建账户。')
      } else if (/invalid registration invite code/i.test(message)) {
        setError('邀请码缺失或无效。')
      } else {
        setError(message)
      }
      return false
    } finally {
      setSubmitting(false)
    }
  }, [refreshBalance, registrationEnabled])

  const logout = useCallback(async () => {
    setSubmitting(true)
    try {
      await logoutRequest()
      // Server-side revocation is best effort and may take several seconds.
      // Another tab can log in during that window, so converge on the identity
      // that is stored now instead of letting the stale logout completion
      // overwrite the newer account with an anonymous React state.
      const currentUser = getStoredUser()
      balanceRequestRef.current += 1
      setUser(currentUser)
      setBalance(null)
      if (currentUser) {
        setAnonymousAllowed(false)
        void refreshBalance()
        return
      }
      const access = await getSystemAccess()
      setAnonymousAllowed(access.anonymousAPIEnabled)
      setRagEnabled(access.ragEnabled)
      setRegistrationEnabled(access.registrationEnabled)
      setAllowUserApiKey(
        await getSystemSettings()
          .then((settings) => settings.allow_user_api_key === true)
          .catch(() => false),
      )
    } finally {
      setSubmitting(false)
    }
  }, [refreshBalance])

  return {
    user,
    balance,
    checking,
    submitting,
    anonymousAllowed,
    ragEnabled,
    registrationEnabled,
    allowUserApiKey,
    error,
    login,
    register,
    logout,
    clearError: () => setError(null),
    refreshBalance,
  }
}
