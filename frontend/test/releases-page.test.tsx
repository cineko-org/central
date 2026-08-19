import { MantineProvider } from '@mantine/core';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import type { AdminReleases } from '../src/central/types';
import { ReleasesPageView } from '../src/central/ui/ReleasesPageView';
import { SettingsPageView } from '../src/central/ui/SettingsPageView';
import { StatusPageView } from '../src/central/ui/StatusPageView';

const noOp = () => undefined;
const empty: AdminReleases = { generation: 0, components: { launcher: [], client: [], browser: [], playwright: [], probe: [] } };

function render(releases?: AdminReleases, failed = false): string {
  return renderToStaticMarkup(
    <MantineProvider><ReleasesPageView releases={releases} failed={failed} onRefresh={noOp} /></MantineProvider>,
  );
}

describe('Releases page presentation', () => {
  it('shows the database generation and component inventory', () => {
    const releases: AdminReleases = {
      generation: 7,
      components: {
        launcher: [],
        client: [{
          channel: 'stable', platform: 'darwin', arch: 'arm64', version: '1.2.3',
          minimumLauncherVersion: '1.0.0', minimumBrowserRevision: '140.0', playwrightVersion: '1.61.1', protocol: 3,
          artifact: { url: 'https://cdn.example/client.zip', size: 2_048, sha256: 'a'.repeat(64), executable: 'cineko-client' },
          probeBootstrapPublicKeys: {}, publishedAt: '2026-08-12T08:20:00Z',
        }],
        browser: [], playwright: [], probe: [],
      },
    };
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
    const releases = { ...empty, generation: 9 };
    const status = renderToStaticMarkup(
      <MantineProvider><StatusPageView status={{ ready: true }} releases={releases} onRefresh={noOp} /></MantineProvider>,
    );
    const settings = renderToStaticMarkup(
      <MantineProvider>
        <SettingsPageView
          configuration={{ listenAddress: ':8080', clientSessionSeconds: 60, clientRefreshSeconds: 60, adminSessionSeconds: 60, reconcileIntervalSeconds: 5, probeHeartbeatTtlSeconds: 90, probeOfflineRetentionDays: 30, assignmentRetryMinSeconds: 1, assignmentRetryMaxSeconds: 5, reconcileBatchSize: 100 }}
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
