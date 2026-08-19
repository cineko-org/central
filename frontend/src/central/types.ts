export interface AdminSession {
  userId: string;
  displayName: string;
  expiresAt: number;
}

interface ReconcilerStatus {
  healthy: boolean;
  leader: boolean;
  lastSuccessAt?: string;
  lastError?: string;
  oldestDueAgeSeconds?: number;
}

export interface AdminStatus {
  ready: boolean;
  reconciler?: ReconcilerStatus;
}

export interface AdminConfiguration {
  listenAddress: string;
  minimumRuntimeVersion?: string;
  minimumBrowserRevision?: string;
  clientSessionSeconds: number;
  clientRefreshSeconds: number;
  adminSessionSeconds: number;
  reconcileIntervalSeconds: number;
  probeHeartbeatTtlSeconds: number;
  probeOfflineRetentionDays: number;
  assignmentRetryMinSeconds: number;
  assignmentRetryMaxSeconds: number;
  reconcileBatchSize: number;
}

export interface ReleaseArtifact {
  url: string;
  size: number;
  sha256: string;
  executable: string;
}

interface DesktopRelease {
  channel: string;
  platform: string;
  arch: string;
  publishedAt: string;
}

export interface ClientRelease extends DesktopRelease {
  version: string;
  minimumLauncherVersion: string;
  minimumBrowserRevision: string;
  playwrightVersion: string;
  protocol: number;
  artifact: ReleaseArtifact;
  probeBootstrapPublicKeys: Record<string, string>;
}

export interface BrowserRelease extends DesktopRelease {
  revision: string;
  compatiblePlaywrightVersions: string[];
  artifact: ReleaseArtifact;
}

export interface PlaywrightRelease extends DesktopRelease {
  version: string;
  artifact: ReleaseArtifact;
}

export interface LauncherRelease extends DesktopRelease {
  version: string;
  protocol: number;
  launcher: ReleaseArtifact;
}

export interface ProbeRelease {
  channel: string;
  version: string;
  protocol: number;
  browserRevision: string;
  image: string;
  imageDigest: string;
  publishedAt: string;
}

export interface AdminReleases {
  generation: number;
  components: {
    launcher: LauncherRelease[];
    client: ClientRelease[];
    browser: BrowserRelease[];
    playwright: PlaywrightRelease[];
    probe: ProbeRelease[];
  };
}

interface ClientUser {
  id: string;
  displayName: string;
  createdAt: string;
  updatedAt: string;
}

export interface ClientPINUser {
  user: ClientUser;
  pinActive: boolean;
  deviceCount: number;
}

export interface ClientPINIssue {
  user: ClientUser;
  pin: string;
}

export interface AdminProbe {
  id: string;
  kind: 'container' | 'client';
  ownerUserId?: string;
  networkId: string;
  runtimeVersion: string;
  browserRevision: string;
  platform: string;
  arch: string;
  status: 'online' | 'offline';
  draining: boolean;
  availableSlots: number;
  maxConcurrency: number;
  health: 'healthy' | 'degraded';
  reasonCode?: string;
  lastHeartbeatAt?: string;
  updatedAt: string;
}

export interface AdminDataSummary {
  providers: number;
  theaters: number;
  auditoriums: number;
  movies: number;
  showtimes: number;
  seatMapVersions: number;
  scheduleCaptures: number;
  showtimeObservations: number;
  observationPolicies: number;
  activeObservationPolicies: number;
  queuedAssignments: number;
  leasedAssignments: number;
  completedAssignments: number;
  failedAssignments: number;
  latestScheduleObservedAt?: string;
}

export interface Provider {
  id: string;
  name: string;
}

export interface Theater {
  id: string;
  providerId: string;
  sourceKey: string;
  region: string;
  name: string;
}

export interface Movie {
  id: string;
  providerId: string;
  sourceKey: string;
  title: string;
  posterUrl?: string;
}

export interface Auditorium {
  id: string;
  theaterId: string;
  sourceKey: string;
  name: string;
  screenTypes: string[];
  capacity: number;
	seatMapVersion?: string;
}

export interface CatalogIndex {
  generation: number;
  providers: Provider[];
  theaters: Theater[];
  movies: Movie[];
  auditoriums: Auditorium[];
  showtimes: unknown[];
}

export interface CatalogRefreshStatus {
  state: 'ready' | 'queued' | 'running' | 'waiting_for_probe';
  catalogEmpty: boolean;
  requestedAt?: string;
  active: boolean;
  eligibleProbes: number;
  lastStatus?: string;
  lastAttemptedAt?: string;
}

export interface ObservationPolicyInput {
  theaterId: string;
  enabled: boolean;
  horizonDays: number;
  priority: number;
  baselineMinSeconds: number;
  baselineMaxSeconds: number;
  demandMinSeconds: number;
  demandMaxSeconds: number;
  burstMinSeconds: number;
  burstMaxSeconds: number;
  burstDurationSeconds: number;
  locale: string;
  timeZone: string;
  egressPolicyId: string;
}

export interface AdminObservationPolicy extends ObservationPolicyInput {
  id: string;
  revision: number;
  theater: Theater;
  effectiveMode: 'baseline' | 'demand' | 'burst';
  effectivePriority: number;
  effectiveMinSeconds: number;
  effectiveMaxSeconds: number;
  demandActive: boolean;
  burstUntil?: string;
  nextRunAt?: string;
  lastFinishedAt?: string;
  lastOutcome?: 'completed' | 'partial' | 'failed' | 'missed';
  lastErrorCode?: string;
  createdAt: string;
  updatedAt: string;
}

export interface OpeningPattern {
  theaterId: string;
  theaterName: string;
  auditoriumId: string;
  auditoriumName: string;
  movie: string;
  screenTypes: string[];
  sampleSize: number;
  typicalOpenTime: string;
  typicalLeadHours: number;
  typicalPrecisionMinutes: number;
  lastObservedAt: string;
}

export interface DemandPattern {
  theaterId: string;
  theaterName: string;
  auditoriumId: string;
  auditoriumName: string;
  movie: string;
  occurrenceCount: number;
  firstHourSampleSize: number;
  typicalFirstHourSellThrough: number;
  halfSoldSampleSize: number;
  typicalHalfSoldMinutes: number;
  soldOutSampleSize: number;
  typicalSoldOutMinutes: number;
  lastObservedAt: string;
}

export interface ObservationIntelligence {
  snapshotCount: number;
  showtimeObservations: number;
  lastObservedAt?: string;
  openingPatterns: OpeningPattern[];
  demandPatterns: DemandPattern[];
}
