import type { IndexedDbSessionRepository } from '../../core/session'
import type {
  TranscriptSegment,
  TranslationSegment,
} from '../../core/transcription/types'

interface WritableFileTarget {
  write(data: Blob): Promise<void>
  close(): Promise<void>
  abort?(): Promise<void>
}

interface SaveFileHandle {
  createWritable(): Promise<WritableFileTarget>
}

interface SaveFilePickerResult {
  handle?: SaveFileHandle
  reason?: unknown
}

export interface CompleteAudioSaveRequest {
  result: Promise<SaveFilePickerResult>
}

interface SaveFilePickerWindow extends Window {
  showSaveFilePicker?: (options: {
    suggestedName: string
    types: Array<{
      description: string
      accept: Record<string, string[]>
    }>
  }) => Promise<SaveFileHandle>
}

function safeFilename(value: string): string {
  return (
    value
      .trim()
      .replace(/[<>:"/\\|?*]/g, '-')
      .replace(/\p{Cc}/gu, '')
      .replace(/\s+/g, ' ')
      .slice(0, 90)
    || 'DreamTrans 会话'
  )
}

function audioExtension(mimeType: string | undefined): string {
  if (mimeType?.includes('mp4')) return 'm4a'
  if (mimeType?.includes('ogg')) return 'ogg'
  if (mimeType?.includes('mpeg')) return 'mp3'
  return 'webm'
}

function triggerBlobDownload(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = filename
  anchor.style.display = 'none'
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  window.setTimeout(() => URL.revokeObjectURL(url), 1_000)
}

function isUserCancellation(reason: unknown): boolean {
  return reason instanceof DOMException && reason.name === 'AbortError'
}

/**
 * Calls the file picker synchronously from the click handler so Chromium keeps
 * transient user activation. The settled wrapper is attached immediately to
 * avoid an unhandled rejection while the recorder and IndexedDB writes flush.
 */
export function requestCompleteAudioSave(
  title: string,
  mimeType = 'audio/webm',
): CompleteAudioSaveRequest | null {
  const pickerWindow = window as SaveFilePickerWindow
  const picker = pickerWindow.showSaveFilePicker
  if (!picker) return null

  const pickerMimeType = mimeType.split(';', 1)[0] || 'audio/webm'
  const filename = `${safeFilename(title)}.${audioExtension(mimeType)}`
  const result = picker.call(pickerWindow, {
    suggestedName: filename,
    types: [{
      description: '完整录音',
      accept: { [pickerMimeType]: [`.${audioExtension(mimeType)}`] },
    }],
  }).then(
    (handle): SaveFilePickerResult => ({ handle }),
    (reason): SaveFilePickerResult => ({ reason }),
  )
  return { result }
}

/**
 * Downloads the complete recording without ever rebuilding it during autosave.
 * Chromium can stream IndexedDB chunks straight into the chosen file; other
 * browsers assemble one Blob only for this explicit user action.
 */
export async function downloadCompleteAudio(
  repository: IndexedDbSessionRepository<TranscriptSegment, TranslationSegment>,
  sessionId: string,
  title: string,
  saveRequest: CompleteAudioSaveRequest | null = null,
): Promise<boolean> {
  const metadata = await repository.getSessionMetadata(sessionId)
  if (!metadata || metadata.audioChunkCount === 0) return false
  const mimeType = metadata.audioMimeType || 'audio/webm'
  const filename = `${safeFilename(title)}.${audioExtension(mimeType)}`

  if (saveRequest) {
    const pickerResult = await saveRequest.result
    if (pickerResult.reason && isUserCancellation(pickerResult.reason)) return false
    // If invoking the picker itself failed (unsupported MIME declaration,
    // permissions, etc.), retain a working explicit-download fallback.
    if (pickerResult.handle) {
      let writable: WritableFileTarget | null = null
      try {
        writable = await pickerResult.handle.createWritable()
        for await (const chunk of repository.iterateAudioChunks(sessionId, 25)) {
          await writable.write(chunk.blob)
        }
        await writable.close()
        return true
      } catch (reason) {
        await writable?.abort?.().catch(() => undefined)
        if (isUserCancellation(reason)) return false
        throw reason
      }
    }
  }

  const blob = await repository.getCompleteAudioBlob(sessionId, mimeType)
  if (!blob) return false
  triggerBlobDownload(blob, filename)
  return true
}

export type TextDownloadMode = 'original' | 'translation' | 'bilingual'

function formatTimestamp(seconds: number): string {
  const safe = Math.max(0, Math.floor(seconds))
  const hours = Math.floor(safe / 3_600)
  const minutes = Math.floor((safe % 3_600) / 60)
  const remainder = safe % 60
  return [
    String(hours).padStart(2, '0'),
    String(minutes).padStart(2, '0'),
    String(remainder).padStart(2, '0'),
  ].join(':')
}

export async function downloadSessionText(
  repository: IndexedDbSessionRepository<TranscriptSegment, TranslationSegment>,
  sessionId: string,
  title: string,
  mode: TextDownloadMode,
): Promise<boolean> {
  const parts: BlobPart[] = []
  const translations = new Map<string, TranslationSegment>()

  if (mode !== 'original') {
    for await (const record of repository.iterateTranslations(sessionId, 500)) {
      if (record.data.segmentId) translations.set(record.data.segmentId, record.data)
      else if (mode === 'translation') {
        parts.push(
          `[${formatTimestamp(record.data.startTime)}] ${record.data.speaker}\n`,
          `${record.data.text}\n\n`,
        )
      }
    }
  }

  let found = parts.length > 0
  for await (const record of repository.iterateTranscripts(sessionId, 500)) {
    const segment = record.data
    const translation = translations.get(segment.id)
    if (mode === 'translation' && !translation) continue
    found = true
    parts.push(`[${formatTimestamp(segment.startTime)}] ${segment.speaker}\n`)
    if (mode !== 'translation') parts.push(`${segment.text}\n`)
    if (mode !== 'original' && translation) parts.push(`${translation.text}\n`)
    parts.push('\n')
  }

  if (!found) return false
  const suffix = mode === 'bilingual' ? '双语' : mode === 'translation' ? '译文' : '原文'
  triggerBlobDownload(
    new Blob(parts, { type: 'text/plain;charset=utf-8' }),
    `${safeFilename(title)}-${suffix}.txt`,
  )
  return true
}
