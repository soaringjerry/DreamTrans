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

export async function askRag(sessionId: string, query: string, topK: number = 5): Promise<{ answer: string }> {
  const base = isProduction ? '' : BACKEND_URL
  const res = await fetch(`${base}/api/rag/ask`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ session_id: sessionId, query, top_k: topK }),
  })
  if (!res.ok) throw new Error(await res.text())
  return await res.json()
}
