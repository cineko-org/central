import { useCallback, useEffect, useState } from 'react';
import { GetStatusResponseSchema, type Status } from '@cineko/contracts/gen/ts/cineko/admin/admin_pb';
import { RegistrySchema, type Registry } from '@cineko/contracts/gen/ts/cineko/release/release_pb';
import { CentralAPIError, loadProto } from './api';
import { StatusPageView } from './ui/StatusPageView';

export function StatusView({ onUnauthorized }: { onUnauthorized: () => void }) {
  const [status, setStatus] = useState<Status>();
  const [releases, setReleases] = useState<Registry>();
  const [updatedAt, setUpdatedAt] = useState<Date>();
  const [failed, setFailed] = useState(false);
  const refresh = useCallback(async () => {
    setFailed(false);
    try {
      const [nextStatus, nextReleases] = await Promise.all([
        loadProto(GetStatusResponseSchema, '/v1/admin/status'),
        loadProto(RegistrySchema, '/v1/admin/releases'),
      ]);
      setStatus(nextStatus.status);
      setReleases(nextReleases);
      setUpdatedAt(new Date());
    } catch (error) {
      if (error instanceof CentralAPIError && error.status === 401) onUnauthorized();
      else setFailed(true);
    }
  }, [onUnauthorized]);

  useEffect(() => {
    queueMicrotask(() => void refresh());
    const timer = window.setInterval(() => void refresh(), 5_000);
    return () => window.clearInterval(timer);
  }, [refresh]);

  return <StatusPageView status={status} releases={releases} updatedAt={updatedAt} failed={failed} onRefresh={() => void refresh()} />;
}
