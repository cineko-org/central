import { MantineProvider } from '@mantine/core';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { create } from '@bufbuild/protobuf';
import { CatalogRefreshStatusSchema, ObservationPolicyInputSchema, ObservationPolicySchema, type CatalogRefreshStatus, type ObservationPolicy } from '@cineko/contracts/gen/ts/cineko/admin/admin_pb';
import { CgvTheaterIdentitySchema, TheaterIdentitySchema, TheaterSchema } from '@cineko/contracts/gen/ts/cineko/catalog/catalog_pb';
import { ObservationsPageView } from '../src/central/ui/ObservationsPageView';

const noOp = () => undefined;
const theaters = [
  create(TheaterSchema, { id: 'internal-theater-id', providerId: 'cgv', identity: create(TheaterIdentitySchema, { provider: { case: 'cgv', value: create(CgvTheaterIdentitySchema, { siteNo: '0013' }) } }), region: '서울', name: '용산아이파크몰' }),
];
const draft = create(ObservationPolicyInputSchema, { theaterId: '', enabled: true, horizonDays: 14 });

function render(
  editing?: ObservationPolicy,
  catalog = theaters,
  catalogRefresh?: CatalogRefreshStatus,
): string {
  return renderToStaticMarkup(
    <MantineProvider>
      <ObservationsPageView
        policies={editing ? [editing] : []}
        theaters={catalog}
        catalogRefresh={catalogRefresh}
        draft={editing?.input ?? draft}
        editing={editing}
        failed={false}
        saving={false}
        onDraftChange={noOp}
        onSave={noOp}
        onRefresh={noOp}
        onEdit={noOp}
        onCancel={noOp}
        onDelete={noOp}
      />
    </MantineProvider>,
  );
}

describe('Observation policy presentation', () => {
  it('offers the Central catalog by human-readable theater name', () => {
    const markup = render();
    expect(markup).toContain('>극장<');
    expect(markup).toContain('극장을 선택하세요');
    expect(markup).not.toContain('극장 ID');
  });

  it('does not expose the internal theater id and locks theater identity while editing', () => {
    const policy = create(ObservationPolicySchema, { id: 'policy', revision: 1n, theater: theaters[0], input: create(ObservationPolicyInputSchema, { ...draft, theaterId: theaters[0].id, enabled: true }), effectiveMode: { mode: { case: 'baseline', value: {} } }, effectivePriority: 50, effectiveMinSeconds: 900, effectiveMaxSeconds: 1800 });
    const markup = render(policy);
    expect(markup).toContain('서울 · 용산아이파크몰');
    expect(markup).toContain('data-disabled="true"');
    expect(markup).not.toContain('>극장 ID<');
  });

  it('shows only the operator decisions and explains the rolling scan', () => {
    const markup = render();
    expect(markup).toContain('한 번에 확인할 기간');
    expect(markup).toContain('매 조회마다 오늘부터 선택한 기간까지의 예매 일정을 모두 확인합니다.');
    expect(markup).toContain('관측 속도는 Central이 자동으로 조정합니다');
    expect(markup).not.toContain('평상시 최소 간격');
    expect(markup).not.toContain('기본 우선순위');
    expect(markup).not.toContain('좌석 배치');
  });

  it('labels cancellation monitoring separately from baseline collection', () => {
    const policy = create(ObservationPolicySchema, { id: 'policy', revision: 1n, theater: theaters[0], input: create(ObservationPolicyInputSchema, { ...draft, theaterId: theaters[0].id, enabled: true }), effectiveMode: { mode: { case: 'seatAvailability', value: {} } }, effectivePriority: 44, effectiveMinSeconds: 30, effectiveMaxSeconds: 45, demandActive: true });
    expect(render(policy)).toContain('취소표 관측');
  });

  it('offers an initial catalog path instead of an unusable empty policy form', () => {
    const markup = render(undefined, []);
    expect(markup).toContain('극장 데이터가 아직 없습니다');
    expect(markup).toContain('Central이 CGV 영화·극장 목록을 한 번 전체 수집한 뒤 모든 Client에 같은 목록을 제공합니다.');
    expect(markup).toContain('전체 카탈로그 수집 요청');
    expect(markup).not.toContain('새 관측 정책');
  });

  it('explains that an empty Central catalog waits for an eligible Probe', () => {
    const markup = render(undefined, [], create(CatalogRefreshStatusSchema, { state: { case: 'waitingForProbe', value: {} }, catalogEmpty: true }));
    expect(markup).toContain('카탈로그 수집을 위해 Probe를 기다리고 있습니다');
    expect(markup).toContain('사용 가능한 Probe가 연결되면 Central이 CGV 영화·극장 목록을 한 번 전체 수집합니다.');
  });
});
