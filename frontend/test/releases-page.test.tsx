import { MantineProvider } from '@mantine/core';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { create } from '@bufbuild/protobuf';
import { ConfigurationSchema, StatusSchema } from '@cineko/contracts/gen/ts/cineko/admin/admin_pb';
import { RegistrySchema, type Registry } from '@cineko/contracts/gen/ts/cineko/release/release_pb';
import { ReleasesPageView } from '../src/central/ui/ReleasesPageView';
import { SettingsPageView } from '../src/central/ui/SettingsPageView';
import { StatusPageView } from '../src/central/ui/StatusPageView';

const noOp = () => undefined;
const empty = create(RegistrySchema);

function render(releases?: Registry, failed = false): string {
  return renderToStaticMarkup(
    <MantineProvider><ReleasesPageView releases={releases} failed={failed} onRefresh={noOp} /></MantineProvider>,
  );
}

describe('Releases page presentation', () => {
  it('shows the database generation and component inventory', () => {
    const releases = create(RegistrySchema, { generation: 7n, clients: { releases: [{ channel: 'stable', platform: 'darwin', architecture: 'arm64', version: '1.2.3', minimumLauncherVersion: '1.0.0', minimumBrowserRevision: '140.0', playwrightVersion: '1.61.1', artifact: { url: 'https://cdn.example/client.zip', size: 2_048n, sha256: 'a'.repeat(64), executable: 'cineko-client' } }] } });
    const markup = render(releases);
    expect(markup).toContain('#7');
    expect(markup).toContain('1.2.3');
    expect(markup).toContain('darwin/arm64');
    expect(markup).toContain('cineko-client');
  });

  it('distinguishes empty, loading, and failed inventory', () => {
    expect(render(empty)).toContain('등록된 릴리스가 없습니다');
    expect(render()).toContain('mantine-Skeleton-root');
    expect(render(undefined, true)).toContain('릴리스 레지스트리를 불러오지 못했습니다');
  });

  it('uses registry generation and records in status and settings', () => {
    const releases = create(RegistrySchema, { generation: 9n });
    const status = renderToStaticMarkup(
      <MantineProvider><StatusPageView status={create(StatusSchema, { ready: true })} releases={releases} onRefresh={noOp} /></MantineProvider>,
    );
    const settings = renderToStaticMarkup(
      <MantineProvider>
        <SettingsPageView
          configuration={create(ConfigurationSchema, { listenAddress: ':8080', clientSessionSeconds: 60n, clientRefreshSeconds: 60n, adminSessionSeconds: 60n, reconcileIntervalSeconds: 5n, probeHeartbeatTtlSeconds: 90n, probeOfflineRetentionDays: 30n, assignmentRetryMinSeconds: 1n, assignmentRetryMaxSeconds: 5n, reconcileBatchSize: 100 })}
          releases={releases}
          failed={false}
          onRefresh={noOp}
        />
      </MantineProvider>,
    );
    expect(status).toContain('Release generation');
    expect(status).toContain('#9');
    expect(settings).toContain('데스크톱 릴리스 세대');
    expect(settings).toContain('레지스트리 레코드');
    expect(settings).not.toContain('릴리스 아티팩트');
  });
});
