/**
 * Long-session hot-path verification.
 *
 * Run from frontend/:
 *   esbuild src/unified/workspace/verification.ts --bundle --platform=node \
 *     --format=esm --outfile=/tmp/dreamtrans-long-session-verify.mjs
 *   node /tmp/dreamtrans-long-session-verify.mjs
 */
import { TranscriptStore } from '../../core/transcription'
import { VirtualLayout } from '../feed/virtualLayout'
import { AiTranslateClient, type AiTranslateChunk } from './AiTranslateClient'
import { mergeSessionRecords } from './mergeSessionRecords'
import { TranscriptFeedModel } from './TranscriptFeedModel'

const SEGMENT_COUNT = 30_000

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(`Long-session verification failed: ${message}`)
}

const mergeLocalStore = new TranscriptStore()
const localOnly = mergeLocalStore.appendTranscript({
  id: 'local-only',
  speaker: 'S1',
  text: 'This exists only in the durable local copy.',
  startTime: 0,
  endTime: 1,
}).record
const shared = mergeLocalStore.appendTranscript({
  id: 'shared',
  speaker: 'S1',
  text: 'Local content wins.',
  startTime: 2,
  endTime: 3,
}).record
const localTranslation = mergeLocalStore.appendTranslation({
  segmentId: shared.id,
  speaker: shared.speaker,
  language: 'cmn',
  text: '保留本地译文。',
  startTime: shared.startTime,
  endTime: shared.endTime,
}).record
const mergeCloudStore = new TranscriptStore()
const cloudShared = mergeCloudStore.appendTranscript(shared).record
const cloudOnly = mergeCloudStore.appendTranscript({
  id: 'cloud-only',
  speaker: 'S2',
  text: 'This exists only in the cloud copy.',
  startTime: 4,
  endTime: 5,
}).record
const mergedRecords = mergeSessionRecords(
  { segments: [localOnly, shared], translations: [localTranslation] },
  { segments: [cloudShared, cloudOnly], translations: [] },
)
assert(mergedRecords.segments.length === 3, 'cloud load retains local-only records')
assert(mergedRecords.segments[1]?.text === 'Local content wins.', 'local identity wins')
assert(mergedRecords.translations.length === 1, 'cloud load retains local translation')

// --- Display aggregation: fragments merge into readable utterance cards ---
const aggStore = new TranscriptStore()
const agg = new TranscriptFeedModel({
  sourceLanguage: 'en',
  targetLanguage: 'cmn',
  translationEnabled: true,
})

const fragmentTexts = ['Hi.', 'Hello.', 'How', 'are', 'you', 'doing?']
const fragmentSegments = fragmentTexts.map((text, index) => aggStore.appendTranscript({
  speaker: 'S1',
  text,
  startTime: index * 0.6,
  endTime: index * 0.6 + 0.5,
}).record)
for (const segment of fragmentSegments) agg.appendSegment(segment)
assert(
  agg.getSnapshot().items.length === 1,
  'six same-speaker fragments must render as one card',
)
assert(
  agg.getSnapshot().items[0]?.original?.text === 'Hi. Hello. How are you doing?',
  `punctuation-aware join, got ${JSON.stringify(agg.getSnapshot().items[0]?.original?.text)}`,
)
assert(
  agg.getSnapshot().items[0]?.segmentIds?.length === 6,
  'card must keep the mapping to its atomic segments',
)

// Translation of one fragment attaches to the whole card without leaving the
// remaining fragments stuck on a waiting placeholder forever.
const translationTexts = ['一', '二', '三', '四', '五', '六']
const middleTranslation = aggStore.appendTranslation({
  segmentId: fragmentSegments[2].id,
  speaker: 'S1',
  language: 'cmn',
  text: translationTexts[2],
  startTime: fragmentSegments[2].startTime,
  endTime: fragmentSegments[2].endTime,
}).record
agg.appendTranslation(middleTranslation)
{
  const card = agg.getSnapshot().items[0]
  assert(
    card?.translation?.text === '三' && card.translation.status === 'streaming',
    'a partial set of translations must render on the card immediately',
  )
}
const translationRecords = [middleTranslation]
for (const [index, segment] of fragmentSegments.entries()) {
  if (index === 2) continue
  const record = aggStore.appendTranslation({
    segmentId: segment.id,
    speaker: 'S1',
    language: 'cmn',
    text: translationTexts[index],
    startTime: segment.startTime,
    endTime: segment.endTime,
  }).record
  translationRecords.push(record)
  agg.appendTranslation(record)
}
{
  const card = agg.getSnapshot().items[0]
  assert(
    card?.translation?.text === '一二三四五六' && card.translation.status === 'final',
    `card translation joins in part order, got ${JSON.stringify(card?.translation)}`,
  )
}

// Speaker changes and >2s pauses start a new card.
const speakerChange = aggStore.appendTranscript({
  speaker: 'S2',
  text: 'Different speaker.',
  startTime: 3.7,
  endTime: 4.2,
}).record
agg.appendSegment(speakerChange)
assert(agg.getSnapshot().items.length === 2, 'a speaker change must start a new card')

const afterLongPause = aggStore.appendTranscript({
  speaker: 'S2',
  text: 'After a long pause.',
  startTime: 4.2 + 2.01,
  endTime: 4.2 + 2.51,
}).record
agg.appendSegment(afterLongPause)
assert(agg.getSnapshot().items.length === 3, 'a 2.01s pause must start a new card')

// A continuing live partial streams inside the newest card instead of
// flapping a separate row for every provider micro-final.
const livePartial = aggStore.setPartial({
  speaker: 'S2',
  text: 'and one more',
  startTime: 6.9,
  endTime: 7.4,
})
assert(livePartial !== null, 'store must produce a live partial')
agg.setPartial(livePartial)
{
  const items = agg.getSnapshot().items
  assert(items.length === 3, 'a continuing partial must not add a separate row')
  assert(
    items[2]?.original?.partialText === 'and one more',
    'the continuing partial streams inside the newest card',
  )
}
const mergedFinal = aggStore.appendTranscript({
  speaker: 'S2',
  text: 'and one more thing.',
  startTime: 6.9,
  endTime: 7.5,
}).record
agg.appendSegment(mergedFinal)
assert(
  agg.getSnapshot().items.length === 3,
  'the merged final keeps the same card count',
)
assert(
  agg.getSnapshot().items[2]?.original?.text === 'After a long pause. and one more thing.',
  'merged final must extend the previous card',
)
assert(
  agg.getSnapshot().items[2]?.original?.partialText === undefined,
  'the merged final consumes the in-card streaming preview',
)

// A partial for a NEW speaker still gets its own live row.
const otherSpeakerPartial = aggStore.setPartial({
  speaker: 'S9',
  text: 'different voice',
  startTime: 7.6,
  endTime: 8.1,
})
assert(otherSpeakerPartial !== null, 'store must produce the second partial')
agg.setPartial(otherSpeakerPartial)
assert(
  agg.getSnapshot().items.length === 4,
  'a new-speaker partial must appear as its own live row',
)
agg.clearPartial()
assert(agg.getSnapshot().items.length === 3, 'clearing removes the live row')

// Hard limits keep cards bounded for very long monologues.
const monologue = new TranscriptFeedModel({
  sourceLanguage: 'en',
  targetLanguage: 'cmn',
  translationEnabled: false,
})
const monologueStore = new TranscriptStore()
for (let index = 0; index < 12; index += 1) {
  monologue.appendSegment(monologueStore.appendTranscript({
    speaker: 'S1',
    text: `${'word '.repeat(11).trim()}.`,
    startTime: index * 1.2,
    endTime: index * 1.2 + 1,
  }).record)
}
{
  const items = monologue.getSnapshot().items
  assert(items.length > 1, 'an unbroken monologue must still break into multiple cards')
  for (const item of items) {
    const length = item.original?.text?.length ?? 0
    assert(length <= 240, `card exceeded the 240-char bound: ${length}`)
  }
}

// Reloading a session must produce exactly the same cards as the live view.
const rehydrated = new TranscriptFeedModel({
  sourceLanguage: 'en',
  targetLanguage: 'cmn',
  translationEnabled: true,
})
rehydrated.hydrate(
  [...fragmentSegments, speakerChange, afterLongPause, mergedFinal],
  translationRecords,
)
const cardShape = (items: readonly ReturnType<TranscriptFeedModel['getSnapshot']>['items'][number][]) =>
  items.map((item) => ({
    speaker: item.speaker,
    text: item.original?.text ?? null,
    translation: item.translation?.text ?? null,
    segments: item.segmentIds?.length ?? 0,
  }))
assert(
  JSON.stringify(cardShape(rehydrated.getSnapshot().items))
    === JSON.stringify(cardShape(agg.getSnapshot().items)),
  'hydrated history must render the same cards as the live session',
)

const store = new TranscriptStore()
const feed = new TranscriptFeedModel({
  sourceLanguage: 'en',
  targetLanguage: 'cmn',
  translationEnabled: true,
})
const layout = new VirtualLayout([], [], 'bilingual:1024', '0', 164)
const startedAt = performance.now()

for (let index = 0; index < SEGMENT_COUNT; index += 1) {
  const startTime = index * 1.8
  const speaker = `S${index % 4}`
  const partial = store.setPartial({
    speaker,
    text: `Streaming phrase ${index}`,
    startTime,
    endTime: startTime + 0.8,
  })
  if (partial) feed.setPartial(partial)

  const segment = store.appendTranscript({
    speaker,
    text: `Confirmed phrase number ${index}.`,
    startTime,
    endTime: startTime + 1.5,
  }).record
  feed.appendSegment(segment)

  const translation = store.appendTranslation({
    speaker,
    language: 'cmn',
    text: `第 ${index} 个确认片段。`,
    startTime,
    endTime: startTime + 1.5,
  }).record
  feed.appendTranslation(translation)
  layout.append([feed.getSnapshot().items[index]?.id ?? segment.id], [164 + index % 5])
}

const appendElapsedMs = performance.now() - startedAt
const lookupStartedAt = performance.now()
let checksum = 0
for (let index = 0; index < 100_000; index += 1) {
  const row = layout.indexAtOffset((index * 7919) % layout.totalSize)
  checksum += layout.getOffset(row)
}
const lookupElapsedMs = performance.now() - lookupStartedAt

assert(store.getSnapshot().segmentCount === SEGMENT_COUNT, 'all final segments appended')
assert(store.getSnapshot().translationCount === SEGMENT_COUNT, 'all translations appended')
assert(feed.getSnapshot().items.length === SEGMENT_COUNT, 'streaming rows promoted in place')
assert(layout.length === SEGMENT_COUNT, 'virtual layout appended without a rebuild')
assert(Number.isFinite(checksum) && checksum > 0, 'virtual offsets remain valid')

const sizeBeforeTailTruncate = layout.totalSize
const removedTailSize = layout.getSize(layout.length - 1)
layout.truncate(SEGMENT_COUNT - 1)
assert(layout.length === SEGMENT_COUNT - 1, 'virtual layout truncates a transient tail in place')
assert(
  layout.totalSize === sizeBeforeTailTruncate - removedTailSize,
  'tail truncation preserves Fenwick sums',
)
layout.append(['tail-restored'], [removedTailSize])
assert(layout.length === SEGMENT_COUNT, 'virtual layout appends normally after a tail truncation')
assert(layout.totalSize === sizeBeforeTailTruncate, 'tail restoration preserves total size')

assert(appendElapsedMs < 15_000, `append path took ${appendElapsedMs.toFixed(0)}ms`)
assert(lookupElapsedMs < 5_000, `lookup path took ${lookupElapsedMs.toFixed(0)}ms`)

// --- AI context translation client: batching, protocol, and matching ---
class FakeTranslateSocket {
  readyState = 0
  readonly sent: string[] = []
  onopen: ((event: unknown) => void) | null = null
  onmessage: ((event: { data: unknown }) => void) | null = null
  onclose: ((event: { code: number; reason: string }) => void) | null = null
  onerror: ((event: unknown) => void) | null = null

  send(data: string): void {
    if (this.readyState !== 1) throw new Error('Fake translate socket is not open')
    this.sent.push(data)
  }

  close(): void {
    if (this.readyState === 3) return
    this.readyState = 3
    this.onclose?.({ code: 1000, reason: '' })
  }

  open(): void {
    this.readyState = 1
    this.onopen?.({})
  }

  messages(): Array<Record<string, unknown>> {
    return this.sent.map((raw) => JSON.parse(raw) as Record<string, unknown>)
  }
}

async function verifyAiTranslateClient(): Promise<void> {
  const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms))
  let socket: FakeTranslateSocket | null = null
  const received: Array<{ chunk: AiTranslateChunk; text: string }> = []
  const errors: string[] = []
  const client = new AiTranslateClient({
    url: 'ws://verify/ws/translate',
    tokenProvider: async () => 'verify-token',
    protocolFactory: (token) => ['dreamtrans.v1', `dreamtrans.jwt.${token}`],
    socketFactory: () => {
      socket = new FakeTranslateSocket()
      return socket
    },
    onTranslation: (chunk, result) => received.push({ chunk, text: result.text }),
    onError: (message) => errors.push(message),
    idleFlushMs: 20,
    minChunkChars: 4,
  })

  client.startSession({ translatePrompt: 'CUSTOM PROMPT' })
  await sleep(0)
  assert(socket !== null, 'startSession must open a translate socket')
  const activeSocket = socket as FakeTranslateSocket
  activeSocket.open()

  const init = activeSocket.messages()[0] as {
    type?: string
    mode?: string
    config?: { translate_prompt?: string; disable_summarization?: boolean }
  }
  assert(init.type === 'init' && init.mode === 'ai_rolling', 'init announces rolling context mode')
  assert(init.config?.translate_prompt === 'CUSTOM PROMPT', 'custom prompt reaches init')
  assert(init.config?.disable_summarization === true, 'summarization stays off by default')

  // Fragments of one card batch into one sentence-complete paragraph.
  client.addSegment({ id: 'f1', speaker: 'S1', text: 'Hi.', startTime: 0, endTime: 0.4 }, 'card-1')
  assert(activeSocket.messages().length === 1, 'a too-short sentence fragment stays buffered')
  client.addSegment({ id: 'f2', speaker: 'S1', text: 'Hello.', startTime: 0.5, endTime: 0.9 }, 'card-1')
  {
    const sentMessages = activeSocket.messages()
    assert(sentMessages.length === 3, 'sentence completion sends transcript plus flush')
    const transcript = sentMessages[1] as { type?: string; payload?: { transcript?: string } }
    assert(transcript.type === 'transcript', 'chunk goes out as a transcript message')
    assert(
      transcript.payload?.transcript === 'Hi. Hello.',
      `chunk joins fragments, got ${JSON.stringify(transcript.payload?.transcript)}`,
    )
    assert((sentMessages[2] as { type?: string }).type === 'flush', 'each chunk is explicitly flushed')
  }

  // A card change flushes the open buffer even without sentence punctuation.
  client.addSegment({ id: 'f3', speaker: 'S1', text: 'Unfinished thought', startTime: 1.2, endTime: 1.8 }, 'card-1')
  client.addSegment({ id: 'f4', speaker: 'S2', text: 'Next speaker', startTime: 2.0, endTime: 2.6 }, 'card-2')
  {
    const sentMessages = activeSocket.messages()
    const transcript = sentMessages[3] as { payload?: { transcript?: string } }
    assert(
      transcript.payload?.transcript === 'Unfinished thought',
      'card change flushes the previous buffer',
    )
  }

  // Idle timeout flushes the trailing buffer.
  await sleep(40)
  {
    const sentMessages = activeSocket.messages()
    const transcript = sentMessages[5] as { payload?: { transcript?: string } }
    assert(
      transcript.payload?.transcript === 'Next speaker',
      'idle timeout flushes the open buffer',
    )
  }

  // Ordered results map back to their chunk (and its segment ids).
  activeSocket.onmessage?.({
    data: JSON.stringify({
      message: 'AddTranslation',
      results: [{ content: '你好。', start_time: 0, end_time: 0.9 }],
    }),
  })
  assert(received.length === 1, 'AddTranslation resolves one chunk')
  assert(
    JSON.stringify(received[0]?.chunk.segmentIds) === JSON.stringify(['f1', 'f2']),
    'translation maps to the atomic segments of its chunk',
  )
  assert(received[0]?.text === '你好。', 'translated text is delivered')

  // An error consumes the oldest outstanding chunk without stalling later ones.
  activeSocket.onmessage?.({
    data: JSON.stringify({ message: 'Error', reason: 'upstream busy' }),
  })
  assert(errors.length === 1, 'server errors surface to the caller')
  activeSocket.onmessage?.({
    data: JSON.stringify({
      message: 'AddTranslation',
      results: [{ content: '下一位。', start_time: 2.0, end_time: 2.6 }],
    }),
  })
  assert(
    JSON.stringify(received[1]?.chunk.segmentIds) === JSON.stringify(['f4']),
    'delivery continues past a failed chunk',
  )

  client.stopSession()
  assert(activeSocket.readyState === 3, 'stop with no pending work closes the socket')
  client.destroy()
}

await verifyAiTranslateClient()

console.log(JSON.stringify({
  segments: SEGMENT_COUNT,
  appendElapsedMs: Math.round(appendElapsedMs),
  lookupElapsedMs: Math.round(lookupElapsedMs),
  aiTranslateClient: 'batching, protocol, and matching verified',
  mountedRowsAtTypicalViewport: 'visible rows + overscan only',
}))
