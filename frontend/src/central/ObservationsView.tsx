import { create } from '@bufbuild/protobuf';
import { useCallback, useState } from 'react';
import {
  CreateObservationPolicyResponseSchema,
  GetCatalogRefreshStatusResponseSchema,
  ListObservationPoliciesResponseSchema,
  ObservationPolicyInputSchema,
  RequestCatalogRefreshResponseSchema,
  UpdateObservationPolicyResponseSchema,
  type CatalogRefreshStatus,
  type ObservationPolicy,
  type ObservationPolicyInput,
} from '@cineko/contracts/gen/ts/cineko/admin/admin_pb';
import { CatalogSnapshotSchema, type CatalogSnapshot } from '@cineko/contracts/gen/ts/cineko/catalog/catalog_pb';
import { CentralAPIError, loadProto, protoBody, request } from './api';
import { ObservationsPageView } from './ui/ObservationsPageView';
import { useInitialRefresh } from './useInitialRefresh';

const emptyPolicy = (): ObservationPolicyInput => create(ObservationPolicyInputSchema, {
  theaterId: '', enabled: true, horizonDays: 14,
});

export function ObservationsView({ onUnauthorized }: { onUnauthorized: () => void }) {
  const [policies, setPolicies] = useState<ObservationPolicy[]>();
  const [catalog, setCatalog] = useState<CatalogSnapshot>();
  const [catalogRefresh, setCatalogRefresh] = useState<CatalogRefreshStatus>();
  const [editing, setEditing] = useState<ObservationPolicy>();
  const [draft, setDraft] = useState<ObservationPolicyInput>(emptyPolicy());
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
        loadProto(ListObservationPoliciesResponseSchema, '/v1/admin/observation-policies'),
        loadProto(CatalogSnapshotSchema, '/v1/admin/catalog'),
        loadProto(GetCatalogRefreshStatusResponseSchema, '/v1/admin/catalog-refresh'),
      ]);
      setPolicies(policyResponse.policies);
      setCatalog(catalogResponse);
      setCatalogRefresh(catalogRefreshResponse.status);
    } catch (error) {
      handleError(error);
    }
  }, [handleError]);

  const requestCatalogRefresh = useCallback(async () => {
    setRequestingCatalog(true);
    setFailed(false);
    try {
      await loadProto(RequestCatalogRefreshResponseSchema, '/v1/admin/catalog-refresh', { method: 'POST' });
      await refresh();
    } catch (error) {
      handleError(error);
    } finally {
      setRequestingCatalog(false);
    }
  }, [handleError, refresh]);

  useInitialRefresh(refresh);

  const save = useCallback(async () => {
    setSaving(true);
    setFailed(false);
    try {
      const path = editing ? `/v1/admin/observation-policies/${encodeURIComponent(editing.id)}` : '/v1/admin/observation-policies';
      const init: RequestInit = {
        method: editing ? 'PUT' : 'POST',
        headers: editing ? new Headers({ 'If-Match': `"${editing.revision}"` }) : new Headers({ 'If-None-Match': '*' }),
        body: protoBody(ObservationPolicyInputSchema, draft),
      };
      if (editing) await loadProto(UpdateObservationPolicyResponseSchema, path, init);
      else await loadProto(CreateObservationPolicyResponseSchema, path, init);
      setEditing(undefined);
      setDraft(emptyPolicy());
      await refresh();
    } catch (error) {
      handleError(error);
    } finally {
      setSaving(false);
    }
  }, [draft, editing, handleError, refresh]);

  const remove = useCallback(async (policy: ObservationPolicy) => {
    setSaving(true);
    setFailed(false);
    try {
      await request(`/v1/admin/observation-policies/${encodeURIComponent(policy.id)}`, {
        method: 'DELETE', headers: { 'If-Match': `"${policy.revision}"` },
      });
      setEditing(undefined);
      setDraft(emptyPolicy());
      await refresh();
    } catch (error) {
      handleError(error);
    } finally {
      setSaving(false);
    }
  }, [handleError, refresh]);

  return <ObservationsPageView
    policies={policies} theaters={catalog?.theaters}
    catalogRefresh={catalogRefresh} draft={draft} editing={editing}
    failed={failed} saving={saving} requestingCatalog={requestingCatalog}
    onDraftChange={setDraft} onSave={() => { if (!saving) void save(); }} onRefresh={() => void refresh()}
    onRequestCatalogRefresh={() => void requestCatalogRefresh()}
    onEdit={(policy) => { setEditing(policy); setDraft(create(ObservationPolicyInputSchema, {
      theaterId: policy.theater?.id ?? '', enabled: policy.input?.enabled ?? true, horizonDays: policy.input?.horizonDays ?? 14,
    })); }}
    onCancel={() => { setEditing(undefined); setDraft(emptyPolicy()); }}
    onDelete={(policy) => { if (!saving) void remove(policy); }}
  />;
}
