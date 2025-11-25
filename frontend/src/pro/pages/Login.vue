<script setup lang="ts">
import { ref, computed } from 'vue'
import { LogIn, Mail, Lock, Eye, EyeOff, Loader2, ArrowRight } from 'lucide-vue-next'
import { useAuth } from '../composables/useAuth'

const emit = defineEmits<{
  (e: 'success'): void
  (e: 'switch-to-register'): void
}>()

const { login, loading, error, clearError } = useAuth()

const email = ref('')
const password = ref('')
const showPassword = ref(false)
const localError = ref('')

const canSubmit = computed(() => {
  return email.value.trim() && password.value.length >= 6 && !loading.value
})

async function handleSubmit() {
  if (!canSubmit.value) return

  clearError()
  localError.value = ''

  try {
    await login(email.value.trim(), password.value)
    emit('success')
  } catch (e) {
    localError.value = e instanceof Error ? e.message : 'Login failed'
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
          <LogIn :size="24" />
        </div>
        <h1 class="brand-title">DreamTrans Pro</h1>
        <p class="brand-subtitle">Sign in to continue</p>
      </div>

      <!-- Error Message -->
      <div v-if="localError || error" class="auth-error">
        {{ localError || error }}
      </div>

      <!-- Login Form -->
      <form class="auth-form" @submit.prevent="handleSubmit">
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
              autocomplete="current-password"
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
        </div>

        <button
          type="submit"
          class="submit-btn"
          :disabled="!canSubmit"
        >
          <Loader2 v-if="loading" class="btn-icon spinning" :size="18" />
          <span>Sign In</span>
          <ArrowRight v-if="!loading" class="btn-icon-right" :size="18" />
        </button>
      </form>

      <!-- Footer -->
      <div class="auth-footer">
        <span>Don't have an account?</span>
        <button class="link-btn" @click="emit('switch-to-register')">
          Create one
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
  background: linear-gradient(135deg, #6366f1, #8b5cf6);
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
  border-color: #6366f1;
  background: rgba(255, 255, 255, 0.08);
  box-shadow: 0 0 0 3px rgba(99, 102, 241, 0.1);
}

.form-input:disabled {
  opacity: 0.5;
  cursor: not-allowed;
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

.submit-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0.5rem;
  padding: 0.875rem 1.5rem;
  margin-top: 0.5rem;
  background: linear-gradient(135deg, #6366f1, #8b5cf6);
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
  box-shadow: 0 4px 12px rgba(99, 102, 241, 0.4);
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
  color: #818cf8;
  font-size: inherit;
  cursor: pointer;
  padding: 0;
  margin-left: 0.25rem;
  transition: color 0.2s;
}

.link-btn:hover {
  color: #a5b4fc;
  text-decoration: underline;
}
</style>
