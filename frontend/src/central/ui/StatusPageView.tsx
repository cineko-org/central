import { Alert, Button, Divider, Group, Indicator, SimpleGrid, Skeleton, Stack, Text } from '@mantine/core';
import type { AdminReleases, AdminStatus } from '../types';
import { PageHeader } from './PageHeader';

export interface StatusPageViewProps {
  status?: AdminStatus;
  releases?: AdminReleases;
  updatedAt?: Date;
  failed?: boolean;
  onRefresh: () => void;
}

function Health({ healthy, children }: { healthy: boolean; children: string }) {
  return <Group gap="sm" wrap="nowrap"><Indicator color={healthy ? 'green' : 'red'} size={8} processing={healthy} /><Text fw={600}>{children}</Text></Group>;
}

function Metric({ label, value, detail }: { label: string; value: string; detail?: string }) {
  return (
    <Stack gap={8} py="lg">
      <Text size="xs" c="dimmed" tt="uppercase" fw={700} lts="0.08em">{label}</Text>
      <Text fz={24} fw={700}>{value}</Text>
      {detail ? <Text size="sm" c="dimmed">{detail}</Text> : null}
    </Stack>
  );
}

export function StatusPageView({ status, releases, updatedAt, failed, onRefresh }: StatusPageViewProps) {
  if ((!status || !releases) && !failed) return <Stack gap="md"><PageHeader title="운영 상태" /><Skeleton h={48} /><Skeleton h={240} /></Stack>;
  if (!status || !releases) return <Stack gap="xl"><PageHeader title="운영 상태" /><Alert color="red" title="Central 상태를 불러오지 못했습니다"><Button variant="subtle" color="red" p={0} mt="xs" onClick={onRefresh}>다시 시도</Button></Alert></Stack>;
  const ready = Boolean(status?.ready);
  const reconcilerHealthy = Boolean(status?.reconciler?.healthy);
  return (
    <Stack gap={40}>
      <PageHeader
        title="운영 상태"
        description={updatedAt ? `${updatedAt.toLocaleTimeString('ko-KR')} 기준` : '상태를 불러오는 중입니다.'}
        actions={<Button variant="default" onClick={onRefresh}>새로고침</Button>}
      />
      <Stack gap={0}>
        <SimpleGrid cols={{ base: 1, xs: 2, lg: 5 }} spacing={{ base: 0, lg: 32 }}>
          <Metric label="Central" value={ready ? '정상' : '확인 필요'} detail="PostgreSQL 준비 상태" />
          <Metric label="Reconciler" value={reconcilerHealthy ? '정상' : '확인 필요'} detail="할당 조정 루프" />
          <Metric label="Leader" value={status?.reconciler?.leader ? '현재 인스턴스' : '대기'} detail="활성 리더" />
          <Metric label="Oldest due" value={`${status?.reconciler?.oldestDueAgeSeconds ?? 0}초`} detail="가장 오래 지연된 작업" />
          <Metric label="Release generation" value={`#${releases.generation}`} detail="데스크톱 릴리스 세대" />
        </SimpleGrid>
        <Divider />
      </Stack>
      <Stack gap="md">
        <Text component="h2" fz="lg" fw={700}>서비스</Text>
        <Group justify="space-between" py="md"><Text c="dimmed">PostgreSQL</Text><Health healthy={ready}>{ready ? '연결됨' : '연결 실패'}</Health></Group>
        <Divider />
        <Group justify="space-between" py="md"><Text c="dimmed">Reconciler</Text><Health healthy={reconcilerHealthy}>{reconcilerHealthy ? '실행 중' : '확인 필요'}</Health></Group>
        <Divider />
      </Stack>
      {status?.reconciler?.lastError ? <Alert color="red" title="최근 Reconciler 오류">{status.reconciler.lastError}</Alert> : null}
    </Stack>
  );
}
