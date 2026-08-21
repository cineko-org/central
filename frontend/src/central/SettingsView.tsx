import { useCallback, useState } from 'react';
import { GetConfigurationResponseSchema, type Configuration } from '@cineko/contracts/gen/ts/cineko/admin/admin_pb';
import { RegistrySchema, type Registry } from '@cineko/contracts/gen/ts/cineko/release/release_pb';
import { CentralAPIError, loadProto } from './api';
import { SettingsPageView } from './ui/SettingsPageView';
import { useInitialRefresh } from './useInitialRefresh';

export function SettingsView({ onUnauthorized }: { onUnauthorized: () => void }) {
  const [configuration, setConfiguration] = useState<Configuration>();
  const [releases, setReleases] = useState<Registry>();
  const [failed, setFailed] = useState(false);
  const refresh = useCallback(async () => {
    setFailed(false);
    try {
      const [nextConfiguration, nextReleases] = await Promise.all([
        loadProto(GetConfigurationResponseSchema, '/v1/admin/configuration'),
        loadProto(RegistrySchema, '/v1/admin/releases'),
      ]);
      setConfiguration(nextConfiguration.configuration);
      setReleases(nextReleases);
    } catch (error) {
      if (error instanceof CentralAPIError && error.status === 401) onUnauthorized();
      else setFailed(true);
    }
  }, [onUnauthorized]);

  useInitialRefresh(refresh);
  return <SettingsPageView configuration={configuration} releases={releases} failed={failed} onRefresh={() => void refresh()} />;
}
