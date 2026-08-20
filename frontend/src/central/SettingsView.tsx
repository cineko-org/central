import { useCallback, useState } from 'react';
import { CentralAPIError, loadJSON } from './api';
import type { AdminConfiguration, AdminReleases } from './types';
import { SettingsPageView } from './ui/SettingsPageView';
import { useInitialRefresh } from './useInitialRefresh';

export function SettingsView({ onUnauthorized }: { onUnauthorized: () => void }) {
  const [configuration, setConfiguration] = useState<AdminConfiguration>();
  const [releases, setReleases] = useState<AdminReleases>();
  const [failed, setFailed] = useState(false);
  const refresh = useCallback(async () => {
    setFailed(false);
    try {
      const [nextConfiguration, nextReleases] = await Promise.all([
        loadJSON<AdminConfiguration>('/v1/admin/configuration'),
        loadJSON<AdminReleases>('/v1/admin/releases'),
      ]);
      setConfiguration(nextConfiguration);
      setReleases(nextReleases);
    } catch (error) {
      if (error instanceof CentralAPIError && error.status === 401) onUnauthorized();
      else setFailed(true);
    }
  }, [onUnauthorized]);

  useInitialRefresh(refresh);
  return <SettingsPageView configuration={configuration} releases={releases} failed={failed} onRefresh={() => void refresh()} />;
}
