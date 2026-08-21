import type { Timestamp } from '@bufbuild/protobuf/wkt';

/** Converts a protobuf timestamp into a Date for presentation. */
export function timestampDate(value?: Timestamp): Date | undefined {
  if (!value) return undefined;
  return new Date(Number(value.seconds) * 1_000 + value.nanos / 1_000_000);
}

/** Formats protobuf integer counters without losing precision. */
export function integerText(value: bigint | number): string {
  return value.toLocaleString('ko-KR');
}
