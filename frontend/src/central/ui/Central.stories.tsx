import type { Meta, StoryObj } from '@storybook/react-vite';
import type { FormEventHandler } from 'react';
import { useState } from 'react';
import { create } from '@bufbuild/protobuf';
import { timestampFromDate } from '@bufbuild/protobuf/wkt';
import {
  ClientPinIssueSchema,
  ClientPinUserSchema,
  ConfigurationSchema,
  DataSummarySchema,
  ObservationIntelligenceSchema,
  ObservationPolicyInputSchema,
  ObservationPolicySchema,
  PrincipalSchema,
  ProbeSchema,
  StatusSchema,
  type ClientPinIssue,
  type ClientPinUser,
  type Probe,
} from '@cineko/contracts/gen/ts/cineko/admin/admin_pb';
import { TheaterSchema } from '@cineko/contracts/gen/ts/cineko/catalog/catalog_pb';
import { UserSchema } from '@cineko/contracts/gen/ts/cineko/client/client_pb';
import { RegistrySchema } from '@cineko/contracts/gen/ts/cineko/release/release_pb';
import { CentralShellView, type CentralPage } from './CentralShellView';
import { DataPageView } from './DataPageView';
import { LoginPageView } from './LoginPageView';
import { ObservationsPageView } from './ObservationsPageView';
import { ProbesPageView } from './ProbesPageView';
import { ReleasesPageView } from './ReleasesPageView';
import { SettingsPageView } from './SettingsPageView';
import { StatusPageView } from './StatusPageView';
import { UsersPageView } from './UsersPageView';

const observedAt = timestampFromDate(new Date('2026-08-12T08:20:00Z'));
const session = create(PrincipalSchema, { userId: 'admin', displayName: 'Example Admin', expiresAt: timestampFromDate(new Date('2026-09-01T00:00:00Z')) });
const users: ClientPinUser[] = [
  create(ClientPinUserSchema, { user: create(UserSchema, { id: 'usr_01', displayName: 'Yongsan Lab' }), pinActive: true, deviceCount: 2 }),
  create(ClientPinUserSchema, { user: create(UserSchema, { id: 'usr_02', displayName: 'Team Client' }), pinActive: false, deviceCount: 0 }),
];
const probes: Probe[] = [
  create(ProbeSchema, { id: 'probe_client_01', kind: { kind: { case: 'client', value: {} } }, ownerUserId: 'usr_01', networkId: 'home-seoul', runtime: { componentVersion: 'v1.0.0', browserRevision: '140.0.7339.81', platform: 'darwin', architecture: 'arm64' }, state: { state: { case: 'online', value: {} } }, availableSlots: 1, maxConcurrency: 1, health: { health: { case: 'healthy', value: {} } }, lastHeartbeatAt: observedAt, updatedAt: observedAt }),
  create(ProbeSchema, { id: 'probe_container_02', kind: { kind: { case: 'container', value: {} } }, networkId: 'example-network', runtime: { componentVersion: 'v1.0.0', browserRevision: '140.0.7339.81', platform: 'linux', architecture: 'amd64' }, state: { state: { case: 'offline', value: {} } }, availableSlots: 0, maxConcurrency: 3, health: { health: { case: 'degraded', value: { reasonCode: 'heartbeat_timeout' } } }, lastHeartbeatAt: observedAt, updatedAt: observedAt }),
];
const theaters = [
  create(TheaterSchema, { id: 'theater_yongsan', providerId: 'cgv', sourceKey: '서울/용산아이파크몰', region: '서울', name: '용산아이파크몰' }),
  create(TheaterSchema, { id: 'theater_yeongdeungpo', providerId: 'cgv', sourceKey: '서울/영등포', region: '서울', name: '영등포' }),
];
const data = create(DataSummarySchema, { providers: 1n, theaters: 198n, auditoriums: 1_327n, movies: 84n, showtimes: 3_621n, seatMapVersions: 412n, scheduleCaptures: 18_429n, showtimeObservations: 243_806n, observationPolicies: 12n, activeObservationPolicies: 10n, queuedAssignments: 8n, leasedAssignments: 3n, completedAssignments: 18_401n, failedAssignments: 17n, latestScheduleObservedAt: observedAt });
const intelligence = create(ObservationIntelligenceSchema, { snapshotCount: 18_429, showtimeObservations: 243_806, openingPatterns: [{ theaterId: '0013', theaterName: '용산아이파크몰', auditoriumId: 'imax', auditoriumName: 'IMAX관', movie: '예시 영화', screenTypes: ['IMAX'], sampleSize: 17, typicalOpenTime: '14:32', typicalLeadHours: 146, typicalPrecisionMinutes: 12 }], demandPatterns: [{ theaterId: '0013', theaterName: '용산아이파크몰', auditoriumId: 'imax', auditoriumName: 'IMAX관', movie: '예시 영화', occurrenceCount: 9, firstHourSampleSize: 8, typicalFirstHourSellThrough: 72, halfSoldSampleSize: 7, typicalHalfSoldMinutes: 18, soldOutSampleSize: 5, typicalSoldOutMinutes: 43 }] });
const policyInput = create(ObservationPolicyInputSchema, { theaterId: theaters[0].id, enabled: true, horizonDays: 14, priority: 50, baselineMinSeconds: 300, baselineMaxSeconds: 900, demandMinSeconds: 30, demandMaxSeconds: 45, burstMinSeconds: 15, burstMaxSeconds: 30, burstDurationSeconds: 1_800, locale: 'ko-KR', timeZone: 'Asia/Seoul', egressPolicyId: 'scan_default' });
const observationPolicy = create(ObservationPolicySchema, { id: 'policy_0013', revision: 4n, theater: theaters[0], input: policyInput, effectiveMode: { mode: { case: 'demand', value: {} } }, effectivePriority: 94, effectiveMinSeconds: 2, effectiveMaxSeconds: 5, demandActive: true, nextRunAt: observedAt, lastFinishedAt: observedAt, lastOutcome: { outcome: { case: 'completed', value: {} } }, createdAt: observedAt, updatedAt: observedAt });
const configuration = create(ConfigurationSchema, { listenAddress: ':8080', minimumRuntimeVersion: 'v1.0.0', minimumBrowserRevision: '140.0', clientSessionSeconds: 43_200n, clientRefreshSeconds: 2_592_000n, adminSessionSeconds: 43_200n, reconcileIntervalSeconds: 5n, probeHeartbeatTtlSeconds: 90n, probeOfflineRetentionDays: 30n, assignmentRetryMinSeconds: 1n, assignmentRetryMaxSeconds: 5n, reconcileBatchSize: 100 });
const artifact = { url: 'https://downloads.example.com/cineko/releases/client/stable/1.0.0/darwin-arm64.zip', size: 84_934_656n, sha256: 'a'.repeat(64), executable: 'Cineko Client.app/Contents/MacOS/Cineko Client' };
const releases = create(RegistrySchema, { generation: 12n, launchers: { releases: [{ channel: 'stable', platform: 'darwin', architecture: 'arm64', version: '1.0.0', launcher: { ...artifact, executable: 'Cineko Launcher.app/Contents/MacOS/Cineko Launcher' }, publishedAt: observedAt }] }, clients: { releases: [{ channel: 'stable', platform: 'darwin', architecture: 'arm64', version: '1.0.0', minimumLauncherVersion: '1.0.0', minimumBrowserRevision: '140.0', playwrightVersion: '1.61.1', artifact, publishedAt: observedAt }] }, browsers: { releases: [{ channel: 'stable', platform: 'linux', architecture: 'amd64', revision: '140.0.7339.81', compatiblePlaywrightVersions: ['1.61.1'], artifact: { ...artifact, executable: 'chromium/chrome' }, publishedAt: observedAt }] }, playwright: { releases: [{ channel: 'stable', platform: 'windows', architecture: 'amd64', version: '1.61.1', artifact: { ...artifact, executable: 'playwright.exe' }, publishedAt: observedAt }] }, probes: { releases: [{ channel: 'stable', version: '1.0.0', browserRevision: '140.0.7339.81', image: 'registry.example.com/example/cineko-probe', imageDigest: `sha256:${'b'.repeat(64)}`, publishedAt: observedAt }] } });
const emptyReleases = create(RegistrySchema, { generation: 0n });
const noOp = () => undefined;
const preventDefault: FormEventHandler<HTMLFormElement> = (event) => event.preventDefault();

function UsersStory({ deleting, issued, items = users, failure = false }: { deleting?: ClientPinUser; issued?: ClientPinIssue; items?: ClientPinUser[]; failure?: boolean }) {
  return <UsersPageView users={items} displayName="" issued={issued} deleting={deleting} loading={false} failure={failure} onDisplayNameChange={noOp} onCreate={preventDefault} onRotate={noOp} onDeleteRequest={noOp} onDeleteCancel={noOp} onDelete={noOp} onDismissIssue={noOp} />;
}

function ProbesStory({ items = probes, removing, failed = false }: { items?: Probe[]; removing?: Probe; failed?: boolean }) {
  return <ProbesPageView probes={items} removing={removing} busy={false} failure={failed ? 'remove' : undefined} onRefresh={noOp} onRemoveRequest={noOp} onRemoveCancel={noOp} onRemove={noOp} />;
}

function ShellStory() {
  const [page, setPage] = useState<CentralPage>('overview');
  const status = create(StatusSchema, { ready: true, reconciler: { healthy: true, leader: true, lastReport: { oldestDueAgeSeconds: 2n } } });
  return (
    <CentralShellView page={page} session={session} navigationOpen={false} onNavigate={setPage} onToggleNavigation={noOp} onLogout={noOp}>
      {page === 'overview' ? <StatusPageView status={status} releases={releases} updatedAt={new Date()} onRefresh={noOp} /> : null}
      {page === 'observations' ? <ObservationsPageView policies={[observationPolicy]} theaters={theaters} draft={policyInput} failed={false} saving={false} onDraftChange={noOp} onSave={noOp} onRefresh={noOp} onEdit={noOp} onCancel={noOp} onDelete={noOp} /> : null}
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
export const StatusHealthy: Story = { render: () => <StatusPageView status={create(StatusSchema, { ready: true, reconciler: { healthy: true, leader: true, lastReport: { oldestDueAgeSeconds: 2n } } })} releases={releases} updatedAt={new Date()} onRefresh={noOp} /> };
export const StatusLoading: Story = { render: () => <StatusPageView onRefresh={noOp} /> };
export const StatusFailed: Story = { render: () => <StatusPageView failed onRefresh={noOp} /> };
export const StatusReconcilerFailure: Story = { render: () => <StatusPageView status={create(StatusSchema, { ready: true, reconciler: { healthy: false, leader: true, lastErrorCode: 'database_timeout', lastReport: { oldestDueAgeSeconds: 84n } } })} releases={releases} updatedAt={new Date()} onRefresh={noOp} /> };
export const Probes: Story = { render: () => <ProbesStory /> };
export const ProbesEmpty: Story = { render: () => <ProbesStory items={[]} /> };
export const ProbeDeleteConfirmation: Story = { render: () => <ProbesStory removing={probes[1]} /> };
export const ProbesLoading: Story = { render: () => <ProbesPageView busy={false} onRefresh={noOp} onRemoveRequest={noOp} onRemoveCancel={noOp} onRemove={noOp} /> };
export const ProbesFailed: Story = { render: () => <ProbesPageView busy={false} failure="load" onRefresh={noOp} onRemoveRequest={noOp} onRemoveCancel={noOp} onRemove={noOp} /> };
export const Data: Story = { render: () => <DataPageView summary={data} intelligence={intelligence} failed={false} onRefresh={noOp} /> };
export const DataEmpty: Story = { render: () => <DataPageView summary={create(DataSummarySchema)} intelligence={create(ObservationIntelligenceSchema)} failed={false} onRefresh={noOp} /> };
export const DataLoading: Story = { render: () => <DataPageView failed={false} onRefresh={noOp} /> };
export const DataFailed: Story = { render: () => <DataPageView failed onRefresh={noOp} /> };
export const ObservationPolicies: Story = { render: () => <ObservationsPageView policies={[observationPolicy]} theaters={theaters} draft={policyInput} failed={false} saving={false} onDraftChange={noOp} onSave={noOp} onRefresh={noOp} onEdit={noOp} onCancel={noOp} onDelete={noOp} /> };
export const ObservationPoliciesEmpty: Story = { render: () => <ObservationsPageView policies={[]} theaters={[]} draft={create(ObservationPolicyInputSchema, { enabled: true, horizonDays: 14 })} failed={false} saving={false} onDraftChange={noOp} onSave={noOp} onRefresh={noOp} onEdit={noOp} onCancel={noOp} onDelete={noOp} /> };
export const Releases: Story = { render: () => <ReleasesPageView releases={releases} failed={false} onRefresh={noOp} /> };
export const ReleasesEmpty: Story = { render: () => <ReleasesPageView releases={emptyReleases} failed={false} onRefresh={noOp} /> };
export const ReleasesLoading: Story = { render: () => <ReleasesPageView failed={false} onRefresh={noOp} /> };
export const ReleasesFailed: Story = { render: () => <ReleasesPageView failed onRefresh={noOp} /> };
export const Users: Story = { render: () => <UsersStory /> };
export const UsersEmpty: Story = { render: () => <UsersStory items={[]} /> };
export const UserPINIssued: Story = { render: () => <UsersStory issued={create(ClientPinIssueSchema, { user: users[0].user, pin: '482193' })} /> };
export const UserDeleteConfirmation: Story = { render: () => <UsersStory deleting={users[0]} /> };
export const UsersFailed: Story = { render: () => <UsersStory failure /> };
export const Settings: Story = { render: () => <SettingsPageView configuration={configuration} releases={releases} failed={false} onRefresh={noOp} /> };
export const SettingsLoading: Story = { render: () => <SettingsPageView failed={false} onRefresh={noOp} /> };
export const SettingsFailed: Story = { render: () => <SettingsPageView failed onRefresh={noOp} /> };
