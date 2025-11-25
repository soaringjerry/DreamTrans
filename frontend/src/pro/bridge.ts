export interface ProSegment {
  text: string
  startTime: number
  endTime: number
}

export interface ProTranscriptLine {
  id: number
  speaker: string
  confirmedSegments: ProSegment[]
  partialText: string
}

export interface ProTranslation {
  id: string
  speaker: string
  startTime: number
  content: string
  original?: string
  isPartial: boolean
}

export interface ProStateSnapshot {
  lines: ProTranscriptLine[]
  translations: ProTranslation[]
  isTranscribing: boolean
  isInitializing: boolean
  isPaused: boolean
  elapsedTime: number
  sessionId: string
  hiddenCounts?: {
    transcripts: number
    translations: number
  }
}

export type ProCommand =
  | { type: 'start' }
  | { type: 'stop' }
  | { type: 'continue' }
  | { type: 'pause-toggle' }
  | { type: 'download-audio' }
  | { type: 'download-transcript' }
  | { type: 'download-translation' }
  | { type: 'open-settings' }
  | { type: 'open-history' }

export function emitProState(state: ProStateSnapshot) {
  window.dispatchEvent(new CustomEvent<ProStateSnapshot>('dt-pro-state', { detail: state }))
}

export function onProState(listener: (state: ProStateSnapshot) => void) {
  const handler = (ev: Event) => {
    const detail = (ev as CustomEvent<ProStateSnapshot>).detail
    if (detail) listener(detail)
  }
  window.addEventListener('dt-pro-state', handler)
  return () => window.removeEventListener('dt-pro-state', handler)
}

export function emitProCommand(cmd: ProCommand) {
  window.dispatchEvent(new CustomEvent<ProCommand>('dt-pro-command', { detail: cmd }))
}

export function onProCommand(listener: (cmd: ProCommand) => void) {
  const handler = (ev: Event) => {
    const detail = (ev as CustomEvent<ProCommand>).detail
    if (detail) listener(detail)
  }
  window.addEventListener('dt-pro-command', handler)
  return () => window.removeEventListener('dt-pro-command', handler)
}
