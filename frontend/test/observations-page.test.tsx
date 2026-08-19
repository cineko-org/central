import { MantineProvider } from '@mantine/core';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import type { AdminObservationPolicy, CatalogRefreshStatus, ObservationPolicyInput, Theater } from '../src/central/types';
import { ObservationsPageView } from '../src/central/ui/ObservationsPageView';

const noOp = () => undefined;
const theaters: Theater[] = [
  { id: 'internal-theater-id', providerId: 'cgv', sourceKey: '서울/용산아이파크몰', region: '서울', name: '용산아이파크몰' },
];
const draft: ObservationPolicyInput = {
  theaterId: '', enabled: true,
  horizonDays: 14, priority: 50,
  baselineMinSeconds: 900, baselineMaxSeconds: 1800,
  demandMinSeconds: 120, demandMaxSeconds: 300,
  burstMinSeconds: 30, burstMaxSeconds: 90, burstDurationSeconds: 3600,
  locale: 'ko-KR', timeZone: 'Asia/Seoul', egressPolicyId: 'scan_default',
};

function render(
  editing?: AdminObservationPolicy,
  catalog = theaters,
  catalogRefresh?: CatalogRefreshStatus,
): string {
  return renderToStaticMarkup(
    <MantineProvider>
      <ObservationsPageView
        policies={editing ? [editing] : []}
        theaters={catalog}
        catalogRefresh={catalogRefresh}
        draft={editing ?? draft}
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
    const policy: AdminObservationPolicy = {
      ...draft,
      id: 'policy', revision: 1, theaterId: theaters[0].id, theater: theaters[0],
      effectiveMode: 'baseline', effectivePriority: 50,
      effectiveMinSeconds: 900, effectiveMaxSeconds: 1800, demandActive: false,
      createdAt: '2026-08-14T00:00:00Z', updatedAt: '2026-08-14T00:00:00Z',
    };
    const markup = render(policy);
    expect(markup).toContain('서울 · 용산아이파크몰');
    expect(markup).toContain('data-disabled="true"');
    expect(markup).not.toContain('>극장 ID<');
  });

  it('offers an initial catalog path instead of an unusable empty policy form', () => {
    const markup = render(undefined, []);
    expect(markup).toContain('극장 데이터가 아직 없습니다');
    expect(markup).toContain('Central이 CGV 영화·극장 목록을 한 번 전체 수집한 뒤 모든 Client에 같은 목록을 제공합니다.');
    expect(markup).toContain('전체 카탈로그 수집 요청');
    expect(markup).not.toContain('새 관측 정책');
  });

  it('explains that an empty Central catalog waits for an eligible Probe', () => {
    const markup = render(undefined, [], {
      state: 'waiting_for_probe', catalogEmpty: true, active: false, eligibleProbes: 0,
    });
    expect(markup).toContain('카탈로그 수집을 위해 Probe를 기다리고 있습니다');
    expect(markup).toContain('사용 가능한 Probe가 연결되면 Central이 CGV 영화·극장 목록을 한 번 전체 수집합니다.');
  });
});
