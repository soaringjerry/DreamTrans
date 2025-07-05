// src/db.ts
import { openDB, DBSchema } from 'idb';

// Import the TranscriptLine interface type
interface TranscriptLine {
  id: number;
  speaker: string;
  confirmedSegments: string[];
  partialText: string;
  lastSegmentEndTime: number;
}

interface SessionData {
  id: string;
  audioBlob: Blob | null;
  lines: TranscriptLine[];
  timestamp: number;
}

interface DreamTransDB extends DBSchema {
  sessions: {
    key: string;
    value: SessionData;
  };
}

const dbPromise = openDB<DreamTransDB>('dreamtrans-db', 1, {
  upgrade(db) {
    db.createObjectStore('sessions');
  },
});

export async function saveSession(id: string, data: Omit<SessionData, 'id' | 'timestamp'>) {
  try {
    const db = await dbPromise;
    const sessionData: SessionData = {
      id,
      ...data,
      timestamp: Date.now(),
    };
    await db.put('sessions', sessionData, id);
    return true;
  } catch (error) {
    console.error('Failed to save session:', error);
    return false;
  }
}

export async function loadSession(id: string): Promise<SessionData | undefined> {
  try {
    const db = await dbPromise;
    return await db.get('sessions', id);
  } catch (error) {
    console.error('Failed to load session:', error);
    return undefined;
  }
}

export async function clearSession(id: string) {
  try {
    const db = await dbPromise;
    await db.delete('sessions', id);
    return true;
  } catch (error) {
    console.error('Failed to clear session:', error);
    return false;
  }
}