import { useCallback, useEffect, useState } from 'react';
import { CentralAPIError, loadJSON } from './api';
import type { AdminReleases } from './types';
import { ReleasesPageView } from './ui/ReleasesPageView';

export function ReleasesView({ onUnauthorized }: { onUnauthorized: () => void }) {
  const [releases, setReleases] = useState<AdminReleases>();
  const [failed, setFailed] = useState(false);
  const refresh = useCallback(async () => {
    setFailed(false);
    try {
      setReleases(await loadJSON<AdminReleases>('/v1/admin/releases'));
    } catch (error) {
      if (error instanceof CentralAPIError && error.status === 401) onUnauthorized();
      else setFailed(true);
    }
  }, [onUnauthorized]);

  useEffect(() => { void refresh(); }, [refresh]);
  return <ReleasesPageView releases={releases} failed={failed} onRefresh={() => void refresh()} />;
}
