import { useCallback, useEffect, useState } from 'react';
import { CentralAPIError, loadJSON, request } from './api';
import type { AdminObservationPolicy, CatalogIndex, CatalogRefreshStatus, ObservationPolicyInput } from './types';
import { ObservationsPageView } from './ui/ObservationsPageView';

const emptyPolicy: ObservationPolicyInput = {
  theaterId: '', enabled: true, horizonDays: 14, priority: 50,
  baselineMinSeconds: 900, baselineMaxSeconds: 1800,
  demandMinSeconds: 120, demandMaxSeconds: 300,
  burstMinSeconds: 30, burstMaxSeconds: 90, burstDurationSeconds: 3600,
  locale: 'ko-KR', timeZone: 'Asia/Seoul', egressPolicyId: 'scan_default',
};

export function ObservationsView({ onUnauthorized }: { onUnauthorized: () => void }) {
  const [policies, setPolicies] = useState<AdminObservationPolicy[]>();
  const [catalog, setCatalog] = useState<CatalogIndex>();
  const [catalogRefresh, setCatalogRefresh] = useState<CatalogRefreshStatus>();
  const [editing, setEditing] = useState<AdminObservationPolicy>();
  const [draft, setDraft] = useState<ObservationPolicyInput>(emptyPolicy);
  const [failed, setFailed] = useState(false);
  const [saving, setSaving] = useState(false);
  const [requestingCatalog, setRequestingCatalog] = useState(false);
  const handleError = useCallback((error: unknown) => {
    if (error instanceof CentralAPIError && error.status === 401) onUnauthorized();
    else setFailed(true);
  }, [onUnauthorized]);
  const refresh = useCallback(async () => {
    setFailed(false);
    try {
      const [policyResponse, catalogResponse, catalogRefreshResponse] = await Promise.all([
        loadJSON<{ data: AdminObservationPolicy[] }>('/v1/admin/observation-policies'),
        loadJSON<CatalogIndex>('/v1/admin/catalog'),
        loadJSON<CatalogRefreshStatus>('/v1/admin/catalog-refresh'),
      ]);
      setPolicies(policyResponse.data);
      setCatalog(catalogResponse);
      setCatalogRefresh(catalogRefreshResponse);
    } catch (error) {
      handleError(error);
    }
  }, [handleError]);

  const requestCatalogRefresh = useCallback(async () => {
    setRequestingCatalog(true);
    setFailed(false);
    try {
      await loadJSON<CatalogRefreshStatus>('/v1/admin/catalog-refresh', { method: 'POST' });
      await refresh();
    } catch (error) {
      handleError(error);
    } finally {
      setRequestingCatalog(false);
    }
  }, [handleError, refresh]);

  useEffect(() => { void refresh(); }, [refresh]);

  const save = useCallback(async () => {
    setSaving(true);
    setFailed(false);
    try {
      const path = editing ? `/v1/admin/observation-policies/${encodeURIComponent(editing.id)}` : '/v1/admin/observation-policies';
      await loadJSON<AdminObservationPolicy>(path, {
        method: editing ? 'PUT' : 'POST',
        headers: editing ? { 'If-Match': `"${editing.revision}"` } : { 'If-None-Match': '*' },
        body: JSON.stringify(draft),
      });
      setEditing(undefined);
      setDraft(emptyPolicy);
      await refresh();
    } catch (error) {
      handleError(error);
    } finally {
      setSaving(false);
    }
  }, [draft, editing, handleError, refresh]);

  const remove = useCallback(async (policy: AdminObservationPolicy) => {
    setSaving(true);
    setFailed(false);
    try {
      await request(`/v1/admin/observation-policies/${encodeURIComponent(policy.id)}`, {
        method: 'DELETE', headers: { 'If-Match': `"${policy.revision}"` },
      });
      setEditing(undefined);
      setDraft(emptyPolicy);
      await refresh();
    } catch (error) {
      handleError(error);
    } finally {
      setSaving(false);
    }
  }, [handleError, refresh]);

  return <ObservationsPageView
	policies={policies} theaters={catalog?.theaters} auditoriums={catalog?.auditoriums}
	catalogRefresh={catalogRefresh} draft={draft} editing={editing}
    failed={failed} saving={saving} requestingCatalog={requestingCatalog}
    onDraftChange={setDraft} onSave={() => { if (!saving) void save(); }} onRefresh={() => void refresh()}
    onRequestCatalogRefresh={() => void requestCatalogRefresh()}
    onEdit={(policy) => { setEditing(policy); setDraft({ ...policy, theaterId: policy.theater.id }); }}
    onCancel={() => { setEditing(undefined); setDraft(emptyPolicy); }}
    onDelete={(policy) => { if (!saving) void remove(policy); }}
  />;
}
