/**
 * A tiny dynamic-size virtual layout backed by a Fenwick tree. Measurements
 * update in O(log n), as do offset lookup and visible-row lookup.
 */
export class VirtualLayout {
  readonly modeScope: string
  readonly layoutRevision: string
  readonly estimatedSize: number

  private readonly mutableIds: string[]
  private readonly sizes: number[]
  private readonly tree: number[]
  private readonly indexById = new Map<string, number>()

  constructor(
    ids: readonly string[],
    sizes: readonly number[],
    modeScope: string,
    layoutRevision: string,
    estimatedSize: number,
  ) {
    this.modeScope = modeScope
    this.layoutRevision = layoutRevision
    this.estimatedSize = estimatedSize
    this.mutableIds = Array.from(ids)
    this.sizes = Array.from(sizes)
    this.tree = new Array(ids.length + 1).fill(0)

    for (let index = 0; index < ids.length; index += 1) {
      this.indexById.set(ids[index], index)
      const treeIndex = index + 1
      this.tree[treeIndex] = (this.tree[treeIndex] ?? 0) + (this.sizes[index] ?? estimatedSize)
      const parent = treeIndex + (treeIndex & -treeIndex)
      if (parent < this.tree.length) {
        this.tree[parent] = (this.tree[parent] ?? 0) + (this.tree[treeIndex] ?? 0)
      }
    }
  }

  get ids(): readonly string[] {
    return this.mutableIds
  }

  get length(): number {
    return this.mutableIds.length
  }

  get totalSize(): number {
    return this.prefixSum(this.length)
  }

  getIndex(id: string): number | undefined {
    return this.indexById.get(id)
  }

  append(ids: readonly string[], sizes: readonly number[]): void {
    for (let offset = 0; offset < ids.length; offset += 1) {
      const id = ids[offset]
      const size = Math.max(1, sizes[offset] ?? this.estimatedSize)
      const index = this.mutableIds.length
      const treeIndex = index + 1
      const rangeStart = treeIndex - (treeIndex & -treeIndex)
      const previousRangeSize = this.prefixSum(treeIndex - 1) - this.prefixSum(rangeStart)

      this.mutableIds.push(id)
      this.sizes.push(size)
      this.indexById.set(id, index)
      this.tree.push(previousRangeSize + size)
    }
  }

  /**
   * Removes only rows at the end of the layout. A tail pop does not require
   * updating the surviving Fenwick nodes because a removed value contributes
   * only to its own node and later nodes, which are removed with it.
   */
  truncate(nextLength: number): void {
    const safeLength = Number.isFinite(nextLength)
      ? Math.max(0, Math.min(this.length, Math.floor(nextLength)))
      : this.length

    while (this.mutableIds.length > safeLength) {
      const id = this.mutableIds.pop()
      if (id !== undefined) this.indexById.delete(id)
      this.sizes.pop()
      this.tree.pop()
    }
  }

  getSize(index: number): number {
    return this.sizes[index] ?? this.estimatedSize
  }

  getOffset(index: number): number {
    if (index <= 0) return 0
    return this.prefixSum(Math.min(index, this.length))
  }

  setSize(index: number, nextSize: number): number {
    if (index < 0 || index >= this.length || !Number.isFinite(nextSize)) return 0
    const safeSize = Math.max(1, nextSize)
    const previousSize = this.getSize(index)
    const delta = safeSize - previousSize
    if (Math.abs(delta) < 0.5) return 0

    this.sizes[index] = safeSize
    this.add(index, delta)
    return delta
  }

  /**
   * Returns the row containing the offset. If the offset is beyond the content,
   * the final row is returned.
   */
  indexAtOffset(offset: number): number {
    if (this.length === 0) return -1
    const target = Math.max(0, offset)
    let index = 0
    let accumulated = 0
    let step = 1

    while (step * 2 <= this.length) step *= 2

    for (; step > 0; step = Math.floor(step / 2)) {
      const next = index + step
      if (next <= this.length && accumulated + (this.tree[next] ?? 0) <= target) {
        index = next
        accumulated += this.tree[next] ?? 0
      }
    }

    return Math.min(index, this.length - 1)
  }

  private add(index: number, delta: number): void {
    for (let cursor = index + 1; cursor < this.tree.length; cursor += cursor & -cursor) {
      this.tree[cursor] = (this.tree[cursor] ?? 0) + delta
    }
  }

  /** Sum of [0, endExclusive). */
  private prefixSum(endExclusive: number): number {
    let sum = 0
    for (let cursor = endExclusive; cursor > 0; cursor -= cursor & -cursor) {
      sum += this.tree[cursor] ?? 0
    }
    return sum
  }
}
