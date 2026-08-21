import { MantineProvider } from '@mantine/core';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { create } from '@bufbuild/protobuf';
import { ProbeSchema, type Probe } from '@cineko/contracts/gen/ts/cineko/admin/admin_pb';
import { ProbesPageView } from '../src/central/ui/ProbesPageView';

const noOp = () => undefined;
const probes: Probe[] = [
  create(ProbeSchema, { id: 'probe_client_home_seoul_01', kind: { kind: { case: 'client', value: {} } }, ownerUserId: 'user_yongsan', networkId: 'home-seoul', runtime: { componentVersion: '2.0.0', browserRevision: '1228', platform: 'darwin', architecture: 'arm64' }, state: { state: { case: 'online', value: {} } }, availableSlots: 1, maxConcurrency: 1, health: { health: { case: 'healthy', value: {} } } }),
  create(ProbeSchema, { id: 'probe_container_example_02', kind: { kind: { case: 'container', value: {} } }, networkId: 'example-network', runtime: { componentVersion: '1.2.1', browserRevision: '1228', platform: 'linux', architecture: 'amd64' }, state: { state: { case: 'offline', value: {} } }, availableSlots: 0, maxConcurrency: 3, health: { health: { case: 'degraded', value: { reasonCode: 'heartbeat_timeout' } } } }),
];

function render(items?: Probe[], failed?: 'load' | 'remove'): string {
  return renderToStaticMarkup(
    <MantineProvider>
      <ProbesPageView
        probes={items}
        failure={failed}
        busy={false}
        onRefresh={noOp}
        onRemoveRequest={noOp}
        onRemoveCancel={noOp}
        onRemove={noOp}
      />
    </MantineProvider>,
  );
}

describe('Probe management presentation', () => {
  it('uses operator-facing labels and summarizes capacity', () => {
    const markup = render(probes);
    expect(markup).toContain('Probe 관리');
    expect(markup).toContain('사용자 Client');
    expect(markup).toContain('서버 Probe');
    expect(markup).toContain('Heartbeat 기록 없음');
    expect(markup).toContain('가용 슬롯');
    expect(markup).not.toContain('>Container<');
    expect(markup).toContain('Probe, 네트워크 또는 사용자 검색');
  });

  it('keeps loading, empty, and failed states distinct', () => {
    expect(render()).toContain('mantine-Skeleton-root');
    expect(render([])).toContain('등록된 Probe가 없습니다.');
    expect(render(undefined, 'load')).toContain('Probe를 불러오지 못했습니다');
  });

  it('explains removal as an offline-only operation', () => {
    expect(render(probes, 'remove')).toContain('오프라인이고 작업 이력이 없는 Probe만 제거할 수 있습니다.');
  });
});
