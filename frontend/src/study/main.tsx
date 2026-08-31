import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { StudyApp } from './StudyApp'

createRoot(document.getElementById('study-root')!).render(
  <StrictMode>
    <StudyApp />
  </StrictMode>,
)
