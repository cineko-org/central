import { act, type FormEventHandler } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type {
  AdminConfiguration,
  AdminDataSummary,
  CatalogIndex,
  ObservationIntelligence,
  AdminProbe,
  AdminObservationPolicy,
  AdminReleases,
  AdminSession,
  AdminStatus,
  ClientPINIssue,
  ClientPINUser,
} from '../src/central/types';

vi.mock('../src/central/ui/LoginPageView', () => ({
  LoginPageView: (props: {
    userId: string;
    password: string;
    loading: boolean;
    failed: boolean;
    onUserIdChange: (value: string) => void;
    onPasswordChange: (value: string) => void;
    onSubmit: FormEventHandler<HTMLFormElement>;
  }) => (
    <form data-testid="login-view" data-loading={String(props.loading)} data-failed={String(props.failed)} onSubmit={props.onSubmit}>
      <input data-testid="user-id" value={props.userId} onChange={(event) => props.onUserIdChange(event.currentTarget.value)} />
      <input data-testid="password" value={props.password} onChange={(event) => props.onPasswordChange(event.currentTarget.value)} />
      <button type="submit">submit</button>
    </form>
  ),
}));

vi.mock('../src/central/ui/StatusPageView', () => ({
  StatusPageView: (props: { status?: AdminStatus; releases?: AdminReleases; updatedAt?: Date; failed: boolean; onRefresh: () => void }) => (
    <section data-testid="status-view" data-ready={String(props.status?.ready)} data-generation={String(props.releases?.generation)} data-updated={String(Boolean(props.updatedAt))} data-failed={String(props.failed)}>
      <button data-testid="status-refresh" onClick={props.onRefresh}>refresh</button>
    </section>
  ),
}));

vi.mock('../src/central/ui/ReleasesPageView', () => ({
  ReleasesPageView: (props: { releases?: AdminReleases; failed: boolean; onRefresh: () => void }) => (
    <section data-testid="releases-view" data-generation={String(props.releases?.generation)} data-records={String(props.releases ? Object.values(props.releases.components).flat().length : -1)} data-failed={String(props.failed)}>
      <button data-testid="releases-refresh" onClick={props.onRefresh}>refresh</button>
    </section>
  ),
}));

vi.mock('../src/central/ui/SettingsPageView', () => ({
  SettingsPageView: (props: { configuration?: AdminConfiguration; releases?: AdminReleases; failed: boolean; onRefresh: () => void }) => (
    <section data-testid="settings-view" data-listen={props.configuration?.listenAddress} data-generation={String(props.releases?.generation)} data-failed={String(props.failed)}>
      <button data-testid="settings-refresh" onClick={props.onRefresh}>refresh</button>
    </section>
  ),
}));

vi.mock('../src/central/ui/ProbesPageView', () => ({
  ProbesPageView: (props: {
    probes?: AdminProbe[];
    removing?: AdminProbe;
    busy: boolean;
    failure?: 'load' | 'remove';
    onRefresh: () => void;
    onRemoveRequest: (probe: AdminProbe) => void;
    onRemoveCancel: () => void;
    onRemove: () => void;
  }) => (
    <section data-testid="probes-view" data-count={String(props.probes?.length)} data-removing={props.removing?.id} data-busy={String(props.busy)} data-failed={String(Boolean(props.failure))} data-failure={props.failure}>
      <button data-testid="probes-refresh" onClick={props.onRefresh}>refresh</button>
      {props.probes?.map((probe) => <button key={probe.id} data-testid={`probes-remove-request-${probe.id}`} onClick={() => props.onRemoveRequest(probe)}>request remove</button>)}
      <button data-testid="probes-remove-cancel" onClick={props.onRemoveCancel}>cancel remove</button>
      <button data-testid="probes-remove-confirm" onClick={props.onRemove}>confirm remove</button>
    </section>
  ),
}));

vi.mock('../src/central/ui/DataPageView', () => ({
  DataPageView: (props: { summary?: AdminDataSummary; intelligence?: ObservationIntelligence; failed: boolean; onRefresh: () => void }) => (
    <section data-testid="data-view" data-captures={String(props.summary?.scheduleCaptures)} data-snapshots={String(props.intelligence?.snapshotCount)} data-failed={String(props.failed)}>
      <button data-testid="data-refresh" onClick={props.onRefresh}>refresh</button>
    </section>
  ),
}));

vi.mock('../src/central/ui/ObservationsPageView', () => ({
  ObservationsPageView: (props: {
    policies?: AdminObservationPolicy[];
    editing?: AdminObservationPolicy;
    failed: boolean;
    saving: boolean;
    onSave: () => void;
    onRefresh: () => void;
    onRequestCatalogRefresh: () => void;
    onEdit: (policy: AdminObservationPolicy) => void;
    onCancel: () => void;
    onDelete: (policy: AdminObservationPolicy) => void;
  }) => (
    <section data-testid="observations-view" data-count={String(props.policies?.length)} data-editing={props.editing?.id} data-failed={String(props.failed)} data-saving={String(props.saving)}>
      <button data-testid="observations-save" onClick={props.onSave}>save</button>
      <button data-testid="observations-refresh" onClick={props.onRefresh}>refresh</button>
      <button data-testid="observations-catalog-refresh" onClick={props.onRequestCatalogRefresh}>catalog refresh</button>
      <button data-testid="observations-cancel" onClick={props.onCancel}>cancel</button>
      {props.policies?.map((policy) => <span key={policy.id}><button data-testid={`observations-edit-${policy.id}`} onClick={() => props.onEdit(policy)}>edit</button><button data-testid={`observations-delete-${policy.id}`} onClick={() => props.onDelete(policy)}>delete</button></span>)}
    </section>
  ),
}));

vi.mock('../src/central/ui/UsersPageView', () => ({
  UsersPageView: (props: {
    users: ClientPINUser[];
    displayName: string;
    issued?: ClientPINIssue;
    deleting?: ClientPINUser;
    loading: boolean;
    failure: boolean;
    onDisplayNameChange: (value: string) => void;
    onCreate: FormEventHandler<HTMLFormElement>;
    onRotate: (userId: string) => void;
    onDeleteRequest: (user: ClientPINUser) => void;
    onDeleteCancel: () => void;
    onDelete: () => void;
    onDismissIssue: () => void;
  }) => (
    <section data-testid="users-view" data-count={String(props.users.length)} data-name={props.displayName} data-pin={props.issued?.pin} data-deleting={props.deleting?.user.id} data-loading={String(props.loading)} data-failure={String(props.failure)}>
      <input data-testid="display-name" value={props.displayName} onChange={(event) => props.onDisplayNameChange(event.currentTarget.value)} />
      <form data-testid="create-user" onSubmit={props.onCreate}><button type="submit">create</button></form>
      {props.users.map((user) => (
        <span key={user.user.id}>
          <button data-testid={`rotate-${user.user.id}`} onClick={() => props.onRotate(user.user.id)}>rotate</button>
          <button data-testid={`delete-request-${user.user.id}`} onClick={() => props.onDeleteRequest(user)}>request delete</button>
        </span>
      ))}
      <button data-testid="delete-cancel" onClick={props.onDeleteCancel}>cancel</button>
      <button data-testid="delete-confirm" onClick={props.onDelete}>delete</button>
      <button data-testid="dismiss-issue" onClick={props.onDismissIssue}>dismiss</button>
    </section>
  ),
}));

import { DataView } from '../src/central/DataView';
import { LoginView } from '../src/central/LoginView';
import { ObservationsView } from '../src/central/ObservationsView';
import { ProbesView } from '../src/central/ProbesView';
import { ReleasesView } from '../src/central/ReleasesView';
import { SettingsView } from '../src/central/SettingsView';
import { StatusView } from '../src/central/StatusView';
import { UsersView } from '../src/central/UsersView';

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const emptyReleases: AdminReleases = { generation: 0, components: { launcher: [], client: [], browser: [], playwright: [], probe: [] } };
const configuration: AdminConfiguration = {
  listenAddress: ':8080', clientSessionSeconds: 60, clientRefreshSeconds: 60, adminSessionSeconds: 60,
  reconcileIntervalSeconds: 5, probeHeartbeatTtlSeconds: 90, probeOfflineRetentionDays: 30,
  assignmentRetryMinSeconds: 1, assignmentRetryMaxSeconds: 5, reconcileBatchSize: 100,
};
const summary: AdminDataSummary = {
  providers: 1, theaters: 2, auditoriums: 3, movies: 4, showtimes: 5, seatMapVersions: 6,
  scheduleCaptures: 3, showtimeObservations: 2, observationPolicies: 4, activeObservationPolicies: 3,
  queuedAssignments: 1, leasedAssignments: 1, completedAssignments: 1, failedAssignments: 0,
};
const intelligence: ObservationIntelligence = { snapshotCount: 3, showtimeObservations: 2, openingPatterns: [], demandPatterns: [] };
const policy: AdminObservationPolicy = {
  id: 'policy / 1', revision: 2, theaterId: 'theater_0013', enabled: true,
  theater: { id: 'theater_0013', providerId: 'cgv', sourceKey: '서울/용산아이파크몰', region: '서울', name: '용산아이파크몰' }, horizonDays: 14, priority: 50,
  baselineMinSeconds: 900, baselineMaxSeconds: 1800, demandMinSeconds: 120, demandMaxSeconds: 300,
  burstMinSeconds: 30, burstMaxSeconds: 90, burstDurationSeconds: 3600,
  locale: 'ko-KR', timeZone: 'Asia/Seoul', egressPolicyId: 'scan_default', effectiveMode: 'baseline',
  effectivePriority: 50, effectiveMinSeconds: 900, effectiveMaxSeconds: 1800, demandActive: false,
  createdAt: '2026-08-12T00:00:00Z', updatedAt: '2026-08-12T00:00:00Z',
};
const catalog: CatalogIndex = {
  generation: 1,
  providers: [{ id: 'cgv', name: 'CGV' }],
  theaters: [policy.theater],
  movies: [],
  auditoriums: [],
  showtimes: [],
};
const probe: AdminProbe = {
  id: 'probe-1', kind: 'container', networkId: 'direct', runtimeVersion: '1.0.0', browserRevision: '1228',
  platform: 'linux', arch: 'amd64', status: 'online', draining: false, availableSlots: 1, maxConcurrency: 1,
  health: 'healthy', updatedAt: '2026-08-12T00:00:00Z',
};
const offlineProbe: AdminProbe = { ...probe, id: 'probe / offline', status: 'offline', availableSlots: 0 };
const user: ClientPINUser = {
  user: { id: 'user / 1', displayName: 'User', createdAt: '2026-08-12T00:00:00Z', updatedAt: '2026-08-12T00:00:00Z' },
  pinActive: true,
  deviceCount: 1,
};
const issue: ClientPINIssue = { user: user.user, pin: '123456' };

function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
}

async function settle(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

function enter(input: HTMLInputElement, value: string): void {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set;
  setter?.call(input, value);
  input.dispatchEvent(new Event('input', { bubbles: true }));
}

describe('Central page controllers', () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    container = document.createElement('div');
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('maps login fields, request body, loading, success, and failure', async () => {
    let resolveLogin!: (response: Response) => void;
    const pending = new Promise<Response>((resolve) => { resolveLogin = resolve; });
    const session: AdminSession = { userId: 'admin', displayName: 'Admin', expiresAt: 1 };
    const onLogin = vi.fn<(session: AdminSession) => void>();
    const fetchMock = vi.fn<typeof fetch>().mockReturnValueOnce(pending).mockResolvedValueOnce(json({}, 500));
    vi.stubGlobal('fetch', fetchMock);
    await act(async () => root.render(<LoginView onLogin={onLogin} />));

    const userId = container.querySelector<HTMLInputElement>('[data-testid="user-id"]')!;
    const password = container.querySelector<HTMLInputElement>('[data-testid="password"]')!;
    await act(async () => {
      enter(userId, 'admin');
      enter(password, 'secret');
    });
    await act(async () => container.querySelector<HTMLFormElement>('[data-testid="login-view"]')!.requestSubmit());
    expect(container.querySelector<HTMLElement>('[data-testid="login-view"]')?.dataset.loading).toBe('true');
    resolveLogin(json(session));
    await settle();
    expect(onLogin).toHaveBeenCalledWith(session);
    expect(fetchMock).toHaveBeenNthCalledWith(1, '/v1/admin/login', expect.objectContaining({ method: 'POST', body: JSON.stringify({ userId: 'admin', password: 'secret' }) }));

    await act(async () => container.querySelector<HTMLFormElement>('[data-testid="login-view"]')!.requestSubmit());
    await settle();
    expect(container.querySelector<HTMLElement>('[data-testid="login-view"]')?.dataset.failed).toBe('true');
    expect(container.querySelector<HTMLElement>('[data-testid="login-view"]')?.dataset.loading).toBe('false');
  });

  it('loads and periodically refreshes status with the release generation', async () => {
    vi.useFakeTimers();
    const fetchMock = vi.fn<typeof fetch>((input) => {
      const path = String(input);
      return Promise.resolve(path.endsWith('/status') ? json({ ready: true }) : json({ ...emptyReleases, generation: 7 }));
    });
    vi.stubGlobal('fetch', fetchMock);
    await act(async () => root.render(<StatusView onUnauthorized={vi.fn<() => void>()} />));
    await settle();
    const view = container.querySelector<HTMLElement>('[data-testid="status-view"]')!;
    expect(view.dataset).toMatchObject({ ready: 'true', generation: '7', updated: 'true', failed: 'false' });

    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="status-refresh"]')!.click());
    await settle();
    expect(fetchMock).toHaveBeenCalledTimes(4);
    await act(async () => vi.advanceTimersByTime(5_000));
    await settle();
    expect(fetchMock).toHaveBeenCalledTimes(6);
  });

  it.each([
    ['status', (unauthorized: () => void) => <StatusView onUnauthorized={unauthorized} />, 2],
    ['releases', (unauthorized: () => void) => <ReleasesView onUnauthorized={unauthorized} />, 1],
    ['settings', (unauthorized: () => void) => <SettingsView onUnauthorized={unauthorized} />, 2],
    ['probes', (unauthorized: () => void) => <ProbesView onUnauthorized={unauthorized} />, 1],
    ['data', (unauthorized: () => void) => <DataView onUnauthorized={unauthorized} />, 2],
  ] as const)('maps %s authorization and request failures', async (name, render, requests) => {
    const unauthorized = vi.fn<() => void>();
    vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockResolvedValue(json({}, 401)));
    await act(async () => root.render(render(unauthorized)));
    await settle();
    expect(unauthorized).toHaveBeenCalledOnce();
    expect(fetch).toHaveBeenCalledTimes(requests);

    await act(async () => root.unmount());
    root = createRoot(container);
    vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockResolvedValue(json({}, 500)));
    await act(async () => root.render(render(unauthorized)));
    await settle();
    expect(container.querySelector<HTMLElement>(`[data-testid="${name}-view"]`)?.dataset.failed).toBe('true');
    await act(async () => container.querySelector<HTMLButtonElement>(`[data-testid="${name}-refresh"]`)?.click());
    await settle();
  });

  it('maps release, settings, probe, and data responses including empty inventory', async () => {
    const fetchMock = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(json(emptyReleases));
    vi.stubGlobal('fetch', fetchMock);
    await act(async () => root.render(<ReleasesView onUnauthorized={vi.fn<() => void>()} />));
    await settle();
    expect(container.querySelector<HTMLElement>('[data-testid="releases-view"]')?.dataset).toMatchObject({ generation: '0', records: '0', failed: 'false' });

    await act(async () => root.unmount());
    root = createRoot(container);
    fetchMock.mockResolvedValueOnce(json(configuration)).mockResolvedValueOnce(json({ ...emptyReleases, generation: 4 }));
    await act(async () => root.render(<SettingsView onUnauthorized={vi.fn<() => void>()} />));
    await settle();
    expect(container.querySelector<HTMLElement>('[data-testid="settings-view"]')?.dataset).toMatchObject({ listen: ':8080', generation: '4', failed: 'false' });

    await act(async () => root.unmount());
    root = createRoot(container);
    fetchMock.mockResolvedValueOnce(json({ data: [probe] }));
    await act(async () => root.render(<ProbesView onUnauthorized={vi.fn<() => void>()} />));
    await settle();
    expect(container.querySelector<HTMLElement>('[data-testid="probes-view"]')?.dataset.count).toBe('1');

    await act(async () => root.unmount());
    root = createRoot(container);
    fetchMock.mockResolvedValueOnce(json(summary)).mockResolvedValueOnce(json(intelligence));
    await act(async () => root.render(<DataView onUnauthorized={vi.fn<() => void>()} />));
    await settle();
    expect(container.querySelector<HTMLElement>('[data-testid="data-view"]')?.dataset).toMatchObject({ captures: '3', snapshots: '3' });
  });

  it('removes an offline Probe once, supports cancel, and refreshes the inventory', async () => {
    let resolveDelete!: (response: Response) => void;
    const pendingDelete = new Promise<Response>((resolve) => { resolveDelete = resolve; });
    const fetchMock = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(json({ data: [offlineProbe] }))
      .mockReturnValueOnce(pendingDelete)
      .mockResolvedValueOnce(json({ data: [] }));
    vi.stubGlobal('fetch', fetchMock);
    await act(async () => root.render(<ProbesView onUnauthorized={vi.fn<() => void>()} />));
    await settle();

    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="probes-remove-request-probe / offline"]')!.click());
    expect(container.querySelector<HTMLElement>('[data-testid="probes-view"]')?.dataset.removing).toBe(offlineProbe.id);
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="probes-remove-cancel"]')!.click());
    expect(container.querySelector<HTMLElement>('[data-testid="probes-view"]')?.dataset.removing).toBeUndefined();

    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="probes-remove-request-probe / offline"]')!.click());
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="probes-remove-confirm"]')!.click());
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="probes-remove-confirm"]')!.click());
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(fetchMock).toHaveBeenNthCalledWith(2, '/v1/admin/probes/probe%20%2F%20offline', expect.objectContaining({ method: 'DELETE' }));
    resolveDelete(new Response(null, { status: 204 }));
    await settle();
    expect(container.querySelector<HTMLElement>('[data-testid="probes-view"]')?.dataset).toMatchObject({ count: '0', busy: 'false', failed: 'false' });
  });

  it.each([401, 409])('maps Probe removal status %d without losing the inventory', async (status) => {
    const unauthorized = vi.fn<() => void>();
    vi.stubGlobal('fetch', vi.fn<typeof fetch>()
      .mockResolvedValueOnce(json({ data: [offlineProbe] }))
      .mockResolvedValueOnce(json({}, status)));
    await act(async () => root.render(<ProbesView onUnauthorized={unauthorized} />));
    await settle();
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="probes-remove-request-probe / offline"]')!.click());
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="probes-remove-confirm"]')!.click());
    await settle();
    expect(unauthorized).toHaveBeenCalledTimes(status === 401 ? 1 : 0);
    expect(container.querySelector<HTMLElement>('[data-testid="probes-view"]')?.dataset).toMatchObject({ count: '1', failed: String(status !== 401), busy: 'false' });
  });

  it('loads, edits, creates, deletes, and refreshes observation policies', async () => {
    let policies = [policy];
    const fetchMock = vi.fn<typeof fetch>((input, init) => {
      const path = String(input);
      if (path === '/v1/admin/catalog') return Promise.resolve(json(catalog));
      if (path === '/v1/admin/catalog-refresh' && init?.method === 'POST') {
        return Promise.resolve(json({ state: 'queued', catalogEmpty: false, active: false, eligibleProbes: 1 }, 202));
      }
      if (path === '/v1/admin/catalog-refresh') {
        return Promise.resolve(json({ state: 'ready', catalogEmpty: false, active: false, eligibleProbes: 1 }));
      }
      if (init?.method === 'PUT') return Promise.resolve(json(policy));
      if (init?.method === 'DELETE') {
        policies = [];
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      if (init?.method === 'POST') return Promise.resolve(json({ id: 'new', revision: 1 }));
      return Promise.resolve(json({ data: policies }));
    });
    vi.stubGlobal('fetch', fetchMock);
    await act(async () => root.render(<ObservationsView onUnauthorized={vi.fn<() => void>()} />));
    await settle();
    expect(container.querySelector<HTMLElement>('[data-testid="observations-view"]')?.dataset.count).toBe('1');

    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="observations-edit-policy / 1"]')!.click());
    expect(container.querySelector<HTMLElement>('[data-testid="observations-view"]')?.dataset.editing).toBe('policy / 1');
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="observations-save"]')!.click());
    await settle();
    expect(fetchMock).toHaveBeenCalledWith('/v1/admin/observation-policies/policy%20%2F%201', expect.objectContaining({ method: 'PUT', headers: { 'If-Match': '"2"' } }));

    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="observations-edit-policy / 1"]')!.click());
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="observations-delete-policy / 1"]')!.click());
    await settle();
    expect(fetchMock).toHaveBeenCalledWith('/v1/admin/observation-policies/policy%20%2F%201', expect.objectContaining({ method: 'DELETE' }));

    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="observations-save"]')!.click());
    await settle();
    expect(fetchMock).toHaveBeenCalledWith('/v1/admin/observation-policies', expect.objectContaining({ method: 'POST', headers: { 'If-None-Match': '*' } }));
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="observations-cancel"]')!.click());
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="observations-refresh"]')!.click());
    await settle();
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="observations-catalog-refresh"]')!.click());
    await settle();
    expect(fetchMock).toHaveBeenCalledWith('/v1/admin/catalog-refresh', expect.objectContaining({ method: 'POST' }));
  });

  it.each([401, 500])('maps observation policy failure %d', async (status) => {
    const unauthorized = vi.fn<() => void>();
    vi.stubGlobal('fetch', vi.fn<typeof fetch>((input) => Promise.resolve(
      String(input) === '/v1/admin/catalog' ? json(catalog) : json({}, status),
    )));
    await act(async () => root.render(<ObservationsView onUnauthorized={unauthorized} />));
    await settle();
    expect(unauthorized).toHaveBeenCalledTimes(status === 401 ? 1 : 0);
    expect(container.querySelector<HTMLElement>('[data-testid="observations-view"]')?.dataset.failed).toBe(String(status !== 401));
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="observations-save"]')!.click());
    await settle();
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="observations-delete-policy / 1"]')?.click());
  });

  it('reports a manual catalog refresh failure', async () => {
    const fetchMock = vi.fn<typeof fetch>((input, init) => {
      const path = String(input);
      if (path === '/v1/admin/catalog') return Promise.resolve(json(catalog));
      if (path === '/v1/admin/catalog-refresh' && init?.method === 'POST') return Promise.resolve(json({}, 500));
      if (path === '/v1/admin/catalog-refresh') return Promise.resolve(json({ state: 'ready', catalogEmpty: false, active: false, eligibleProbes: 1 }));
      return Promise.resolve(json({ data: [policy] }));
    });
    vi.stubGlobal('fetch', fetchMock);
    await act(async () => root.render(<ObservationsView onUnauthorized={vi.fn<() => void>()} />));
    await settle();
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="observations-catalog-refresh"]')!.click());
    await settle();
    expect(container.querySelector<HTMLElement>('[data-testid="observations-view"]')?.dataset.failed).toBe('true');
  });

  it('prevents duplicate observation mutations and reports delete failure', async () => {
    let resolveSave!: (response: Response) => void;
    const pendingSave = new Promise<Response>((resolve) => { resolveSave = resolve; });
    let saveStarted = false;
    let deleteFailed = false;
    const fetchMock = vi.fn<typeof fetch>((input, init) => {
      const path = String(input);
      if (path === '/v1/admin/catalog') return Promise.resolve(json(catalog));
      if (path === '/v1/admin/catalog-refresh') return Promise.resolve(json({ state: 'ready', catalogEmpty: false, active: false, eligibleProbes: 1 }));
      if (init?.method === 'POST' && !saveStarted) {
        saveStarted = true;
        return pendingSave;
      }
      if (init?.method === 'DELETE') {
        deleteFailed = true;
        return Promise.resolve(json({}, 500));
      }
      return Promise.resolve(json({ data: [policy] }));
    });
    vi.stubGlobal('fetch', fetchMock);
    await act(async () => root.render(<ObservationsView onUnauthorized={vi.fn<() => void>()} />));
    await settle();
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="observations-save"]')!.click());
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="observations-save"]')!.click());
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="observations-delete-policy / 1"]')!.click());
    expect(saveStarted).toBe(true);
    expect(deleteFailed).toBe(false);
    resolveSave(json({ id: 'new', revision: 1 }));
    await settle();
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="observations-edit-policy / 1"]')!.click());
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="observations-delete-policy / 1"]')!.click());
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="observations-delete-policy / 1"]')!.click());
    await settle();
    expect(container.querySelector<HTMLElement>('[data-testid="observations-view"]')?.dataset.failed).toBe('true');
  });

  it('maps user create, rotate, delete, cancel, and dismiss mutations', async () => {
    const fetchMock = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(json({ data: [user] }))
      .mockResolvedValueOnce(json(issue))
      .mockResolvedValueOnce(json({ data: [user] }))
      .mockResolvedValueOnce(json(issue))
      .mockResolvedValueOnce(json({ data: [user] }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(json({ data: [] }));
    vi.stubGlobal('fetch', fetchMock);
    await act(async () => root.render(<UsersView onUnauthorized={vi.fn<() => void>()} />));
    await settle();

    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="delete-confirm"]')!.click());
    expect(fetchMock).toHaveBeenCalledTimes(1);

    const input = container.querySelector<HTMLInputElement>('[data-testid="display-name"]')!;
    await act(async () => {
      enter(input, 'New User');
    });
    await act(async () => container.querySelector<HTMLFormElement>('[data-testid="create-user"]')!.requestSubmit());
    await settle();
    expect(fetchMock).toHaveBeenNthCalledWith(2, '/v1/admin/users', expect.objectContaining({ method: 'POST', body: JSON.stringify({ displayName: 'New User' }) }));
    expect(container.querySelector<HTMLElement>('[data-testid="users-view"]')?.dataset).toMatchObject({ name: '', pin: '123456', loading: 'false' });

    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="dismiss-issue"]')!.click());
    expect(container.querySelector<HTMLElement>('[data-testid="users-view"]')?.dataset.pin).toBeUndefined();

    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="rotate-user / 1"]')!.click());
    await settle();
    expect(fetchMock).toHaveBeenNthCalledWith(4, '/v1/admin/users/user%20%2F%201/pin', expect.objectContaining({ method: 'POST' }));

    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="delete-request-user / 1"]')!.click());
    expect(container.querySelector<HTMLElement>('[data-testid="users-view"]')?.dataset.deleting).toBe('user / 1');
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="delete-cancel"]')!.click());
    expect(container.querySelector<HTMLElement>('[data-testid="users-view"]')?.dataset.deleting).toBeUndefined();
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="delete-request-user / 1"]')!.click());
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="delete-confirm"]')!.click());
    await settle();
    expect(fetchMock).toHaveBeenNthCalledWith(6, '/v1/admin/users/user%20%2F%201', expect.objectContaining({ method: 'DELETE' }));
    expect(container.querySelector<HTMLElement>('[data-testid="users-view"]')?.dataset.count).toBe('0');
  });

  it.each([
    ['create', '[data-testid="create-user"]'],
    ['rotate', '[data-testid="rotate-user / 1"]'],
    ['delete', '[data-testid="delete-confirm"]'],
  ] as const)('maps a failed user %s mutation without losing the current user', async (operation, selector) => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValueOnce(json({ data: [user] })).mockResolvedValueOnce(json({}, 500));
    vi.stubGlobal('fetch', fetchMock);
    await act(async () => root.render(<UsersView onUnauthorized={vi.fn<() => void>()} />));
    await settle();
    if (operation === 'delete') {
      await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="delete-request-user / 1"]')!.click());
    }
    const element = container.querySelector<HTMLElement>(selector)!;
    await act(async () => {
      if (element instanceof HTMLFormElement) element.requestSubmit();
      else element.click();
    });
    await settle();
    expect(container.querySelector<HTMLElement>('[data-testid="users-view"]')?.dataset).toMatchObject({ count: '1', failure: 'true', loading: 'false' });
  });

  it.each([401, 500])('maps initial user load status %d', async (status) => {
    const unauthorized = vi.fn<() => void>();
    vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockResolvedValue(json({}, status)));
    await act(async () => root.render(<UsersView onUnauthorized={unauthorized} />));
    await settle();
    expect(unauthorized).toHaveBeenCalledTimes(status === 401 ? 1 : 0);
    expect(container.querySelector<HTMLElement>('[data-testid="users-view"]')?.dataset.failure).toBe(String(status !== 401));
  });
});
