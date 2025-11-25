import { useEffect, useRef } from 'react'
import { mountProApp } from './mountProApp'
import './pro-shell.css'

interface Props {
  onBackToClassic?: () => void
}

// Hosts the Vue-based Pro UI inside the existing React application.
export default function ProShell({ onBackToClassic }: Props) {
  const mountRef = useRef<HTMLDivElement | null>(null)

  useEffect(() => {
    if (!mountRef.current) return undefined
    const cleanup = mountProApp(mountRef.current, { onBackToClassic })
    return () => cleanup()
  }, [onBackToClassic])

  return (
    <div className="pro-shell">
      <div className="pro-shell__mount" ref={mountRef} />
    </div>
  )
}
