import { useMemo, useState } from 'react';
import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  CopyButton,
  Divider,
  Drawer,
  Group,
  Indicator,
  Modal,
  ScrollArea,
  SegmentedControl,
  Select,
  SimpleGrid,
  Skeleton,
  Stack,
  Table,
  Text,
  TextInput,
  Tooltip,
} from '@mantine/core';
import { IconCheck, IconChevronRight, IconCopy, IconSearch } from '@tabler/icons-react';
import type { AdminProbe } from '../types';
import { PageHeader } from './PageHeader';

export interface ProbesPageViewProps {
  probes?: AdminProbe[];
  removing?: AdminProbe;
  busy: boolean;
  failure?: 'load' | 'remove';
  onRefresh: () => void;
  onRemoveRequest: (probe: AdminProbe) => void;
  onRemoveCancel: () => void;
  onRemove: () => void;
}

type StatusFilter = 'all' | 'online' | 'offline';
type KindFilter = 'all' | AdminProbe['kind'];

function probeLabel(probe: AdminProbe): string {
  return probe.kind === 'client' ? '사용자 Client' : '서버 Probe';
}

function shortID(id: string): string {
  if (id.length <= 18) return id;
  return `${id.slice(0, 9)}…${id.slice(-6)}`;
}

function platformLabel(platform: string, arch: string): string {
  const names: Record<string, string> = { darwin: 'macOS', linux: 'Linux', windows: 'Windows' };
  return `${names[platform] ?? platform} · ${arch}`;
}

function reasonLabel(reasonCode?: string): string | undefined {
  const labels: Record<string, string> = {
    heartbeat_timeout: 'Heartbeat 미수신',
    registration_expired: '등록 만료',
    runtime_error: '런타임 오류',
    unsupported_protocol: '프로토콜 불일치',
  };
  return reasonCode ? labels[reasonCode] ?? reasonCode.replaceAll('_', ' ') : undefined;
}

function heartbeatTime(value?: string): string {
  if (!value) return 'Heartbeat 기록 없음';
  const timestamp = Date.parse(value);
  if (!Number.isFinite(timestamp)) return '시간 정보 없음';
  const elapsed = Math.max(0, Math.round((Date.now() - timestamp) / 1000));
  if (elapsed < 60) return '방금 전';
  if (elapsed < 3_600) return `${Math.floor(elapsed / 60)}분 전`;
  if (elapsed < 86_400) return `${Math.floor(elapsed / 3_600)}시간 전`;
  return `${Math.floor(elapsed / 86_400)}일 전`;
}

function absoluteTime(value?: string): string {
  if (!value) return '기록 없음';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? '시간 정보 없음' : date.toLocaleString('ko-KR');
}

function statusInfo(probe: AdminProbe): { label: string; color: string; detail: string } {
  if (probe.status === 'offline') {
    return { label: '오프라인', color: 'gray', detail: reasonLabel(probe.reasonCode) ?? '연결되지 않음' };
  }
  if (probe.draining) return { label: '정리 중', color: 'orange', detail: '새 작업을 받지 않음' };
  if (probe.health === 'degraded') {
    return { label: '저하됨', color: 'yellow', detail: reasonLabel(probe.reasonCode) ?? '상태 확인 필요' };
  }
  return { label: '온라인', color: 'green', detail: '작업 수신 가능' };
}

function availability(probe: AdminProbe): string {
  if (probe.maxConcurrency <= 0) return '용량 정보 없음';
  return `${Math.round((probe.availableSlots / probe.maxConcurrency) * 100)}% 가용`;
}

function SummaryItem({ label, value, detail, color }: { label: string; value: string | number; detail: string; color: string }) {
  return (
    <Stack gap={4}>
      <Group gap="xs" wrap="nowrap">
        <Indicator color={color} size={8} />
        <Text size="xs" c="dimmed" fw={700}>{label}</Text>
      </Group>
      <Text fz={26} fw={750} lh={1.1}>{value}</Text>
      <Text size="xs" c="dimmed">{detail}</Text>
    </Stack>
  );
}

function DetailRow({ label, value }: { label: string; value: string }) {
  return (
    <Group justify="space-between" align="flex-start" gap="xl" wrap="nowrap">
      <Text size="sm" c="dimmed">{label}</Text>
      <Text size="sm" fw={600} ta="right" style={{ wordBreak: 'break-word' }}>{value}</Text>
    </Group>
  );
}

function ProbeDetails({ probe, onClose }: { probe?: AdminProbe; onClose: () => void }) {
  if (!probe) return null;
  const status = statusInfo(probe);
  return (
    <Drawer opened={Boolean(probe)} onClose={onClose} title="Probe 상세" position="right" size="md">
      <Stack gap="xl">
        <Stack gap={4}>
          <Group justify="space-between" align="flex-start" gap="md">
            <Stack gap={2}>
              <Text fz="lg" fw={700}>{probeLabel(probe)}</Text>
              <Text size="sm" c="dimmed">{probe.networkId || '네트워크 미지정'}</Text>
            </Stack>
            <Badge color={status.color} variant="light">{status.label}</Badge>
          </Group>
          <Text size="sm" c="dimmed">{status.detail}</Text>
        </Stack>
        <Divider />
        <Stack gap="md">
          <Text component="h2" fz="md" fw={700}>연결</Text>
          <DetailRow label="마지막 Heartbeat" value={`${heartbeatTime(probe.lastHeartbeatAt)} · ${absoluteTime(probe.lastHeartbeatAt)}`} />
          <DetailRow label="마지막 변경" value={absoluteTime(probe.updatedAt)} />
          <DetailRow label="네트워크" value={probe.networkId || '미지정'} />
          {probe.ownerUserId ? <DetailRow label="연결 사용자" value={probe.ownerUserId} /> : null}
        </Stack>
        <Divider />
        <Stack gap="md">
          <Text component="h2" fz="md" fw={700}>실행 환경</Text>
          <DetailRow label="런타임" value={probe.runtimeVersion || '미지정'} />
          <DetailRow label="브라우저" value={`Chromium ${probe.browserRevision || '미지정'}`} />
          <DetailRow label="플랫폼" value={platformLabel(probe.platform, probe.arch)} />
          <DetailRow label="동시 작업" value={`${probe.availableSlots}/${probe.maxConcurrency} 슬롯 · ${availability(probe)}`} />
        </Stack>
        <Divider />
        <Stack gap="xs">
          <Group justify="space-between" gap="md">
            <Text size="sm" c="dimmed">내부 식별자</Text>
            <CopyButton value={probe.id} timeout={1_500}>
              {({ copied, copy }) => (
                <Tooltip label={copied ? '복사됨' : '식별자 복사'}>
                  <ActionIcon variant="subtle" color={copied ? 'green' : 'gray'} onClick={copy} aria-label="Probe 식별자 복사">
                    {copied ? <IconCheck size={16} /> : <IconCopy size={16} />}
                  </ActionIcon>
                </Tooltip>
              )}
            </CopyButton>
          </Group>
          <Text size="xs" ff="monospace" c="dimmed" style={{ wordBreak: 'break-all' }}>{probe.id}</Text>
        </Stack>
      </Stack>
    </Drawer>
  );
}

export function ProbesPageView({
  probes, removing, busy, failure, onRefresh, onRemoveRequest, onRemoveCancel, onRemove,
}: ProbesPageViewProps) {
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  const [kindFilter, setKindFilter] = useState<KindFilter>('all');
  const [query, setQuery] = useState('');
  const [selected, setSelected] = useState<AdminProbe>();

  const filtered = useMemo(() => {
    if (!probes) return [];
    const normalizedQuery = query.trim().toLocaleLowerCase();
    return probes.filter((probe) => {
      if (statusFilter !== 'all' && probe.status !== statusFilter) return false;
      if (kindFilter !== 'all' && probe.kind !== kindFilter) return false;
      if (!normalizedQuery) return true;
      return [probe.id, probe.networkId, probe.ownerUserId, probe.runtimeVersion, probe.platform, probe.arch]
        .filter(Boolean)
        .some((value) => value?.toLocaleLowerCase().includes(normalizedQuery));
    });
  }, [kindFilter, probes, query, statusFilter]);

  if (!probes && !failure) return <Stack gap="md"><PageHeader title="Probe 관리" /><Skeleton h={72} /><Skeleton h={48} /><Skeleton h={320} /></Stack>;
  if (!probes) return <Stack gap="xl"><PageHeader title="Probe 관리" /><Alert color="red" title="Probe를 불러오지 못했습니다"><Button variant="subtle" color="red" p={0} mt="xs" onClick={onRefresh}>다시 시도</Button></Alert></Stack>;

  const online = probes.filter((probe) => probe.status === 'online').length;
  const offline = probes.length - online;
  const healthy = probes.filter((probe) => probe.status === 'online' && probe.health === 'healthy' && !probe.draining).length;
  const draining = probes.filter((probe) => probe.draining).length;
  const availableSlots = probes.reduce((total, probe) => total + Math.max(0, probe.availableSlots), 0);
  const totalSlots = probes.reduce((total, probe) => total + Math.max(0, probe.maxConcurrency), 0);

  return (
    <Stack gap={40}>
      <PageHeader title="Probe 관리" description="관측 작업을 실행하는 Probe의 연결 상태와 실행 환경을 관리합니다." actions={<Button variant="default" onClick={onRefresh}>새로고침</Button>} />
      {failure === 'load' ? <Alert color="red" title="최신 상태를 불러오지 못했습니다">현재 표시된 상태는 이전 정보일 수 있습니다.</Alert> : null}
      {failure === 'remove' ? <Alert color="red" title="Probe를 제거하지 못했습니다">오프라인이고 작업 이력이 없는 Probe만 제거할 수 있습니다.</Alert> : null}

      <SimpleGrid cols={{ base: 2, sm: 4 }} spacing={{ base: 24, sm: 32 }}>
        <SummaryItem label="전체" value={probes.length} detail={`${online}개 온라인 · ${offline}개 오프라인`} color="gray" />
        <SummaryItem label="정상" value={healthy} detail={`${draining}개 정리 중`} color="green" />
        <SummaryItem label="가용 슬롯" value={`${availableSlots}/${totalSlots}`} detail="현재 작업을 받을 수 있는 용량" color={availableSlots > 0 ? 'green' : 'yellow'} />
        <SummaryItem label="확인 필요" value={probes.filter((probe) => probe.status === 'online' && probe.health !== 'healthy').length} detail="저하 또는 연결 상태 확인" color="yellow" />
      </SimpleGrid>
      <Divider />

      <Stack gap="sm">
        <Group align="end" justify="space-between" gap="md" wrap="wrap">
          <SegmentedControl
            value={statusFilter}
            onChange={(value) => setStatusFilter(value as StatusFilter)}
            data={[{ label: '전체', value: 'all' }, { label: '온라인', value: 'online' }, { label: '오프라인', value: 'offline' }]}
          />
          <Group align="end" gap="sm" wrap="wrap">
            <TextInput aria-label="Probe 검색" placeholder="Probe, 네트워크 또는 사용자 검색" leftSection={<IconSearch size={16} />} value={query} onChange={(event) => setQuery(event.currentTarget.value)} />
            <Select aria-label="Probe 종류" placeholder="종류" value={kindFilter} onChange={(value) => setKindFilter((value as KindFilter | null) ?? 'all')} data={[{ label: '모든 종류', value: 'all' }, { label: '서버 Probe', value: 'container' }, { label: '사용자 Client', value: 'client' }]} w={150} />
          </Group>
        </Group>
        <Text size="xs" c="dimmed">{filtered.length}개 표시 · 상태는 마지막 Heartbeat 기준입니다.</Text>
      </Stack>

      <ScrollArea>
        <Table verticalSpacing="md" horizontalSpacing="md" highlightOnHover miw={1_040}>
          <Table.Thead><Table.Tr><Table.Th>상태</Table.Th><Table.Th>Probe</Table.Th><Table.Th>네트워크</Table.Th><Table.Th>실행 환경</Table.Th><Table.Th>가용 슬롯</Table.Th><Table.Th>Heartbeat</Table.Th><Table.Th ta="right">관리</Table.Th></Table.Tr></Table.Thead>
          <Table.Tbody>
            {filtered.map((probe) => {
              const status = statusInfo(probe);
              return (
                <Table.Tr key={probe.id}>
                  <Table.Td>
                    <Group gap="sm" wrap="nowrap">
                      <Indicator color={status.color} size={8} processing={status.color === 'green'} />
                      <Stack gap={0}><Text size="sm" fw={600}>{status.label}</Text><Text size="xs" c="dimmed">{status.detail}</Text></Stack>
                    </Group>
                  </Table.Td>
                  <Table.Td>
                    <Stack gap={2}>
                      <Text fw={600}>{probeLabel(probe)}</Text>
                      <Group gap={4} wrap="nowrap">
                        <Text size="xs" c="dimmed" ff="monospace">{shortID(probe.id)}</Text>
                        <CopyButton value={probe.id} timeout={1_500}>
                          {({ copied, copy }) => (
                            <Tooltip label={copied ? '복사됨' : '식별자 복사'}>
                              <ActionIcon variant="subtle" color={copied ? 'green' : 'gray'} size="sm" onClick={copy} aria-label="Probe 식별자 복사">
                                {copied ? <IconCheck size={14} /> : <IconCopy size={14} />}
                              </ActionIcon>
                            </Tooltip>
                          )}
                        </CopyButton>
                      </Group>
                    </Stack>
                  </Table.Td>
                  <Table.Td><Text size="sm">{probe.networkId || '네트워크 미지정'}</Text></Table.Td>
                  <Table.Td><Text size="sm">{probe.runtimeVersion || '버전 미지정'}</Text><Text size="xs" c="dimmed">Chromium {probe.browserRevision || '미지정'} · {platformLabel(probe.platform, probe.arch)}</Text></Table.Td>
                  <Table.Td><Text size="sm" fw={600}>{probe.availableSlots}/{probe.maxConcurrency}</Text><Text size="xs" c="dimmed">{availability(probe)}</Text></Table.Td>
                  <Table.Td><Text size="sm">{heartbeatTime(probe.lastHeartbeatAt)}</Text><Text size="xs" c="dimmed">{absoluteTime(probe.lastHeartbeatAt)}</Text></Table.Td>
                  <Table.Td ta="right">
                    <Group justify="flex-end" gap="xs" wrap="nowrap">
                      <Button variant="subtle" color="gray" size="compact-sm" rightSection={<IconChevronRight size={14} />} onClick={() => setSelected(probe)}>상세</Button>
                      {probe.status === 'offline' ? <Button variant="subtle" color="red" size="compact-sm" disabled={busy} onClick={() => onRemoveRequest(probe)}>제거</Button> : null}
                    </Group>
                  </Table.Td>
                </Table.Tr>
              );
            })}
            {filtered.length === 0 ? <Table.Tr><Table.Td colSpan={7}><Text c="dimmed" ta="center" py={32}>{probes.length === 0 ? '등록된 Probe가 없습니다.' : '조건에 맞는 Probe가 없습니다.'}</Text></Table.Td></Table.Tr> : null}
          </Table.Tbody>
        </Table>
      </ScrollArea>

      <ProbeDetails probe={selected} onClose={() => setSelected(undefined)} />
      <Modal opened={Boolean(removing)} onClose={onRemoveCancel} title="Probe 제거" centered>
        <Stack>
          <Text><Text span fw={700}>{removing ? probeLabel(removing) : 'Probe'}</Text>를 목록에서 제거할까요?</Text>
          <Text size="sm" c="dimmed">오프라인 등록 정보만 제거합니다. 작업 이력이 연결되어 있으면 안전을 위해 제거되지 않습니다.</Text>
          {removing ? <Text size="xs" ff="monospace" c="dimmed">{shortID(removing.id)}</Text> : null}
          <Group justify="flex-end"><Button variant="subtle" color="gray" disabled={busy} onClick={onRemoveCancel}>취소</Button><Button color="red" loading={busy} onClick={onRemove}>Probe 제거</Button></Group>
        </Stack>
      </Modal>
    </Stack>
  );
}
