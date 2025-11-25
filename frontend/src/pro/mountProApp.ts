import type { App as VueApp } from 'vue'
import { createApp } from 'vue'
import ProApp from './ProApp.vue'

export interface ProAppOptions {
  onBackToClassic?: () => void
}

// Mount the Vue-based Pro UI into the provided container.
// Returns a cleanup function that unmounts the Vue app.
export function mountProApp(container: HTMLElement, options?: ProAppOptions) {
  let app: VueApp<Element> | null = null
  if (container.childElementCount > 0) {
    container.innerHTML = ''
  }
  app = createApp(ProApp, options)
  app.mount(container)
  return () => {
    app?.unmount()
    app = null
    container.innerHTML = ''
  }
}
