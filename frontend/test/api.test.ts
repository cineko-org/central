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
      method: 'POST',
      body: '{}',
    }));
    const headers = new Headers(fetchMock.mock.calls[0]?.[1]?.headers);
    expect(headers.get('Accept')).toBe('application/json');
    expect(headers.get('Content-Type')).toBe('application/json');
  });

  it('preserves caller precondition headers while adding JSON defaults', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    await request('/policies', {
      method: 'POST', headers: { Accept: 'application/problem+json', 'If-None-Match': '*' }, body: '{}',
    });
    const headers = new Headers(fetchMock.mock.calls[0]?.[1]?.headers);
    expect(headers.get('If-None-Match')).toBe('*');
    expect(headers.get('Accept')).toBe('application/problem+json');
    expect(headers.get('Content-Type')).toBe('application/json');
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

  it('retains the safe API error contract and request id', async () => {
    vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      error: { code: 'revision_conflict', message: 'resource revision does not match', retryable: false, requestId: 'req_1' },
    }), { status: 409, headers: { 'Content-Type': 'application/json' } })));
    const error = await request('/policies').catch((value: unknown) => value);
    expect(error).toMatchObject({
      status: 409, code: 'revision_conflict', message: 'resource revision does not match',
      retryable: false, requestId: 'req_1',
    });
  });
});
