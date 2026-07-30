import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import UnifiedApp from '../unified/UnifiedApp'

createRoot(document.getElementById('pro-root')!).render(
  <StrictMode>
    <UnifiedApp proEntry />
  </StrictMode>,
)
