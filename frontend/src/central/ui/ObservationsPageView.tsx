import { Alert, Button, Checkbox, Divider, Group, NumberInput, ScrollArea, Select, Skeleton, Stack, Table, Text } from '@mantine/core';
import type { AdminObservationPolicy, Auditorium, CatalogRefreshStatus, ObservationPolicyInput, Theater } from '../types';
import { PageHeader } from './PageHeader';

interface Props {
  policies?: AdminObservationPolicy[];
  theaters?: Theater[];
	auditoriums?: Auditorium[];
  catalogRefresh?: CatalogRefreshStatus;
  draft: ObservationPolicyInput;
  editing?: AdminObservationPolicy;
  failed: boolean;
  saving: boolean;
  requestingCatalog?: boolean;
  onDraftChange: (draft: ObservationPolicyInput) => void;
  onSave: () => void;
  onRefresh: () => void;
  onRequestCatalogRefresh?: () => void;
  onEdit: (policy: AdminObservationPolicy) => void;
  onCancel: () => void;
  onDelete: (policy: AdminObservationPolicy) => void;
}

function seconds(value: number): string {
  if (value % 3600 === 0) return `${value / 3600}시간`;
  if (value % 60 === 0) return `${value / 60}분`;
  return `${value}초`;
}

function PolicyForm(props: Props) {
  const update = <K extends keyof ObservationPolicyInput>(key: K, value: ObservationPolicyInput[K]) => props.onDraftChange({ ...props.draft, [key]: value });
  const theaterOptions = props.theaters?.map((theater) => ({
    value: theater.id,
    label: `${theater.region} · ${theater.name}`,
  })) ?? [];
  return (
    <Stack gap="lg">
      <Text component="h2" fz="lg" fw={700}>{props.editing ? '관측 정책 편집' : '새 관측 정책'}</Text>
      <Select label="극장" placeholder="극장을 선택하세요" searchable data={theaterOptions} value={props.draft.theaterId || null} disabled={Boolean(props.editing)} nothingFoundMessage="등록된 극장이 없습니다" onChange={(value) => update('theaterId', value ?? '')} />
      <Group grow align="start"><NumberInput label="관측할 날짜" suffix="일" min={1} max={90} value={props.draft.horizonDays} onChange={(value) => update('horizonDays', Number(value))} /><NumberInput label="기본 우선순위" min={0} max={100} value={props.draft.priority} onChange={(value) => update('priority', Number(value))} /></Group>
      <Group grow align="start"><NumberInput label="평상시 최소 간격" suffix="초" min={30} value={props.draft.baselineMinSeconds} onChange={(value) => update('baselineMinSeconds', Number(value))} /><NumberInput label="평상시 최대 간격" suffix="초" min={31} value={props.draft.baselineMaxSeconds} onChange={(value) => update('baselineMaxSeconds', Number(value))} /></Group>
      <Group grow align="start"><NumberInput label="예매 요청 시 최소 간격" suffix="초" min={30} value={props.draft.demandMinSeconds} onChange={(value) => update('demandMinSeconds', Number(value))} /><NumberInput label="예매 요청 시 최대 간격" suffix="초" min={31} value={props.draft.demandMaxSeconds} onChange={(value) => update('demandMaxSeconds', Number(value))} /></Group>
      <Group grow align="start"><NumberInput label="새 회차 발견 후 최소 간격" suffix="초" min={15} value={props.draft.burstMinSeconds} onChange={(value) => update('burstMinSeconds', Number(value))} /><NumberInput label="새 회차 발견 후 최대 간격" suffix="초" min={16} value={props.draft.burstMaxSeconds} onChange={(value) => update('burstMaxSeconds', Number(value))} /><NumberInput label="집중 관측 시간" suffix="초" min={300} max={21_600} value={props.draft.burstDurationSeconds} onChange={(value) => update('burstDurationSeconds', Number(value))} /></Group>
      <Checkbox label="관측 실행" checked={props.draft.enabled} onChange={(event) => update('enabled', event.currentTarget.checked)} />
      <Group justify="flex-end"><Button variant="subtle" color="gray" onClick={props.onCancel}>초기화</Button><Button loading={props.saving} disabled={!props.draft.theaterId} onClick={props.onSave}>{props.editing ? '변경 저장' : '정책 추가'}</Button></Group>
    </Stack>
  );
}

function EmptyCatalogState(props: Pick<Props, 'catalogRefresh' | 'onRefresh' | 'onRequestCatalogRefresh' | 'requestingCatalog'>) {
  const waiting = props.catalogRefresh?.state === 'waiting_for_probe';
  const running = props.catalogRefresh?.state === 'running';
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
	const seatMapCount = props.auditoriums?.filter((auditorium) => Boolean(auditorium.seatMapVersion)).length ?? 0;
	const missingSeatMaps = (props.auditoriums?.length ?? 0) - seatMapCount;
  if ((!props.policies || !props.theaters) && !props.failed) return <Stack gap="md"><PageHeader title="관측 정책" /><Skeleton h={280} /></Stack>;
  return (
    <Stack gap={48}>
      <PageHeader title="관측 정책" description="극장마다 한 번 조회하고, 예매 요청과 새 회차 발견에 따라 관측 간격만 자동으로 조정합니다." actions={<Group gap="xs"><Button variant="default" onClick={props.onRefresh}>새로고침</Button>{props.theaters && props.theaters.length > 0 ? <Button variant="default" loading={props.requestingCatalog} disabled={props.catalogRefresh?.state === 'running'} onClick={props.onRequestCatalogRefresh}>전체 카탈로그 다시 수집</Button> : null}</Group>} />
      {props.failed ? <Alert color="red" title="관측 정책 요청을 처리하지 못했습니다">입력값과 Central 연결을 확인한 뒤 다시 시도하세요.</Alert> : null}
      {props.theaters?.length === 0 ? <EmptyCatalogState {...props} /> : null}
	  {missingSeatMaps > 0 ? <Alert color="blue" title={`좌석 배치 ${seatMapCount}/${props.auditoriums?.length ?? 0}개 준비됨`}>
		상영 일정에서 발견한 상영관의 좌석 배치는 순차적으로 채웁니다. 로그인된 Client가 없으면 안전하게 대기합니다.
	  </Alert> : null}
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
              <Table.Td><Text fw={600}>{policy.theater.name}</Text><Text size="xs" c="dimmed">{policy.theater.region}</Text></Table.Td>
              <Table.Td>{policy.enabled ? policy.effectiveMode === 'burst' ? '새 회차 집중 관측' : policy.effectiveMode === 'demand' ? '예매 요청 집중 관측' : '평상시 관측' : '중지됨'}</Table.Td>
              <Table.Td>{seconds(policy.effectiveMinSeconds)}–{seconds(policy.effectiveMaxSeconds)}</Table.Td>
              <Table.Td>{policy.nextRunAt ? new Date(policy.nextRunAt).toLocaleString('ko-KR') : '할당 처리 중'}</Table.Td>
              <Table.Td><Group justify="flex-end" gap="xs"><Button variant="subtle" color="gray" onClick={() => props.onEdit(policy)}>편집</Button><Button variant="subtle" color="red" onClick={() => props.onDelete(policy)}>삭제</Button></Group></Table.Td>
            </Table.Tr>)}
            {props.policies?.length === 0 ? <Table.Tr><Table.Td colSpan={5}><Text c="dimmed" ta="center" py="xl">등록된 관측 정책이 없습니다.</Text></Table.Td></Table.Tr> : null}
          </Table.Tbody>
        </Table></ScrollArea>
      </Stack>
    </Stack>
  );
}
