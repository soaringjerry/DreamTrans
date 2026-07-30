import { useState, type FormEvent } from 'react'
import type { RegisterInput } from '../hooks/useUnifiedAuth'
import { Icon } from './Icon'

interface AuthGateProps {
  allowAnonymous: boolean
  error: string | null
  proEntry: boolean
  registrationEnabled: boolean
  submitting: boolean
  onContinueAnonymous: () => void
  onLogin: (email: string, password: string) => Promise<boolean>
  onRegister: (input: RegisterInput) => Promise<boolean>
}

export function AuthGate({
  allowAnonymous,
  error,
  proEntry,
  registrationEnabled,
  submitting,
  onContinueAnonymous,
  onLogin,
  onRegister,
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

  return (
    <main className="dt-auth">
      <section className="dt-auth__intro" aria-labelledby="auth-title">
        <div className="dt-auth__mark"><Icon name="wave" size={26} /></div>
        <p className="dt-eyebrow">DreamTrans</p>
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

        {error && <div className="dt-form-error" role="alert">{error}</div>}

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
            minLength={6}
            onChange={(event) => setPassword(event.target.value)}
            placeholder="至少 6 位"
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
