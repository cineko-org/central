import { create, fromJson, toJson, type DescMessage, type MessageInitShape, type MessageShape } from '@bufbuild/protobuf';
import { APIErrorResponseSchema } from '@cineko/contracts/gen/ts/cineko/common/common_pb';

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
    let detail;
    try {
      detail = fromJson(APIErrorResponseSchema, await response.json()).error;
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

export async function loadProto<T extends DescMessage>(
  schema: T,
  path: string,
  init?: RequestInit,
): Promise<MessageShape<T>> {
  const response = await request(path, init);
  return fromJson(schema, await response.json());
}

export function protoBody<T extends DescMessage>(schema: T, value: MessageInitShape<T>): string {
  return JSON.stringify(toJson(schema, create(schema, value)));
}
