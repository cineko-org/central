import { create } from '@bufbuild/protobuf';
import { Alert, Button, Checkbox, Divider, Group, NumberInput, ScrollArea, Select, Skeleton, Stack, Table, Text } from '@mantine/core';
import { ObservationPolicyInputSchema, type CatalogRefreshStatus, type ObservationPolicy, type ObservationPolicyInput } from '@cineko/contracts/gen/ts/cineko/admin/admin_pb';
import type { Theater } from '@cineko/contracts/gen/ts/cineko/catalog/catalog_pb';
import { PageHeader } from './PageHeader';
import { timestampDate } from './protoPresentation';

interface Props {
  policies?: ObservationPolicy[];
  theaters?: Theater[];
  catalogRefresh?: CatalogRefreshStatus;
  draft: ObservationPolicyInput;
  editing?: ObservationPolicy;
  failed: boolean;
  saving: boolean;
  requestingCatalog?: boolean;
  onDraftChange: (draft: ObservationPolicyInput) => void;
  onSave: () => void;
  onRefresh: () => void;
  onRequestCatalogRefresh?: () => void;
  onEdit: (policy: ObservationPolicy) => void;
  onCancel: () => void;
  onDelete: (policy: ObservationPolicy) => void;
}

function seconds(value: number): string {
  if (value % 3600 === 0) return `${value / 3600}시간`;
  if (value % 60 === 0) return `${value / 60}분`;
  return `${value}초`;
}

function observationMode(policy: ObservationPolicy): string {
  if (!policy.input?.enabled) return '중지됨';
  switch (policy.effectiveMode?.mode.case) {
    case 'demand': return '예매 요청 집중 관측';
    case 'burst': return '새 회차 집중 관측';
    case 'cancellation': return '취소표 관측';
    case 'baseline': return '평상시 관측';
    default: return '상태 확인 중';
  }
}

function PolicyForm(props: Props) {
  const update = <K extends keyof ObservationPolicyInput>(key: K, value: ObservationPolicyInput[K]) => props.onDraftChange(create(ObservationPolicyInputSchema, { ...props.draft, [key]: value }));
  const theaterOptions = props.theaters?.map((theater) => ({
    value: theater.id,
    label: `${theater.region} · ${theater.name}`,
  })) ?? [];
  return (
    <Stack gap="lg">
      <Text component="h2" fz="lg" fw={700}>{props.editing ? '관측 정책 편집' : '새 관측 정책'}</Text>
      <Select label="극장" placeholder="극장을 선택하세요" searchable data={theaterOptions} value={props.draft.theaterId || null} disabled={Boolean(props.editing)} nothingFoundMessage="등록된 극장이 없습니다" onChange={(value) => update('theaterId', value ?? '')} />
      <NumberInput label="한 번에 확인할 기간" description="매 조회마다 오늘부터 선택한 기간까지의 예매 일정을 모두 확인합니다." suffix="일" min={1} max={14} value={props.draft.horizonDays} onChange={(value) => update('horizonDays', Number(value))} />
      <Alert title="관측 속도는 Central이 자동으로 조정합니다" variant="light">
        예매 요청이 있는 극장은 최우선으로 확인하고, 새 회차를 찾으면 즉시 Client에 알립니다.
      </Alert>
      <Checkbox label="이 극장 관측" checked={props.draft.enabled} onChange={(event) => update('enabled', event.currentTarget.checked)} />
      <Group justify="flex-end"><Button variant="subtle" color="gray" onClick={props.onCancel}>초기화</Button><Button loading={props.saving} disabled={!props.draft.theaterId} onClick={props.onSave}>{props.editing ? '변경 저장' : '정책 추가'}</Button></Group>
    </Stack>
  );
}

function EmptyCatalogState(props: Pick<Props, 'catalogRefresh' | 'onRefresh' | 'onRequestCatalogRefresh' | 'requestingCatalog'>) {
  const waiting = props.catalogRefresh?.state.case === 'waitingForProbe';
  const running = props.catalogRefresh?.state.case === 'running';
  return (
    <Alert title={running ? '전체 카탈로그를 수집하고 있습니다' : waiting ? '카탈로그 수집을 위해 Probe를 기다리고 있습니다' : '극장 데이터가 아직 없습니다'} variant="light">
      <Stack gap="md">
        <Text size="sm">
          {waiting
            ? '사용 가능한 Probe가 연결되면 Central이 CGV 영화·극장 목록을 한 번 전체 수집합니다.'
            : running
              ? '수집이 끝나면 극장을 선택해 첫 관측 정책을 만들 수 있습니다.'
              : 'Central이 CGV 영화·극장 목록을 한 번 전체 수집한 뒤 모든 Client에 같은 목록을 제공합니다.'}
        </Text>
        <Group>
          <Button variant="default" onClick={props.onRefresh}>상태 새로고침</Button>
          <Button loading={props.requestingCatalog} disabled={running} onClick={props.onRequestCatalogRefresh}>전체 카탈로그 수집 요청</Button>
        </Group>
      </Stack>
    </Alert>
  );
}

export function ObservationsPageView(props: Props) {
  if ((!props.policies || !props.theaters) && !props.failed) return <Stack gap="md"><PageHeader title="관측 정책" /><Skeleton h={280} /></Stack>;
  return (
    <Stack gap={48}>
      <PageHeader title="예매 오픈 관측" description="극장별 일정을 반복 확인하고, 새 회차가 열리면 대기 중인 Client에 즉시 전달합니다." actions={<Group gap="xs"><Button variant="default" onClick={props.onRefresh}>새로고침</Button>{props.theaters && props.theaters.length > 0 ? <Button variant="default" loading={props.requestingCatalog} disabled={props.catalogRefresh?.state.case === 'running'} onClick={props.onRequestCatalogRefresh}>극장 목록 다시 수집</Button> : null}</Group>} />
      {props.failed ? <Alert color="red" title="관측 정책 요청을 처리하지 못했습니다">입력값과 Central 연결을 확인한 뒤 다시 시도하세요.</Alert> : null}
      {props.theaters?.length === 0 ? <EmptyCatalogState {...props} /> : null}
      {props.theaters && props.theaters.length > 0 ? <>
        {props.policies?.length === 0 ? <Alert color="blue" title="등록된 관측 정책이 없습니다">극장을 선택해 첫 관측 정책을 저장하면 해당 극장의 일정 수집이 시작됩니다.</Alert> : null}
        <PolicyForm {...props} />
        <Divider />
      </> : null}
      <Stack gap="md">
        <Text component="h2" fz="lg" fw={700}>등록된 관측 정책</Text>
        <ScrollArea><Table verticalSpacing="md" horizontalSpacing="md" miw={900}>
          <Table.Thead><Table.Tr><Table.Th>극장</Table.Th><Table.Th>현재 모드</Table.Th><Table.Th>현재 간격</Table.Th><Table.Th>다음 실행</Table.Th><Table.Th /></Table.Tr></Table.Thead>
          <Table.Tbody>
            {props.policies?.map((policy) => <Table.Tr key={policy.id}>
              <Table.Td><Text fw={600}>{policy.theater?.name || '알 수 없는 극장'}</Text><Text size="xs" c="dimmed">{policy.theater?.region || '지역 미상'}</Text></Table.Td>
              <Table.Td>{observationMode(policy)}</Table.Td>
              <Table.Td>{seconds(policy.effectiveMinSeconds)}–{seconds(policy.effectiveMaxSeconds)}</Table.Td>
              <Table.Td>{policy.nextRunAt ? timestampDate(policy.nextRunAt)?.toLocaleString('ko-KR') : '할당 처리 중'}</Table.Td>
              <Table.Td><Group justify="flex-end" gap="xs"><Button variant="subtle" color="gray" onClick={() => props.onEdit(policy)}>편집</Button><Button variant="subtle" color="red" onClick={() => props.onDelete(policy)}>삭제</Button></Group></Table.Td>
            </Table.Tr>)}
            {props.policies?.length === 0 ? <Table.Tr><Table.Td colSpan={5}><Text c="dimmed" ta="center" py="xl">등록된 관측 정책이 없습니다.</Text></Table.Td></Table.Tr> : null}
          </Table.Tbody>
        </Table></ScrollArea>
      </Stack>
    </Stack>
  );
}
