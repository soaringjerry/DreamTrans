// src/db.ts
import { openDB } from 'idb';

// Import the TranscriptLine interface type
interface ConfirmedSegment {
  text: string;
  startTime: number;
  endTime: number;
}

interface TranscriptLine {
  id: number;
  speaker: string;
  confirmedSegments: ConfirmedSegment[];
  partialText: string;
  lastSegmentEndTime: number;
}

interface TranslationLine {
  id: string;
  speaker: string;
  startTime: number;
  content: string;
  isPartial: boolean;
}

interface SessionData {
  id: string;
  audioBlob: Blob | null;
  lines: TranscriptLine[];
  translations: TranslationLine[];
  timestamp: number;
}

// 直接使用泛型，而不是通过 DBSchema
const dbPromise = openDB('dreamtrans-db', 1, {
  upgrade(db) {
    // 检查对象存储是否已存在
    if (!db.objectStoreNames.contains('sessions')) {
      db.createObjectStore('sessions');
    }
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