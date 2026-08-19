export class CentralAPIError extends Error {
  constructor(readonly status: number) {
    super(status === 401 ? 'unauthorized' : 'request_failed');
  }
}

export async function request(path: string, init?: RequestInit): Promise<Response> {
  const response = await fetch(path, {
    credentials: 'same-origin',
    headers: { Accept: 'application/json', ...(init?.body ? { 'Content-Type': 'application/json' } : {}) },
    ...init,
  });
  if (!response.ok) throw new CentralAPIError(response.status);
  return response;
}

export async function loadJSON<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await request(path, init);
  return response.json() as Promise<T>;
}
