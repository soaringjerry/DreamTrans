import React from 'react'

export default function TopBar() {
  const openSettings = () => {
    window.dispatchEvent(new CustomEvent('dt-open-settings'))
  }
  const openHistory = () => {
    window.dispatchEvent(new CustomEvent('dt-open-history'))
  }
  return (
    <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginBottom: '8px' }}>
      <button className="btn btn-secondary" onClick={openHistory}>历史</button>
      <button className="btn btn-secondary" onClick={openSettings}>设置</button>
    </div>
  )
}

