export interface AssistantScopeSnapshot {
  readonly generation: number
  readonly key: string
}

function scopeKey(ownerId: string | null, sessionId: string): string {
  return `${ownerId ?? 'anonymous'}\u0000${sessionId}`
}

/**
 * Tracks the owner/session scope that asynchronous AssistantPanel work belongs
 * to. A callback must retain the snapshot from the render that created it and
 * verify the snapshot again after every await before mutating UI state.
 */
export class AssistantScopeGuard {
  private snapshot: AssistantScopeSnapshot

  constructor(ownerId: string | null, sessionId: string) {
    this.snapshot = {
      generation: 0,
      key: scopeKey(ownerId, sessionId),
    }
  }

  update(ownerId: string | null, sessionId: string): AssistantScopeSnapshot {
    const key = scopeKey(ownerId, sessionId)
    if (key !== this.snapshot.key) {
      this.snapshot = {
        generation: this.snapshot.generation + 1,
        key,
      }
    }
    return this.snapshot
  }

  isCurrent(snapshot: AssistantScopeSnapshot): boolean {
    return snapshot.generation === this.snapshot.generation
      && snapshot.key === this.snapshot.key
  }
}
