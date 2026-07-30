import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import ProAdmin from './ProAdmin'

createRoot(document.getElementById('pro-admin-root')!).render(
  <StrictMode>
    <ProAdmin />
  </StrictMode>,
)
