import type { AIArtifact } from '../../api'
import { messages } from '../../i18n'

const DATABASE_NAME = 'dreamtrans-ai'
const DATABASE_VERSION = 1
const STORE_NAME = 'artifacts'

interface StoredArtifact extends AIArtifact {
  session_id: string
}

function openDatabase(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DATABASE_NAME, DATABASE_VERSION)
    request.onupgradeneeded = () => {
      const database = request.result
      if (!database.objectStoreNames.contains(STORE_NAME)) {
        const store = database.createObjectStore(STORE_NAME, { keyPath: 'id' })
        store.createIndex('session_id', 'session_id')
      }
    }
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(
      request.error ?? new Error(messages().assistant.localStore.openFailed),
    )
  })
}

export async function saveLocalArtifact(
  sessionId: string,
  artifact: AIArtifact,
): Promise<void> {
  const database = await openDatabase()
  await new Promise<void>((resolve, reject) => {
    const transaction = database.transaction(STORE_NAME, 'readwrite')
    transaction.objectStore(STORE_NAME).put({ ...artifact, session_id: sessionId })
    transaction.oncomplete = () => resolve()
    transaction.onerror = () => reject(
      transaction.error ?? new Error(messages().assistant.localStore.saveFailed),
    )
  })
  database.close()
}

export async function listLocalArtifacts(sessionId: string): Promise<AIArtifact[]> {
  const database = await openDatabase()
  const result = await new Promise<StoredArtifact[]>((resolve, reject) => {
    const transaction = database.transaction(STORE_NAME, 'readonly')
    const request = transaction.objectStore(STORE_NAME).index('session_id').getAll(sessionId)
    request.onsuccess = () => resolve(request.result as StoredArtifact[])
    request.onerror = () => reject(
      request.error ?? new Error(messages().assistant.localStore.listFailed),
    )
  })
  database.close()
  return result
    .sort((left, right) => right.created_at.localeCompare(left.created_at))
    .map((storedArtifact) => {
      const artifact = { ...storedArtifact } as Partial<StoredArtifact>
      delete artifact.session_id
      return artifact as AIArtifact
    })
}

export async function deleteLocalArtifact(
  sessionId: string,
  artifactId: string,
): Promise<void> {
  const database = await openDatabase()
  await new Promise<void>((resolve, reject) => {
    const transaction = database.transaction(STORE_NAME, 'readwrite')
    const store = transaction.objectStore(STORE_NAME)
    const request = store.get(artifactId)
    request.onsuccess = () => {
      const artifact = request.result as StoredArtifact | undefined
      if (artifact?.session_id === sessionId) store.delete(artifactId)
    }
    request.onerror = () => {
      transaction.abort()
      reject(request.error ?? new Error(messages().assistant.localStore.readFailed))
    }
    transaction.oncomplete = () => resolve()
    transaction.onerror = () => reject(
      transaction.error ?? new Error(messages().assistant.localStore.deleteFailed),
    )
  })
  database.close()
}
