import type { ReactNode, SVGProps } from 'react'

export type IconName =
  | 'archive'
  | 'arrow-down'
  | 'check'
  | 'close'
  | 'cloud'
  | 'download'
  | 'history'
  | 'language'
  | 'menu'
  | 'message'
  | 'mic'
  | 'more'
  | 'pause'
  | 'play'
  | 'settings'
  | 'sparkles'
  | 'stop'
  | 'user'
  | 'wave'

const paths: Record<IconName, ReactNode> = {
  archive: (
    <>
      <path d="M4 7h16v13H4z" />
      <path d="M3 3h18v4H3zM9 11h6" />
    </>
  ),
  'arrow-down': <path d="m6 9 6 6 6-6" />,
  check: <path d="m5 12 4 4L19 6" />,
  close: <path d="M6 6l12 12M18 6 6 18" />,
  cloud: <path d="M7 18h10a4 4 0 0 0 .5-8 6 6 0 0 0-11.6-1.8A5 5 0 0 0 7 18Z" />,
  download: <path d="M12 3v12m-5-5 5 5 5-5M4 19h16" />,
  history: (
    <>
      <path d="M3 12a9 9 0 1 0 3-6.7L3 8" />
      <path d="M3 3v5h5M12 7v5l3 2" />
    </>
  ),
  language: (
    <>
      <path d="M4 5h10M9 3v2c0 4-2 7-5 9m3-6c1 3 3 5 6 6" />
      <path d="m15 20 3-8 3 8m-5-3h4" />
    </>
  ),
  menu: <path d="M4 7h16M4 12h16M4 17h16" />,
  message: <path d="M4 5h16v12H8l-4 4V5Z" />,
  mic: (
    <>
      <rect x="9" y="3" width="6" height="12" rx="3" />
      <path d="M5 11a7 7 0 0 0 14 0M12 18v3" />
    </>
  ),
  more: (
    <>
      <circle cx="5" cy="12" r="1" fill="currentColor" stroke="none" />
      <circle cx="12" cy="12" r="1" fill="currentColor" stroke="none" />
      <circle cx="19" cy="12" r="1" fill="currentColor" stroke="none" />
    </>
  ),
  pause: (
    <>
      <path d="M8 5v14M16 5v14" />
    </>
  ),
  play: <path d="m8 5 11 7-11 7V5Z" />,
  settings: (
    <>
      <circle cx="12" cy="12" r="3" />
      <path d="M19 13.5v-3l-2-.7-.8-1.8.9-1.9-2.2-2.2-1.9.9-1.8-.8-.7-2h-3l-.7 2-1.8.8-1.9-.9-2.2 2.2.9 1.9-.8 1.8-2 .7v3l2 .7.8 1.8-.9 1.9 2.2 2.2 1.9-.9 1.8.8.7 2h3l.7-2 1.8-.8 1.9.9 2.2-2.2-.9-1.9.8-1.8 2-.7Z" />
    </>
  ),
  sparkles: (
    <>
      <path d="m12 3 1.2 4.2L17 9l-3.8 1.8L12 15l-1.2-4.2L7 9l3.8-1.8L12 3Z" />
      <path d="m19 15 .6 2.1L22 18l-2.4.9L19 21l-.6-2.1L16 18l2.4-.9L19 15Z" />
    </>
  ),
  stop: <rect x="6" y="6" width="12" height="12" rx="2" />,
  user: (
    <>
      <circle cx="12" cy="8" r="4" />
      <path d="M4 21a8 8 0 0 1 16 0" />
    </>
  ),
  wave: <path d="M3 12h2l2-6 3 12 3-9 2 6 2-3h4" />,
}

interface IconProps extends SVGProps<SVGSVGElement> {
  name: IconName
  size?: number
}

export function Icon({ name, size = 20, ...props }: IconProps) {
  return (
    <svg
      aria-hidden="true"
      fill="none"
      height={size}
      viewBox="0 0 24 24"
      width={size}
      stroke="currentColor"
      strokeLinecap="round"
      strokeLinejoin="round"
      strokeWidth="1.8"
      {...props}
    >
      {paths[name]}
    </svg>
  )
}
