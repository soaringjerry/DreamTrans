export type RecordWriteMode = 'incremental' | 'truncate-tail' | 'replace-all'

/**
 * A truncate call is a bulk replacement only when the supplied records form
 * the complete dense range [0, truncateAfter). Sparse compatibility writes
 * still use truncateAfter to remove a stale tail without replacing untouched
 * records below it.
 */
export function recordWriteMode(
  records: readonly { sequence: number }[],
  truncateAfter?: number,
): RecordWriteMode {
  if (truncateAfter === undefined) return 'incremental'
  if (records.length !== truncateAfter) return 'truncate-tail'

  const sequences = new Set<number>()
  for (const record of records) {
    if (
      !Number.isSafeInteger(record.sequence)
      || record.sequence < 0
      || record.sequence >= truncateAfter
    ) {
      return 'truncate-tail'
    }
    sequences.add(record.sequence)
  }
  return sequences.size === truncateAfter ? 'replace-all' : 'truncate-tail'
}
