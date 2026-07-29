<script setup lang="ts">
import { ref, computed } from 'vue'
import { UserPlus, Mail, Lock, User, KeyRound, Eye, EyeOff, Loader2, ArrowRight } from 'lucide-vue-next'
import { useAuth } from '../composables/useAuth'

const emit = defineEmits<{
  (e: 'success'): void
  (e: 'switch-to-login'): void
}>()

const { register, loading, error, clearError } = useAuth()

const name = ref('')
const email = ref('')
const password = ref('')
const confirmPassword = ref('')
const inviteCode = ref('')
const showPassword = ref(false)
const localError = ref('')

const passwordsMatch = computed(() => {
  return password.value === confirmPassword.value
})

const passwordStrength = computed(() => {
  const p = password.value
  if (p.length < 10) return { level: 0, text: 'At least 10 characters' }
  const hasUpper = /[A-Z]/.test(p)
  const hasLower = /[a-z]/.test(p)
  const hasNumber = /\d/.test(p)
  const hasSpecial = /[!@#$%^&*(),.?":{}|<>]/.test(p)
  const score = [hasUpper, hasLower, hasNumber, hasSpecial].filter(Boolean).length
  if (score >= 4) return { level: 3, text: 'Strong' }
  if (score >= 3) return { level: 2, text: 'Good' }
  return { level: 1, text: 'Fair' }
})

const canSubmit = computed(() => {
  return (
    name.value.trim() &&
    email.value.trim() &&
    password.value.length >= 10 &&
    passwordsMatch.value &&
    !loading.value
  )
})

async function handleSubmit() {
  if (!canSubmit.value) return

  clearError()
  localError.value = ''

  try {
    await register(
      email.value.trim(),
      password.value,
      name.value.trim(),
      inviteCode.value.trim() || undefined,
    )
    emit('success')
  } catch (e) {
    const message = e instanceof Error ? e.message : 'Registration failed'
    if (/self-registration is disabled/i.test(message)) {
      localError.value = 'Registration is disabled on this server. Ask an administrator to enable it or create your account.'
    } else if (/invalid registration invite code/i.test(message)) {
      localError.value = 'The invite code is missing or invalid. Ask an administrator for a valid code.'
    } else {
      localError.value = message
    }
  }
}

function togglePassword() {
  showPassword.value = !showPassword.value
}
</script>

<template>
  <div class="auth-page">
    <div class="auth-container">
      <!-- Logo/Brand -->
      <div class="auth-brand">
        <div class="brand-icon">
          <UserPlus :size="24" />
        </div>
        <h1 class="brand-title">Create Account</h1>
        <p class="brand-subtitle">Start your DreamTrans journey</p>
      </div>

      <!-- Error Message -->
      <div v-if="localError || error" class="auth-error">
        {{ localError || error }}
      </div>

      <!-- Register Form -->
      <form class="auth-form" @submit.prevent="handleSubmit">
        <div class="form-group">
          <label for="name" class="form-label">Name</label>
          <div class="input-wrapper">
            <User class="input-icon" :size="18" />
            <input
              id="name"
              v-model="name"
              type="text"
              class="form-input"
              placeholder="Your name"
              autocomplete="name"
              :disabled="loading"
            />
          </div>
        </div>

        <div class="form-group">
          <label for="email" class="form-label">Email</label>
          <div class="input-wrapper">
            <Mail class="input-icon" :size="18" />
            <input
              id="email"
              v-model="email"
              type="email"
              class="form-input"
              placeholder="you@example.com"
              autocomplete="email"
              :disabled="loading"
            />
          </div>
        </div>

        <div class="form-group">
          <label for="password" class="form-label">Password</label>
          <div class="input-wrapper">
            <Lock class="input-icon" :size="18" />
            <input
              id="password"
              v-model="password"
              :type="showPassword ? 'text' : 'password'"
              class="form-input"
              placeholder="••••••••"
              autocomplete="new-password"
              :disabled="loading"
            />
            <button
              type="button"
              class="password-toggle"
              @click="togglePassword"
              tabindex="-1"
            >
              <EyeOff v-if="showPassword" :size="18" />
              <Eye v-else :size="18" />
            </button>
          </div>
          <div v-if="password" class="password-strength">
            <div class="strength-bar">
              <div
                class="strength-fill"
                :class="`strength-${passwordStrength.level}`"
                :style="{ width: `${(passwordStrength.level / 3) * 100}%` }"
              />
            </div>
            <span class="strength-text">{{ passwordStrength.text }}</span>
          </div>
        </div>

        <div class="form-group">
          <label for="confirm-password" class="form-label">Confirm Password</label>
          <div class="input-wrapper">
            <Lock class="input-icon" :size="18" />
            <input
              id="confirm-password"
              v-model="confirmPassword"
              :type="showPassword ? 'text' : 'password'"
              class="form-input"
              :class="{ 'input-error': confirmPassword && !passwordsMatch }"
              placeholder="••••••••"
              autocomplete="new-password"
              :disabled="loading"
            />
          </div>
          <div v-if="confirmPassword && !passwordsMatch" class="field-error">
            Passwords do not match
          </div>
        </div>

        <div class="form-group">
          <label for="invite-code" class="form-label">Invite code <span class="optional">(optional)</span></label>
          <div class="input-wrapper">
            <KeyRound class="input-icon" :size="18" />
            <input
              id="invite-code"
              v-model="inviteCode"
              type="text"
              class="form-input"
              placeholder="Required on some servers"
              autocomplete="off"
              :disabled="loading"
            />
          </div>
          <p class="auth-hint">Self-registration is disabled by default and may require an administrator-provided invite code.</p>
        </div>

        <button
          type="submit"
          class="submit-btn"
          :disabled="!canSubmit"
        >
          <Loader2 v-if="loading" class="btn-icon spinning" :size="18" />
          <span>Create Account</span>
          <ArrowRight v-if="!loading" class="btn-icon-right" :size="18" />
        </button>
      </form>

      <!-- Footer -->
      <div class="auth-footer">
        <span>Already have an account?</span>
        <button class="link-btn" @click="emit('switch-to-login')">
          Sign in
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.auth-page {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 2rem;
  background: linear-gradient(135deg, #0f0f14 0%, #1a1a24 50%, #0f0f14 100%);
}

.auth-container {
  width: 100%;
  max-width: 400px;
  padding: 2rem;
  background: rgba(30, 30, 40, 0.6);
  backdrop-filter: blur(20px);
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 16px;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.3);
}

.auth-brand {
  text-align: center;
  margin-bottom: 2rem;
}

.brand-icon {
  width: 56px;
  height: 56px;
  margin: 0 auto 1rem;
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(135deg, #10b981, #06b6d4);
  border-radius: 14px;
  color: white;
}

.brand-title {
  font-size: 1.5rem;
  font-weight: 600;
  color: #fff;
  margin: 0 0 0.5rem;
}

.brand-subtitle {
  font-size: 0.9rem;
  color: rgba(255, 255, 255, 0.5);
  margin: 0;
}

.auth-error {
  padding: 0.75rem 1rem;
  margin-bottom: 1.5rem;
  background: rgba(239, 68, 68, 0.1);
  border: 1px solid rgba(239, 68, 68, 0.3);
  border-radius: 8px;
  color: #f87171;
  font-size: 0.875rem;
}

.auth-form {
  display: flex;
  flex-direction: column;
  gap: 1.25rem;
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.form-label {
  font-size: 0.875rem;
  font-weight: 500;
  color: rgba(255, 255, 255, 0.7);
}

.optional {
  color: rgba(255, 255, 255, 0.35);
  font-weight: 400;
}

.auth-hint {
  margin: 0;
  color: rgba(255, 255, 255, 0.45);
  font-size: 0.75rem;
  line-height: 1.45;
}

.input-wrapper {
  position: relative;
  display: flex;
  align-items: center;
}

.input-icon {
  position: absolute;
  left: 12px;
  color: rgba(255, 255, 255, 0.3);
  pointer-events: none;
}

.form-input {
  width: 100%;
  padding: 0.75rem 2.5rem;
  background: rgba(255, 255, 255, 0.05);
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 8px;
  color: #fff;
  font-size: 0.95rem;
  transition: all 0.2s;
}

.form-input::placeholder {
  color: rgba(255, 255, 255, 0.3);
}

.form-input:focus {
  outline: none;
  border-color: #10b981;
  background: rgba(255, 255, 255, 0.08);
  box-shadow: 0 0 0 3px rgba(16, 185, 129, 0.1);
}

.form-input:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.form-input.input-error {
  border-color: #ef4444;
}

.password-toggle {
  position: absolute;
  right: 12px;
  background: none;
  border: none;
  color: rgba(255, 255, 255, 0.3);
  cursor: pointer;
  padding: 4px;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: color 0.2s;
}

.password-toggle:hover {
  color: rgba(255, 255, 255, 0.6);
}

.password-strength {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  margin-top: 0.25rem;
}

.strength-bar {
  flex: 1;
  height: 4px;
  background: rgba(255, 255, 255, 0.1);
  border-radius: 2px;
  overflow: hidden;
}

.strength-fill {
  height: 100%;
  border-radius: 2px;
  transition: width 0.3s, background-color 0.3s;
}

.strength-0 { background: #ef4444; }
.strength-1 { background: #f59e0b; }
.strength-2 { background: #10b981; }
.strength-3 { background: #06b6d4; }

.strength-text {
  font-size: 0.75rem;
  color: rgba(255, 255, 255, 0.5);
  min-width: 50px;
}

.field-error {
  font-size: 0.75rem;
  color: #f87171;
}

.submit-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0.5rem;
  padding: 0.875rem 1.5rem;
  margin-top: 0.5rem;
  background: linear-gradient(135deg, #10b981, #06b6d4);
  border: none;
  border-radius: 8px;
  color: #fff;
  font-size: 0.95rem;
  font-weight: 500;
  cursor: pointer;
  transition: all 0.2s;
}

.submit-btn:hover:not(:disabled) {
  transform: translateY(-1px);
  box-shadow: 0 4px 12px rgba(16, 185, 129, 0.4);
}

.submit-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
  transform: none;
}

.btn-icon {
  flex-shrink: 0;
}

.btn-icon-right {
  margin-left: auto;
}

.spinning {
  animation: spin 1s linear infinite;
}

@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}

.auth-footer {
  margin-top: 1.5rem;
  text-align: center;
  font-size: 0.875rem;
  color: rgba(255, 255, 255, 0.5);
}

.link-btn {
  background: none;
  border: none;
  color: #34d399;
  font-size: inherit;
  cursor: pointer;
  padding: 0;
  margin-left: 0.25rem;
  transition: color 0.2s;
}

.link-btn:hover {
  color: #6ee7b7;
  text-decoration: underline;
}
</style>
