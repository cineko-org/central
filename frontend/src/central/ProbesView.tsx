import { useCallback, useState } from 'react';
import { ListProbesResponseSchema, type Probe } from '@cineko/contracts/gen/ts/cineko/admin/admin_pb';
import { CentralAPIError, loadProto, request } from './api';
import { ProbesPageView } from './ui/ProbesPageView';
import { useInitialRefresh } from './useInitialRefresh';

export function ProbesView({ onUnauthorized }: { onUnauthorized: () => void }) {
  const [probes, setProbes] = useState<Probe[]>();
  const [removing, setRemoving] = useState<Probe>();
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<'load' | 'remove'>();
  const refresh = useCallback(async () => {
    setFailure(undefined);
    try {
      setProbes((await loadProto(ListProbesResponseSchema, '/v1/admin/probes')).probes);
    } catch (error) {
      if (error instanceof CentralAPIError && error.status === 401) onUnauthorized();
      else setFailure('load');
    }
  }, [onUnauthorized]);

  const remove = useCallback(async () => {
    if (!removing || busy) return;
    setBusy(true);
    setFailure(undefined);
    try {
      await request(`/v1/admin/probes/${encodeURIComponent(removing.id)}`, { method: 'DELETE' });
      setRemoving(undefined);
      await refresh();
    } catch (error) {
      if (error instanceof CentralAPIError && error.status === 401) onUnauthorized();
      else setFailure('remove');
    } finally {
      setBusy(false);
    }
  }, [busy, onUnauthorized, refresh, removing]);

  useInitialRefresh(refresh);
  return <ProbesPageView probes={probes} removing={removing} busy={busy} failure={failure} onRefresh={() => void refresh()} onRemoveRequest={setRemoving} onRemoveCancel={() => setRemoving(undefined)} onRemove={() => void remove()} />;
}
