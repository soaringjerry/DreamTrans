import { useEffect, useState, type FormEvent } from 'react'
import type {
  PendingVerification,
  RegisterInput,
  VerificationOutcome,
} from '../hooks/useUnifiedAuth'
import { Icon } from './Icon'

interface AuthGateProps {
  allowAnonymous: boolean
  emailVerificationRequired: boolean
  error: string | null
  pendingVerification: PendingVerification | null
  proEntry: boolean
  registrationEnabled: boolean
  submitting: boolean
  verificationOutcome: VerificationOutcome | null
  onContinueAnonymous: () => void
  onDismissVerification: () => void
  onLogin: (email: string, password: string) => Promise<boolean>
  onRegister: (input: RegisterInput) => Promise<boolean>
  onResendVerification: (email: string) => Promise<boolean>
}

/** Seconds the resend button stays disabled after a send, matching the server cooldown. */
const RESEND_COOLDOWN_SECONDS = 60

export function AuthGate({
  allowAnonymous,
  emailVerificationRequired,
  error,
  pendingVerification,
  proEntry,
  registrationEnabled,
  submitting,
  verificationOutcome,
  onContinueAnonymous,
  onDismissVerification,
  onLogin,
  onRegister,
  onResendVerification,
}: AuthGateProps) {
  const [registering, setRegistering] = useState(false)
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [name, setName] = useState('')
  const [inviteCode, setInviteCode] = useState('')

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (registering) {
      await onRegister({ email, password, name, inviteCode })
    } else {
      await onLogin(email, password)
    }
  }

  if (pendingVerification) {
    return (
      <VerificationPending
        error={error}
        pending={pendingVerification}
        submitting={submitting}
        onBack={() => {
          // "Back to login" should land on the login form even when the
          // user arrived here from the sign-up form.
          setRegistering(false)
          setPassword('')
          onDismissVerification()
        }}
        onResend={onResendVerification}
      />
    )
  }

  return (
    <main className="dt-auth">
      <section className="dt-auth__intro" aria-labelledby="auth-title">
        <div className="dt-auth__mark"><Icon name="wave" size={26} /></div>
        <p className="dt-eyebrow">Yufolo</p>
        <h1 id="auth-title">让对话沉淀为清晰、可用的文字。</h1>
        <p>
          实时转录、双语翻译与 AI 辅助集中在同一个工作台。
          桌面与手机使用完全一致的会话体验。
        </p>
        <div className="dt-auth__signals" aria-label="产品特点">
          <span><Icon name="check" size={15} /> 实时双语</span>
          <span><Icon name="check" size={15} /> 长会话优化</span>
          <span><Icon name="check" size={15} /> 云端同步</span>
        </div>
      </section>

      <form className="dt-auth__card" onSubmit={(event) => { void submit(event) }}>
        <div>
          <p className="dt-eyebrow">{proEntry ? 'Pro workspace' : 'Workspace'}</p>
          <h2>{registering ? '创建账户' : '欢迎回来'}</h2>
          <p className="dt-muted">
            {registering ? '创建账户后即可使用云端会话。' : '登录以继续使用你的会话。'}
          </p>
        </div>

        {verificationOutcome?.status === 'invalid' && !error && (
          <div className="dt-form-error" role="alert">
            这个验证链接已失效或已被使用。请登录后重新发送验证邮件。
          </div>
        )}
        {error && <div className="dt-form-error" role="alert">{error}</div>}

        {registering && emailVerificationRequired && (
          <p className="dt-auth__hint">
            <Icon name="message" size={15} />
            注册后我们会发一封验证邮件，点击其中的链接即可激活账户。
          </p>
        )}

        {registering && (
          <label className="dt-field">
            <span>姓名</span>
            <input
              autoComplete="name"
              onChange={(event) => setName(event.target.value)}
              placeholder="你的名字"
              required
              value={name}
            />
          </label>
        )}

        <label className="dt-field">
          <span>邮箱</span>
          <input
            autoComplete="username"
            inputMode="email"
            onChange={(event) => setEmail(event.target.value)}
            placeholder="name@example.com"
            required
            type="email"
            value={email}
          />
        </label>

        <label className="dt-field">
          <span>密码</span>
          <input
            autoComplete={registering ? 'new-password' : 'current-password'}
            minLength={registering ? 10 : 6}
            onChange={(event) => setPassword(event.target.value)}
            placeholder={registering ? '至少 10 位' : '输入密码'}
            required
            type="password"
            value={password}
          />
        </label>

        {registering && (
          <label className="dt-field">
            <span>邀请码 <small>可选</small></span>
            <input
              autoComplete="off"
              onChange={(event) => setInviteCode(event.target.value)}
              placeholder="由管理员提供"
              value={inviteCode}
            />
          </label>
        )}

        <button className="dt-button dt-button--primary dt-button--wide" disabled={submitting}>
          {submitting ? '请稍候…' : registering ? '创建账户' : '登录'}
        </button>

        {registrationEnabled ? (
          <button
            className="dt-button dt-button--text"
            onClick={() => setRegistering((value) => !value)}
            type="button"
          >
            {registering ? '已有账户？返回登录' : '没有账户？创建一个'}
          </button>
        ) : (
          <p className="dt-muted">当前服务器由管理员创建账户，未开放自主注册。</p>
        )}

        {allowAnonymous && !proEntry && (
          <>
            <div className="dt-auth__divider"><span>或</span></div>
            <button
              className="dt-button dt-button--secondary dt-button--wide"
              onClick={onContinueAnonymous}
              type="button"
            >
              使用本地模式
            </button>
          </>
        )}
      </form>
    </main>
  )
}

interface VerificationPendingProps {
  error: string | null
  pending: PendingVerification
  submitting: boolean
  onBack: () => void
  onResend: (email: string) => Promise<boolean>
}

function VerificationPending({
  error,
  pending,
  submitting,
  onBack,
  onResend,
}: VerificationPendingProps) {
  // A fresh sign-up already has a mail in flight; a failed delivery or a
  // login bounce starts with the button enabled.
  const [cooldown, setCooldown] = useState(pending.mailInFlight ? RESEND_COOLDOWN_SECONDS : 0)
  const [sentAgain, setSentAgain] = useState(false)

  useEffect(() => {
    if (cooldown <= 0) return
    const timer = window.setTimeout(() => setCooldown((value) => value - 1), 1_000)
    return () => window.clearTimeout(timer)
  }, [cooldown])

  const resend = async () => {
    const ok = await onResend(pending.email)
    if (ok) {
      setSentAgain(true)
      setCooldown(RESEND_COOLDOWN_SECONDS)
    }
  }

  return (
    <main className="dt-auth">
      <section className="dt-auth__intro" aria-labelledby="verify-title">
        <div className="dt-auth__mark"><Icon name="wave" size={26} /></div>
        <p className="dt-eyebrow">Yufolo</p>
        <h1 id="verify-title">还差一步：验证你的邮箱</h1>
        <p>
          验证之后账户才会激活并获得试用额度。链接 24 小时内有效。
        </p>
      </section>

      <div className="dt-auth__card dt-auth__card--verify" data-testid="verification-pending">
        <div className="dt-auth__verify-icon"><Icon name="message" size={26} /></div>
        <div>
          <p className="dt-eyebrow">查收邮件</p>
          <h2>{pending.deliveryFailed ? '验证邮件发送失败' : '我们给你发了一封验证邮件'}</h2>
          <p className="dt-muted">
            {pending.deliveryFailed
              ? <>账户已创建，但发往 <strong>{pending.email}</strong> 的邮件没有送出。请点击下面重新发送。</>
              : <>请打开 <strong>{pending.email}</strong> 的收件箱，点击邮件里的「验证邮箱」。没有收到的话，看看垃圾邮件文件夹。</>}
          </p>
        </div>

        {error && <div className="dt-form-error" role="alert">{error}</div>}
        {sentAgain && !error && (
          <p className="dt-auth__hint" role="status">
            <Icon name="check" size={15} />
            已重新发送。如果之前的邮件还在，只有最新一封里的链接有效。
          </p>
        )}

        <button
          className="dt-button dt-button--primary dt-button--wide"
          disabled={submitting || cooldown > 0}
          onClick={() => { void resend() }}
          type="button"
        >
          {submitting
            ? '正在发送…'
            : cooldown > 0
              ? `重新发送（${cooldown} 秒后可用）`
              : '重新发送验证邮件'}
        </button>
        <button className="dt-button dt-button--text" onClick={onBack} type="button">
          返回登录
        </button>
      </div>
    </main>
  )
}
