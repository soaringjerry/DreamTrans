import { ref } from 'vue'
import { getUserBalance, type UserBalance } from '../../api'

const balance = ref<UserBalance | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)

export function useBalance() {
  async function fetchBalance() {
    loading.value = true
    error.value = null
    try {
      balance.value = await getUserBalance()
    } catch (e) {
      console.warn('Failed to fetch balance:', e)
      error.value = e instanceof Error ? e.message : 'Unknown error'
    } finally {
      loading.value = false
    }
  }

  function formatBalance(amount: number): string {
    if (amount >= 1000000) {
      return (amount / 1000000).toFixed(2) + 'M'
    } else if (amount >= 1000) {
      return (amount / 1000).toFixed(2) + 'K'
    } else {
      return amount.toFixed(2)
    }
  }

  return {
    balance,
    loading,
    error,
    fetchBalance,
    formatBalance,
  }
}
