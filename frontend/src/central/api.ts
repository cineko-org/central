export class CentralAPIError extends Error {
  constructor(
    readonly status: number,
    readonly code = status === 401 ? 'unauthorized' : 'request_failed',
    message = code,
    readonly retryable = false,
    readonly requestId = '',
  ) {
    super(message);
  }
}

export async function request(path: string, init?: RequestInit): Promise<Response> {
  const headers = new Headers(init?.headers);
  if (!headers.has('Accept')) headers.set('Accept', 'application/json');
  if (init?.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json');
  const response = await fetch(path, {
    credentials: 'same-origin',
    ...init,
    headers,
  });
  if (!response.ok) {
    let detail: { code?: string; message?: string; retryable?: boolean; requestId?: string } | undefined;
    try {
      const payload = await response.json() as { error?: typeof detail };
      detail = payload.error;
    } catch {
      // A non-JSON intermediary response still maps to a stable typed failure.
    }
    throw new CentralAPIError(
      response.status,
      detail?.code,
      detail?.message,
      detail?.retryable,
      detail?.requestId,
    );
  }
  return response;
}

export async function loadJSON<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await request(path, init);
  return response.json() as Promise<T>;
}
