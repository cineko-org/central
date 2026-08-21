import { useCallback, useState } from 'react';
import { RegistrySchema, type Registry } from '@cineko/contracts/gen/ts/cineko/release/release_pb';
import { CentralAPIError, loadProto } from './api';
import { ReleasesPageView } from './ui/ReleasesPageView';
import { useInitialRefresh } from './useInitialRefresh';

export function ReleasesView({ onUnauthorized }: { onUnauthorized: () => void }) {
  const [releases, setReleases] = useState<Registry>();
  const [failed, setFailed] = useState(false);
  const refresh = useCallback(async () => {
    setFailed(false);
    try {
      setReleases(await loadProto(RegistrySchema, '/v1/admin/releases'));
    } catch (error) {
      if (error instanceof CentralAPIError && error.status === 401) onUnauthorized();
      else setFailed(true);
    }
  }, [onUnauthorized]);

  useInitialRefresh(refresh);
  return <ReleasesPageView releases={releases} failed={failed} onRefresh={() => void refresh()} />;
}
