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

// A live streaming row is consumed when its final merges into a prior card.
const livePartial = aggStore.setPartial({
  speaker: 'S2',
  text: 'and one more',
  startTime: 6.9,
  endTime: 7.4,
})
assert(livePartial !== null, 'store must produce a live partial')
agg.setPartial(livePartial)
assert(agg.getSnapshot().items.length === 4, 'streaming partial adds a live row')
const mergedFinal = aggStore.appendTranscript({
  speaker: 'S2',
  text: 'and one more thing.',
  startTime: 6.9,
  endTime: 7.5,
}).record
agg.appendSegment(mergedFinal)
assert(
  agg.getSnapshot().items.length === 3,
  'the live row must collapse when its final merges into the previous card',
)
assert(
  agg.getSnapshot().items[2]?.original?.text === 'After a long pause. and one more thing.',
  'merged final must extend the previous card',
)

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

console.log(JSON.stringify({
  segments: SEGMENT_COUNT,
  appendElapsedMs: Math.round(appendElapsedMs),
  lookupElapsedMs: Math.round(lookupElapsedMs),
  mountedRowsAtTypicalViewport: 'visible rows + overscan only',
}))
