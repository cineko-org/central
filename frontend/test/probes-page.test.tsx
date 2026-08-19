import { MantineProvider } from '@mantine/core';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import type { AdminProbe } from '../src/central/types';
import { ProbesPageView } from '../src/central/ui/ProbesPageView';

const noOp = () => undefined;
const probes: AdminProbe[] = [
  {
    id: 'probe_client_home_seoul_01', kind: 'client', ownerUserId: 'user_yongsan', networkId: 'home-seoul',
    runtimeVersion: '2.0.0', browserRevision: '1228', platform: 'darwin', arch: 'arm64', status: 'online',
    draining: false, availableSlots: 1, maxConcurrency: 1, health: 'healthy',
    lastHeartbeatAt: '2026-08-18T08:20:00Z', updatedAt: '2026-08-18T08:20:00Z',
  },
  {
    id: 'probe_container_example_02', kind: 'container', networkId: 'example-network', runtimeVersion: '1.2.1', browserRevision: '1228',
    platform: 'linux', arch: 'amd64', status: 'offline', draining: false, availableSlots: 0, maxConcurrency: 3,
    health: 'degraded', reasonCode: 'heartbeat_timeout', lastHeartbeatAt: '2026-08-18T07:00:00Z', updatedAt: '2026-08-18T07:00:00Z',
  },
];

function render(items?: AdminProbe[], failed?: 'load' | 'remove'): string {
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
    expect(markup).toContain('Heartbeat 미수신');
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
