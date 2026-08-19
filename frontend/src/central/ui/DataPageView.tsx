import { Alert, Button, Divider, Group, ScrollArea, SimpleGrid, Skeleton, Stack, Table, Text } from '@mantine/core';
import type { AdminDataSummary, ObservationIntelligence } from '../types';
import { PageHeader } from './PageHeader';

export interface DataPageViewProps {
  summary?: AdminDataSummary;
  intelligence?: ObservationIntelligence;
  failed: boolean;
  onRefresh: () => void;
}

function Metric({ label, value, detail }: { label: string; value: number; detail: string }) {
  return <Stack gap={8} py="lg"><Text size="xs" c="dimmed" fw={700}>{label}</Text><Text fz={28} fw={700}>{value.toLocaleString('ko-KR')}</Text><Text size="sm" c="dimmed">{detail}</Text></Stack>;
}

function AssignmentRow({ label, value, color }: { label: string; value: number; color?: string }) {
  return <><Group justify="space-between" py="md"><Text c="dimmed">{label}</Text><Text fw={700} c={color}>{value.toLocaleString('ko-KR')}건</Text></Group><Divider /></>;
}

function OpeningPatterns({ intelligence }: { intelligence: ObservationIntelligence }) {
  return (
    <Stack gap="md">
      <Stack gap={4}><Text component="h2" fz="lg" fw={700}>예매가 열리는 시점</Text><Text size="sm" c="dimmed">마지막 미노출 관측과 최초 노출 관측 사이를 기준으로 계산합니다.</Text></Stack>
      <ScrollArea>
        <Table verticalSpacing="md" horizontalSpacing="md" miw={820}>
          <Table.Thead><Table.Tr><Table.Th>영화 · 극장 · 관</Table.Th><Table.Th>표본</Table.Th><Table.Th>주로 열린 시각</Table.Th><Table.Th>상영 전</Table.Th><Table.Th>관측 오차</Table.Th></Table.Tr></Table.Thead>
          <Table.Tbody>
            {intelligence.openingPatterns.map((pattern) => (
              <Table.Tr key={`${pattern.theaterId}-${pattern.auditoriumId}-${pattern.movie}`}>
                <Table.Td><Text fw={600}>{pattern.movie}</Text><Text size="xs" c="dimmed">{pattern.theaterName} · {pattern.auditoriumName} · {pattern.screenTypes.join(' · ') || '일반관'}</Text></Table.Td>
                <Table.Td>{pattern.sampleSize}회</Table.Td>
                <Table.Td>{pattern.typicalOpenTime}</Table.Td>
                <Table.Td>약 {pattern.typicalLeadHours}시간 전</Table.Td>
                <Table.Td>± {Math.ceil(pattern.typicalPrecisionMinutes / 2)}분</Table.Td>
              </Table.Tr>
            ))}
            {intelligence.openingPatterns.length === 0 ? <Table.Tr><Table.Td colSpan={5}><Text c="dimmed" ta="center" py="xl">비교할 수 있는 예매 오픈 관측이 아직 없습니다.</Text></Table.Td></Table.Tr> : null}
          </Table.Tbody>
        </Table>
      </ScrollArea>
    </Stack>
  );
}

function DemandPatterns({ intelligence }: { intelligence: ObservationIntelligence }) {
  return (
    <Stack gap="md">
      <Stack gap={4}><Text component="h2" fz="lg" fw={700}>좌석 가용성 변화</Text><Text size="sm" c="dimmed">홀드와 취소가 섞일 수 있으므로 실제 판매량이 아닌 가용 좌석 감소로 표시합니다.</Text></Stack>
      <ScrollArea>
        <Table verticalSpacing="md" horizontalSpacing="md" miw={900}>
          <Table.Thead><Table.Tr><Table.Th>영화 · 극장 · 관</Table.Th><Table.Th>회차</Table.Th><Table.Th>첫 1시간 감소</Table.Th><Table.Th>절반 이하</Table.Th><Table.Th>가용 좌석 0</Table.Th></Table.Tr></Table.Thead>
          <Table.Tbody>
            {intelligence.demandPatterns.map((pattern) => (
              <Table.Tr key={`${pattern.theaterId}-${pattern.auditoriumId}-${pattern.movie}`}>
                <Table.Td><Text fw={600}>{pattern.movie}</Text><Text size="xs" c="dimmed">{pattern.theaterName} · {pattern.auditoriumName}</Text></Table.Td>
                <Table.Td>{pattern.occurrenceCount}회</Table.Td>
                <Table.Td>{pattern.firstHourSampleSize > 0 ? `${pattern.typicalFirstHourSellThrough}%` : '표본 없음'}</Table.Td>
                <Table.Td>{pattern.halfSoldSampleSize > 0 ? `${pattern.typicalHalfSoldMinutes}분` : '관측 안 됨'}</Table.Td>
                <Table.Td>{pattern.soldOutSampleSize > 0 ? `${pattern.typicalSoldOutMinutes}분` : '관측 안 됨'}</Table.Td>
              </Table.Tr>
            ))}
            {intelligence.demandPatterns.length === 0 ? <Table.Tr><Table.Td colSpan={5}><Text c="dimmed" ta="center" py="xl">좌석 변화 표본이 아직 없습니다.</Text></Table.Td></Table.Tr> : null}
          </Table.Tbody>
        </Table>
      </ScrollArea>
    </Stack>
  );
}

export function DataPageView({ summary, intelligence, failed, onRefresh }: DataPageViewProps) {
  if ((!summary || !intelligence) && !failed) return <Stack gap="md"><PageHeader title="수집 데이터" /><Skeleton h={48} /><Skeleton h={240} /></Stack>;
  if (!summary || !intelligence) return <Stack gap="xl"><PageHeader title="수집 데이터" /><Alert color="red" title="수집 현황을 불러오지 못했습니다"><Button variant="subtle" color="red" p={0} mt="xs" onClick={onRefresh}>다시 시도</Button></Alert></Stack>;
  return (
    <Stack gap={48}>
      <PageHeader title="수집 데이터" description={summary.latestScheduleObservedAt ? `최근 관측 ${new Date(summary.latestScheduleObservedAt).toLocaleString('ko-KR')}` : '아직 저장된 관측이 없습니다.'} actions={<Button variant="default" onClick={onRefresh}>새로고침</Button>} />
      <Stack gap={0}>
        <Text component="h2" fz="lg" fw={700}>카탈로그</Text>
        <SimpleGrid cols={{ base: 2, sm: 3, lg: 6 }} spacing={{ base: 0, lg: 32 }}>
          <Metric label="제공자" value={summary.providers} detail="연결된 영화 서비스" />
          <Metric label="극장" value={summary.theaters} detail="선택 가능한 극장" />
          <Metric label="상영관" value={summary.auditoriums} detail="확인된 관" />
          <Metric label="영화" value={summary.movies} detail="확인된 영화" />
          <Metric label="회차" value={summary.showtimes} detail="현재 조회 가능한 일정" />
          <Metric label="좌석 배치" value={summary.seatMapVersions} detail="변경 이력" />
        </SimpleGrid>
        <Divider />
      </Stack>
      <Stack gap={0}>
        <SimpleGrid cols={{ base: 1, xs: 2, lg: 4 }} spacing={{ base: 0, lg: 32 }}>
          <Metric label="일정 캡처" value={summary.scheduleCaptures} detail="극장별 전체 일정 관측" />
          <Metric label="상영 회차 관측" value={summary.showtimeObservations} detail="분석에 사용한 회차별 상태" />
          <Metric label="관측 정책" value={summary.observationPolicies} detail={`${summary.activeObservationPolicies}개 실행 중`} />
          <Metric label="분석 표본" value={intelligence.openingPatterns.length + intelligence.demandPatterns.length} detail="오픈 및 좌석 변화 패턴" />
        </SimpleGrid>
        <Divider />
      </Stack>
      <OpeningPatterns intelligence={intelligence} />
      <DemandPatterns intelligence={intelligence} />
      <Stack gap={0}>
        <Text component="h2" fz="lg" fw={700} mb="sm">Probe 할당</Text>
        <AssignmentRow label="대기" value={summary.queuedAssignments} />
        <AssignmentRow label="실행 중" value={summary.leasedAssignments} color="blue" />
        <AssignmentRow label="완료" value={summary.completedAssignments} color="green" />
        <AssignmentRow label="실패 및 누락" value={summary.failedAssignments} color={summary.failedAssignments > 0 ? 'red' : undefined} />
      </Stack>
    </Stack>
  );
}
