import { afterEach, describe, expect, it, vi } from 'vitest';
import { CentralAPIError, loadJSON, request } from '../src/central/api';

describe('Central API client', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('adds JSON request defaults and returns successful responses', async () => {
    const response = new Response(JSON.stringify({ ready: true }), { status: 200 });
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(response);
    vi.stubGlobal('fetch', fetchMock);

    await expect(loadJSON<{ ready: boolean }>('/status', { method: 'POST', body: '{}' }))
      .resolves.toEqual({ ready: true });
    expect(fetchMock).toHaveBeenCalledWith('/status', expect.objectContaining({
      credentials: 'same-origin',
      headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
      method: 'POST',
      body: '{}',
    }));
  });

  it('keeps bodyless requests bodyless', async () => {
    vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockResolvedValue(new Response(null, { status: 204 })));
    await expect(request('/status')).resolves.toBeInstanceOf(Response);
  });

  it.each([
    [401, 'unauthorized'],
    [500, 'request_failed'],
  ])('maps status %d to a typed error', async (status, message) => {
    vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockResolvedValue(new Response(null, { status })));
    const error = await request('/status').catch((value: unknown) => value);
    expect(error).toBeInstanceOf(CentralAPIError);
    expect(error).toMatchObject({ status, message });
  });
});
