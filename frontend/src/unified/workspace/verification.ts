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
