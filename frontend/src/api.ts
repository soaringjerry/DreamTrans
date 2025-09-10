// In production, use relative URLs to work with the same origin
const BACKEND_URL = import.meta.env.VITE_BACKEND_URL || 'http://localhost:8080';
const isProduction = BACKEND_URL === '/';

export async function getJwt(): Promise<string> {
  try {
    const url = isProduction ? '/api/token/rt' : `${BACKEND_URL}/api/token/rt`;
    const response = await fetch(url, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
    });

    if (!response.ok) {
      throw new Error(`Failed to get JWT: ${response.statusText}`);
    }

    const data = await response.json();
    return data.token;
  } catch (error) {
    console.error('Error fetching JWT:', error);
    throw error;
  }
}

export type RagConfig = {
  api_key?: string
  api_base?: string
  model?: string
  prompt?: string
}

export type RagAskResponse = {
  answer: string
  usage?: { prompt_tokens: number; completion_tokens: number; total_tokens: number; model?: string }
  latency_ms?: number
}

export async function askRag(sessionId: string, query: string, topK: number = 5, config?: RagConfig): Promise<RagAskResponse> {
  const base = isProduction ? '' : BACKEND_URL
  const res = await fetch(`${base}/api/rag/ask`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ session_id: sessionId, query, top_k: topK, config }),
  })
  if (!res.ok) throw new Error(await res.text())
  return await res.json()
}

// Reset server-side API metrics (overall counters/logs). Useful to start a fresh session view.
export async function resetMetrics(): Promise<void> {
  const base = isProduction ? '' : BACKEND_URL
  try {
    await fetch(`${base}/api/metrics/reset`, { method: 'POST' })
  } catch {
    // ignore best-effort errors
  }
}

// Dictionary APIs
export type DictEntry = { word: string; phonetic?: string; pos?: string; definition: string; extra?: string }

export async function lookupDict(word: string): Promise<DictEntry | null> {
  const base = isProduction ? '' : BACKEND_URL
  const res = await fetch(`${base}/api/dict?word=${encodeURIComponent(word)}`)
  if (!res.ok) throw new Error(await res.text())
  const j = await res.json() as { found?: boolean; entry?: DictEntry }
  return j.found && j.entry ? j.entry : null
}

export async function suggestDictPrefix(prefix: string, limit: number = 10): Promise<DictEntry[]> {
  const base = isProduction ? '' : BACKEND_URL
  const res = await fetch(`${base}/api/dict/prefix?q=${encodeURIComponent(prefix)}&limit=${limit}`)
  if (!res.ok) throw new Error(await res.text())
  const j = await res.json() as { items?: DictEntry[] }
  return j.items || []
}
