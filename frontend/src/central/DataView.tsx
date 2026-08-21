import { useCallback, useState } from 'react';
import {
  GetDataSummaryResponseSchema,
  GetObservationIntelligenceResponseSchema,
  type DataSummary,
  type ObservationIntelligence,
} from '@cineko/contracts/gen/ts/cineko/admin/admin_pb';
import { CentralAPIError, loadProto } from './api';
import { DataPageView } from './ui/DataPageView';
import { useInitialRefresh } from './useInitialRefresh';

export function DataView({ onUnauthorized }: { onUnauthorized: () => void }) {
  const [summary, setSummary] = useState<DataSummary>();
  const [intelligence, setIntelligence] = useState<ObservationIntelligence>();
  const [failed, setFailed] = useState(false);
  const refresh = useCallback(async () => {
    setFailed(false);
    try {
      const [nextSummary, nextIntelligence] = await Promise.all([
        loadProto(GetDataSummaryResponseSchema, '/v1/admin/data'),
        loadProto(GetObservationIntelligenceResponseSchema, '/v1/admin/observation-intelligence'),
      ]);
      setSummary(nextSummary.summary);
      setIntelligence(nextIntelligence.intelligence);
    } catch (error) {
      if (error instanceof CentralAPIError && error.status === 401) onUnauthorized();
      else setFailed(true);
    }
  }, [onUnauthorized]);

  useInitialRefresh(refresh);
  return <DataPageView summary={summary} intelligence={intelligence} failed={failed} onRefresh={() => void refresh()} />;
}
