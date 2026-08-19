import { useCallback, useEffect, useState } from 'react';
import { CentralAPIError, loadJSON } from './api';
import type { AdminReleases, AdminStatus } from './types';
import { StatusPageView } from './ui/StatusPageView';

export function StatusView({ onUnauthorized }: { onUnauthorized: () => void }) {
  const [status, setStatus] = useState<AdminStatus>();
  const [releases, setReleases] = useState<AdminReleases>();
  const [updatedAt, setUpdatedAt] = useState<Date>();
  const [failed, setFailed] = useState(false);
  const refresh = useCallback(async () => {
    setFailed(false);
    try {
      const [nextStatus, nextReleases] = await Promise.all([
        loadJSON<AdminStatus>('/v1/admin/status'),
        loadJSON<AdminReleases>('/v1/admin/releases'),
      ]);
      setStatus(nextStatus);
      setReleases(nextReleases);
      setUpdatedAt(new Date());
    } catch (error) {
      if (error instanceof CentralAPIError && error.status === 401) onUnauthorized();
      else setFailed(true);
    }
  }, [onUnauthorized]);

  useEffect(() => {
    void refresh();
    const timer = window.setInterval(() => void refresh(), 5_000);
    return () => window.clearInterval(timer);
  }, [refresh]);

  return <StatusPageView status={status} releases={releases} updatedAt={updatedAt} failed={failed} onRefresh={() => void refresh()} />;
}
