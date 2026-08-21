import type { ReactNode } from 'react';
import { Alert, Button, Divider, Group, Skeleton, Stack, Text } from '@mantine/core';
import type { Configuration } from '@cineko/contracts/gen/ts/cineko/admin/admin_pb';
import type { Registry } from '@cineko/contracts/gen/ts/cineko/release/release_pb';
import { PageHeader } from './PageHeader';

export interface SettingsPageViewProps {
  configuration?: Configuration;
  releases?: Registry;
  failed: boolean;
  onRefresh: () => void;
}

function duration(seconds: bigint): string {
  if (seconds % 86_400n === 0n) return `${seconds / 86_400n}일`;
  if (seconds % 3_600n === 0n) return `${seconds / 3_600n}시간`;
  if (seconds % 60n === 0n) return `${seconds / 60n}분`;
  return `${seconds}초`;
}

function SettingRow({ label, value }: { label: string; value: string }) {
  return <><Group justify="space-between" gap="xl" py="md" wrap="nowrap"><Text c="dimmed" size="sm">{label}</Text><Text size="sm" fw={600} ta="right">{value}</Text></Group><Divider /></>;
}

function SettingSection({ title, children }: { title: string; children: ReactNode }) {
  return <Stack gap={0}><Text component="h2" fz="lg" fw={700} mb="sm">{title}</Text>{children}</Stack>;
}

export function SettingsPageView({ configuration, releases, failed, onRefresh }: SettingsPageViewProps) {
  if ((!configuration || !releases) && !failed) return <Stack gap="md"><PageHeader title="배포 설정" /><Skeleton h={48} /><Skeleton h={240} /></Stack>;
  if (!configuration || !releases) return <Stack gap="xl"><PageHeader title="배포 설정" /><Alert color="red" title="설정을 불러오지 못했습니다"><Button variant="subtle" color="red" p={0} mt="xs" onClick={onRefresh}>다시 시도</Button></Alert></Stack>;
  const releaseRecords = (releases.clients?.releases.length ?? 0)
    + (releases.browsers?.releases.length ?? 0)
    + (releases.playwright?.releases.length ?? 0)
    + (releases.launchers?.releases.length ?? 0)
    + (releases.probes?.releases.length ?? 0);
  return (
    <Stack gap={40}>
      <PageHeader title="배포 설정" actions={<Button variant="default" onClick={onRefresh}>새로고침</Button>} />
      <SettingSection title="런타임">
        <SettingRow label="수신 주소" value={configuration.listenAddress} />
        <SettingRow label="최소 Probe 버전" value={configuration.minimumRuntimeVersion || '제한 없음'} />
        <SettingRow label="최소 브라우저 리비전" value={configuration.minimumBrowserRevision || '제한 없음'} />
        <SettingRow label="데스크톱 릴리스 세대" value={`#${releases.generation}`} />
        <SettingRow label="레지스트리 레코드" value={`${releaseRecords}개`} />
      </SettingSection>
      <SettingSection title="세션">
        <SettingRow label="Client 세션" value={duration(configuration.clientSessionSeconds)} />
        <SettingRow label="Client 갱신" value={duration(configuration.clientRefreshSeconds)} />
        <SettingRow label="관리자 세션" value={duration(configuration.adminSessionSeconds)} />
      </SettingSection>
      <SettingSection title="할당 및 Probe">
        <SettingRow label="조정 주기" value={duration(configuration.reconcileIntervalSeconds)} />
        <SettingRow label="Heartbeat 만료" value={duration(configuration.probeHeartbeatTtlSeconds)} />
        <SettingRow label="오프라인 보존" value={`${configuration.probeOfflineRetentionDays}일`} />
        <SettingRow label="할당 재시도" value={`${duration(configuration.assignmentRetryMinSeconds)}–${duration(configuration.assignmentRetryMaxSeconds)}`} />
        <SettingRow label="조정 배치" value={`${configuration.reconcileBatchSize}건`} />
      </SettingSection>
    </Stack>
  );
}
