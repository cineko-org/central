import type { Meta, StoryObj } from '@storybook/react-vite';
import type { FormEventHandler } from 'react';
import { useState } from 'react';
import type { AdminConfiguration, AdminDataSummary, AdminObservationPolicy, AdminProbe, AdminReleases, ClientPINUser, Theater } from '../types';
import { CentralShellView, type CentralPage } from './CentralShellView';
import { DataPageView } from './DataPageView';
import { LoginPageView } from './LoginPageView';
import { ObservationsPageView } from './ObservationsPageView';
import { ProbesPageView } from './ProbesPageView';
import { ReleasesPageView } from './ReleasesPageView';
import { SettingsPageView } from './SettingsPageView';
import { StatusPageView } from './StatusPageView';
import { UsersPageView } from './UsersPageView';

const session = { userId: 'admin', displayName: 'Example Admin', expiresAt: 1_786_000_000 };
const users: ClientPINUser[] = [
  { user: { id: 'usr_01', displayName: 'Yongsan Lab', createdAt: '2026-08-10', updatedAt: '2026-08-10' }, pinActive: true, deviceCount: 2 },
  { user: { id: 'usr_02', displayName: 'Team Client', createdAt: '2026-08-11', updatedAt: '2026-08-11' }, pinActive: false, deviceCount: 0 },
];
const probes: AdminProbe[] = [
  { id: 'probe_client_01', kind: 'client', ownerUserId: 'usr_01', networkId: 'home-seoul', runtimeVersion: 'v1.0.0', browserRevision: '140.0.7339.81', platform: 'darwin', arch: 'arm64', status: 'online', draining: false, availableSlots: 1, maxConcurrency: 1, health: 'healthy', lastHeartbeatAt: '2026-08-12T08:20:00Z', updatedAt: '2026-08-12T08:20:00Z' },
  { id: 'probe_container_02', kind: 'container', networkId: 'example-network', runtimeVersion: 'v1.0.0', browserRevision: '140.0.7339.81', platform: 'linux', arch: 'amd64', status: 'offline', draining: false, availableSlots: 0, maxConcurrency: 3, health: 'degraded', reasonCode: 'heartbeat_timeout', lastHeartbeatAt: '2026-08-12T08:14:00Z', updatedAt: '2026-08-12T08:14:00Z' },
];
const theaters: Theater[] = [
  { id: 'theater_yongsan', providerId: 'cgv', sourceKey: '서울/용산아이파크몰', region: '서울', name: '용산아이파크몰' },
  { id: 'theater_yeongdeungpo', providerId: 'cgv', sourceKey: '서울/영등포', region: '서울', name: '영등포' },
];
const data: AdminDataSummary = { providers: 1, theaters: 198, auditoriums: 1_327, movies: 84, showtimes: 3_621, seatMapVersions: 412, scheduleCaptures: 18_429, showtimeObservations: 243_806, observationPolicies: 12, activeObservationPolicies: 10, queuedAssignments: 8, leasedAssignments: 3, completedAssignments: 18_401, failedAssignments: 17, latestScheduleObservedAt: '2026-08-12T08:20:00Z' };
const intelligence = { snapshotCount: 18_429, showtimeObservations: 243_806, openingPatterns: [{ theaterId: '0013', theaterName: '용산아이파크몰', auditoriumId: 'imax', auditoriumName: 'IMAX관', movie: '예시 영화', screenTypes: ['IMAX'], sampleSize: 17, typicalOpenTime: '14:32', typicalLeadHours: 146, typicalPrecisionMinutes: 12, lastObservedAt: '2026-08-12T08:20:00Z' }], demandPatterns: [{ theaterId: '0013', theaterName: '용산아이파크몰', auditoriumId: 'imax', auditoriumName: 'IMAX관', movie: '예시 영화', occurrenceCount: 9, firstHourSampleSize: 8, typicalFirstHourSellThrough: 72, halfSoldSampleSize: 7, typicalHalfSoldMinutes: 18, soldOutSampleSize: 5, typicalSoldOutMinutes: 43, lastObservedAt: '2026-08-12T08:20:00Z' }] };
const observationPolicy: AdminObservationPolicy = { id: 'policy_0013', revision: 4, theaterId: theaters[0].id, enabled: true, theater: theaters[0], horizonDays: 14, priority: 50, baselineMinSeconds: 300, baselineMaxSeconds: 900, demandMinSeconds: 30, demandMaxSeconds: 45, burstMinSeconds: 15, burstMaxSeconds: 30, burstDurationSeconds: 1800, locale: 'ko-KR', timeZone: 'Asia/Seoul', egressPolicyId: 'scan_default', effectiveMode: 'demand', effectivePriority: 94, effectiveMinSeconds: 2, effectiveMaxSeconds: 5, demandActive: true, nextRunAt: '2026-08-12T08:22:00Z', lastFinishedAt: '2026-08-12T08:20:00Z', lastOutcome: 'completed', createdAt: '2026-08-01T00:00:00Z', updatedAt: '2026-08-12T08:20:00Z' };
const configuration: AdminConfiguration = { listenAddress: ':8080', minimumRuntimeVersion: 'v1.0.0', minimumBrowserRevision: '140.0', clientSessionSeconds: 43_200, clientRefreshSeconds: 2_592_000, adminSessionSeconds: 43_200, reconcileIntervalSeconds: 5, probeHeartbeatTtlSeconds: 90, probeOfflineRetentionDays: 30, assignmentRetryMinSeconds: 1, assignmentRetryMaxSeconds: 5, reconcileBatchSize: 100 };
const artifact = { url: 'https://downloads.example.com/cineko/releases/client/stable/1.0.0/darwin-arm64.zip', size: 84_934_656, sha256: 'a'.repeat(64), executable: 'Cineko Client.app/Contents/MacOS/Cineko Client' };
const publishedAt = '2026-08-12T08:20:00Z';
const releases: AdminReleases = {
  generation: 12,
  components: {
    launcher: [{ channel: 'stable', platform: 'darwin', arch: 'arm64', version: '1.0.0', protocol: 3, launcher: { ...artifact, executable: 'Cineko Launcher.app/Contents/MacOS/Cineko Launcher' }, publishedAt }],
    client: [{ channel: 'stable', platform: 'darwin', arch: 'arm64', version: '1.0.0', minimumLauncherVersion: '1.0.0', minimumBrowserRevision: '140.0', playwrightVersion: '1.61.1', protocol: 3, artifact, probeBootstrapPublicKeys: {}, publishedAt }],
    browser: [{ channel: 'stable', platform: 'linux', arch: 'amd64', revision: '140.0.7339.81', compatiblePlaywrightVersions: ['1.61.1'], artifact: { ...artifact, executable: 'chromium/chrome' }, publishedAt }],
    playwright: [{ channel: 'stable', platform: 'windows', arch: 'amd64', version: '1.61.1', artifact: { ...artifact, executable: 'playwright.exe' }, publishedAt }],
    probe: [{ channel: 'stable', version: '1.0.0', protocol: 3, browserRevision: '140.0.7339.81', image: 'registry.example.com/example/cineko-probe', imageDigest: `sha256:${'b'.repeat(64)}`, publishedAt }],
  },
};
const emptyReleases: AdminReleases = { generation: 0, components: { launcher: [], client: [], browser: [], playwright: [], probe: [] } };
const noOp = () => undefined;
const preventDefault: FormEventHandler<HTMLFormElement> = (event) => event.preventDefault();

function UsersStory({ deleting, issued, items = users, failure = false }: { deleting?: ClientPINUser; issued?: { user: ClientPINUser['user']; pin: string }; items?: ClientPINUser[]; failure?: boolean }) {
  return <UsersPageView users={items} displayName="" issued={issued} deleting={deleting} loading={false} failure={failure} onDisplayNameChange={noOp} onCreate={preventDefault} onRotate={noOp} onDeleteRequest={noOp} onDeleteCancel={noOp} onDelete={noOp} onDismissIssue={noOp} />;
}

function ProbesStory({ items = probes, removing, failed = false }: { items?: AdminProbe[]; removing?: AdminProbe; failed?: boolean }) {
  return <ProbesPageView probes={items} removing={removing} busy={false} failure={failed ? 'remove' : undefined} onRefresh={noOp} onRemoveRequest={noOp} onRemoveCancel={noOp} onRemove={noOp} />;
}

function ShellStory() {
  const [page, setPage] = useState<CentralPage>('overview');
  return (
    <CentralShellView page={page} session={session} navigationOpen={false} onNavigate={setPage} onToggleNavigation={noOp} onLogout={noOp}>
      {page === 'overview' ? <StatusPageView status={{ ready: true, reconciler: { healthy: true, leader: true, oldestDueAgeSeconds: 2 } }} releases={releases} updatedAt={new Date()} onRefresh={noOp} /> : null}
      {page === 'observations' ? <ObservationsPageView policies={[observationPolicy]} theaters={theaters} draft={observationPolicy} failed={false} saving={false} onDraftChange={noOp} onSave={noOp} onRefresh={noOp} onEdit={noOp} onCancel={noOp} onDelete={noOp} /> : null}
      {page === 'probes' ? <ProbesStory /> : null}
      {page === 'data' ? <DataPageView summary={data} intelligence={intelligence} failed={false} onRefresh={noOp} /> : null}
      {page === 'releases' ? <ReleasesPageView releases={releases} failed={false} onRefresh={noOp} /> : null}
      {page === 'users' ? <UsersStory /> : null}
      {page === 'settings' ? <SettingsPageView failed={false} onRefresh={noOp} configuration={configuration} releases={releases} /> : null}
    </CentralShellView>
  );
}

const meta = { title: 'Central/Application', component: ShellStory } satisfies Meta<typeof ShellStory>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Overview: Story = {};
export const Login: Story = { render: () => <LoginPageView userId="" password="" loading={false} failed={false} onUserIdChange={noOp} onPasswordChange={noOp} onSubmit={preventDefault} /> };
export const LoginFailed: Story = { render: () => <LoginPageView userId="admin" password="wrong-password" loading={false} failed onUserIdChange={noOp} onPasswordChange={noOp} onSubmit={preventDefault} /> };
export const StatusHealthy: Story = { render: () => <StatusPageView status={{ ready: true, reconciler: { healthy: true, leader: true, oldestDueAgeSeconds: 2 } }} releases={releases} updatedAt={new Date()} onRefresh={noOp} /> };
export const StatusLoading: Story = { render: () => <StatusPageView onRefresh={noOp} /> };
export const StatusFailed: Story = { render: () => <StatusPageView failed onRefresh={noOp} /> };
export const StatusReconcilerFailure: Story = { render: () => <StatusPageView status={{ ready: true, reconciler: { healthy: false, leader: true, oldestDueAgeSeconds: 84, lastError: 'reconcile assignments: database timeout' } }} releases={releases} updatedAt={new Date()} onRefresh={noOp} /> };
export const Probes: Story = { render: () => <ProbesStory /> };
export const ProbesEmpty: Story = { render: () => <ProbesStory items={[]} /> };
export const ProbeDeleteConfirmation: Story = { render: () => <ProbesStory removing={probes[1]} /> };
export const ProbesLoading: Story = { render: () => <ProbesPageView busy={false} onRefresh={noOp} onRemoveRequest={noOp} onRemoveCancel={noOp} onRemove={noOp} /> };
export const ProbesFailed: Story = { render: () => <ProbesPageView busy={false} failure="load" onRefresh={noOp} onRemoveRequest={noOp} onRemoveCancel={noOp} onRemove={noOp} /> };
export const Data: Story = { render: () => <DataPageView summary={data} intelligence={intelligence} failed={false} onRefresh={noOp} /> };
export const DataEmpty: Story = { render: () => <DataPageView summary={{ providers: 0, theaters: 0, auditoriums: 0, movies: 0, showtimes: 0, seatMapVersions: 0, scheduleCaptures: 0, showtimeObservations: 0, observationPolicies: 0, activeObservationPolicies: 0, queuedAssignments: 0, leasedAssignments: 0, completedAssignments: 0, failedAssignments: 0 }} intelligence={{ snapshotCount: 0, showtimeObservations: 0, openingPatterns: [], demandPatterns: [] }} failed={false} onRefresh={noOp} /> };
export const DataLoading: Story = { render: () => <DataPageView failed={false} onRefresh={noOp} /> };
export const DataFailed: Story = { render: () => <DataPageView failed onRefresh={noOp} /> };
export const ObservationPolicies: Story = { render: () => <ObservationsPageView policies={[observationPolicy]} theaters={theaters} draft={observationPolicy} failed={false} saving={false} onDraftChange={noOp} onSave={noOp} onRefresh={noOp} onEdit={noOp} onCancel={noOp} onDelete={noOp} /> };
export const ObservationPoliciesEmpty: Story = { render: () => <ObservationsPageView policies={[]} theaters={[]} draft={{ ...observationPolicy, theaterId: '' }} failed={false} saving={false} onDraftChange={noOp} onSave={noOp} onRefresh={noOp} onEdit={noOp} onCancel={noOp} onDelete={noOp} /> };
export const Releases: Story = { render: () => <ReleasesPageView releases={releases} failed={false} onRefresh={noOp} /> };
export const ReleasesEmpty: Story = { render: () => <ReleasesPageView releases={emptyReleases} failed={false} onRefresh={noOp} /> };
export const ReleasesLoading: Story = { render: () => <ReleasesPageView failed={false} onRefresh={noOp} /> };
export const ReleasesFailed: Story = { render: () => <ReleasesPageView failed onRefresh={noOp} /> };
export const Users: Story = { render: () => <UsersStory /> };
export const UsersEmpty: Story = { render: () => <UsersStory items={[]} /> };
export const UserPINIssued: Story = { render: () => <UsersStory issued={{ user: users[0].user, pin: '482193' }} /> };
export const UserDeleteConfirmation: Story = { render: () => <UsersStory deleting={users[0]} /> };
export const UsersFailed: Story = { render: () => <UsersStory failure /> };
export const Settings: Story = { render: () => <SettingsPageView configuration={configuration} releases={releases} failed={false} onRefresh={noOp} /> };
export const SettingsLoading: Story = { render: () => <SettingsPageView failed={false} onRefresh={noOp} /> };
export const SettingsFailed: Story = { render: () => <SettingsPageView failed onRefresh={noOp} /> };
