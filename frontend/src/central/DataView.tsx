import { useCallback, useState } from 'react';
import { CentralAPIError, loadJSON } from './api';
import type { AdminDataSummary, ObservationIntelligence } from './types';
import { DataPageView } from './ui/DataPageView';
import { useInitialRefresh } from './useInitialRefresh';

export function DataView({ onUnauthorized }: { onUnauthorized: () => void }) {
  const [summary, setSummary] = useState<AdminDataSummary>();
  const [intelligence, setIntelligence] = useState<ObservationIntelligence>();
  const [failed, setFailed] = useState(false);
  const refresh = useCallback(async () => {
    setFailed(false);
    try {
      const [nextSummary, nextIntelligence] = await Promise.all([
        loadJSON<AdminDataSummary>('/v1/admin/data'),
        loadJSON<ObservationIntelligence>('/v1/admin/observation-intelligence'),
      ]);
      setSummary(nextSummary);
      setIntelligence(nextIntelligence);
    } catch (error) {
      if (error instanceof CentralAPIError && error.status === 401) onUnauthorized();
      else setFailed(true);
    }
  }, [onUnauthorized]);

  useInitialRefresh(refresh);
  return <DataPageView summary={summary} intelligence={intelligence} failed={failed} onRefresh={() => void refresh()} />;
}
