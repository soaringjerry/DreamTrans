import { useEffect, type RefObject } from 'react'

const FOCUSABLE = 'button:not([disabled]), a[href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

/**
 * Keeps keyboard focus inside a modal dialog, closes on Escape and restores
 * focus to the opener when the dialog unmounts.
 */
export function useDialogFocusTrap(
  dialogRef: RefObject<HTMLElement | null>,
  onClose: () => void,
): void {
  useEffect(() => {
    const previouslyFocused = document.activeElement instanceof HTMLElement
      ? document.activeElement
      : null
    const focusable = () => {
      const dialog = dialogRef.current
      if (!dialog) return []
      return [...dialog.querySelectorAll<HTMLElement>(FOCUSABLE)].filter((element) => (
        element.getClientRects().length > 0
        && window.getComputedStyle(element).visibility !== 'hidden'
      ))
    }
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault()
        onClose()
        return
      }
      if (event.key !== 'Tab') return
      const dialog = dialogRef.current
      if (!dialog) return
      const elements = focusable()
      if (elements.length === 0) {
        event.preventDefault()
        dialog.focus()
        return
      }
      const first = elements[0]
      const last = elements[elements.length - 1]
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault()
        last?.focus()
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault()
        first?.focus()
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    const frame = window.requestAnimationFrame(() => {
      const dialog = dialogRef.current
      if (!dialog || dialog.contains(document.activeElement)) return
      const preferred = dialog.querySelector<HTMLElement>('[data-autofocus]')
      ;(preferred ?? focusable()[0] ?? dialog).focus()
    })
    return () => {
      window.cancelAnimationFrame(frame)
      window.removeEventListener('keydown', handleKeyDown)
      previouslyFocused?.focus()
    }
  }, [dialogRef, onClose])
}
