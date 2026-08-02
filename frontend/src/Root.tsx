import LandingPage from './landing/LandingPage'
import UnifiedApp from './unified/UnifiedApp'

function shouldOpenWorkspace(pathname: string, search: string): boolean {
  if (
    pathname === '/pro'
    || pathname === '/pro.html'
    || pathname.startsWith('/pro/')
  ) {
    return true
  }
  if (pathname === '/app' || pathname === '/workspace') {
    return true
  }
  if (pathname === '/' || pathname === '/index.html') {
    const params = new URLSearchParams(search)
    return params.get('app') === '1'
  }
  return false
}

export default function Root() {
  const openWorkspace = shouldOpenWorkspace(
    window.location.pathname,
    window.location.search,
  )

  if (!openWorkspace) {
    return <LandingPage />
  }

  return <UnifiedApp />
}
