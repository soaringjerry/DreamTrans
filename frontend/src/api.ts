const BACKEND_URL = import.meta.env.VITE_BACKEND_URL || 'http://localhost:8080';

export async function getJwt(): Promise<string> {
  try {
    const response = await fetch(`${BACKEND_URL}/api/token/rt`, {
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