import { Alert, Anchor, Badge, Button, Divider, Group, SimpleGrid, Skeleton, Stack, Table, Text } from '@mantine/core';
import type { AdminReleases, BrowserRelease, ClientRelease, LauncherRelease, PlaywrightRelease, ProbeRelease, ReleaseArtifact } from '../types';
import { PageHeader } from './PageHeader';

export interface ReleasesPageViewProps {
  releases?: AdminReleases;
  failed: boolean;
  onRefresh: () => void;
}

type ComponentRelease = LauncherRelease | ClientRelease | BrowserRelease | PlaywrightRelease | ProbeRelease;
type ComponentKey = keyof AdminReleases['components'];

const componentLabels: Record<ComponentKey, string> = {
  launcher: 'Launcher',
  client: 'Client',
  browser: 'Chromium',
  playwright: 'Playwright',
  probe: 'Probe',
};

export function releaseVersion(release: ComponentRelease): string {
  return 'revision' in release ? release.revision : release.version;
}

function releaseTarget(release: ComponentRelease): string {
  return 'platform' in release ? `${release.platform}/${release.arch}` : 'multi-arch';
}

function releaseArtifact(release: ComponentRelease): ReleaseArtifact | undefined {
  if ('artifact' in release) return release.artifact;
  if ('launcher' in release) return release.launcher;
  return undefined;
}

function totalRecords(releases: AdminReleases): number {
  return Object.values(releases.components).reduce((total, items) => total + items.length, 0);
}

function publishedAt(value: string): string {
  return new Intl.DateTimeFormat('ko-KR', { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value));
}

function bytes(value: number): string {
  if (value < 1_024) return `${value} B`;
  const units = ['KB', 'MB', 'GB'];
  let size = value / 1_024;
  let unit = units[0];
  for (let index = 1; index < units.length && size >= 1_024; index += 1) {
    size /= 1_024;
    unit = units[index];
  }
  return `${size.toFixed(size >= 10 ? 0 : 1)} ${unit}`;
}

function Artifact({ release }: { release: ComponentRelease }) {
  const artifact = releaseArtifact(release);
  if (!artifact && 'image' in release) {
    return (
      <Stack gap={2}>
        <Text size="sm" ff="monospace">{release.image}</Text>
        <Text size="xs" c="dimmed" ff="monospace">{release.imageDigest.slice(0, 19)}…</Text>
      </Stack>
    );
  }
  if (!artifact) return null;
  return (
    <Stack gap={2}>
      <Anchor href={artifact.url} target="_blank" rel="noreferrer" size="sm">{artifact.executable}</Anchor>
      <Text size="xs" c="dimmed">{bytes(artifact.size)} · {artifact.sha256.slice(0, 12)}…</Text>
    </Stack>
  );
}

function ComponentSection({ name, releases }: { name: string; releases: ComponentRelease[] }) {
  return (
    <Stack gap="sm">
      <Group gap="sm">
        <Text component="h2" fz="lg" fw={700}>{name}</Text>
        <Badge variant="light" color="gray">{releases.length}</Badge>
      </Group>
      {releases.length === 0 ? <Text c="dimmed" size="sm" py="md">등록된 릴리스가 없습니다.</Text> : (
        <Table.ScrollContainer minWidth={760}>
          <Table verticalSpacing="md" horizontalSpacing="md">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>버전</Table.Th>
                <Table.Th>채널</Table.Th>
                <Table.Th>대상</Table.Th>
                <Table.Th>게시 시각</Table.Th>
                <Table.Th>아티팩트</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {releases.map((release) => (
                <Table.Tr key={`${releaseVersion(release)}-${release.channel}-${releaseTarget(release)}`}>
                  <Table.Td><Text fw={650}>{releaseVersion(release)}</Text></Table.Td>
                  <Table.Td><Badge variant="outline" color="gray">{release.channel}</Badge></Table.Td>
                  <Table.Td><Text ff="monospace" size="sm">{releaseTarget(release)}</Text></Table.Td>
                  <Table.Td><Text size="sm" c="dimmed">{publishedAt(release.publishedAt)}</Text></Table.Td>
                  <Table.Td><Artifact release={release} /></Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      )}
      <Divider />
    </Stack>
  );
}

export function ReleasesPageView({ releases, failed, onRefresh }: ReleasesPageViewProps) {
  if (!releases && !failed) return <Stack gap="md"><PageHeader title="릴리스" /><Skeleton h={72} /><Skeleton h={320} /></Stack>;
  if (!releases) return <Stack gap="xl"><PageHeader title="릴리스" /><Alert color="red" title="릴리스 레지스트리를 불러오지 못했습니다"><Button variant="subtle" color="red" p={0} mt="xs" onClick={onRefresh}>다시 시도</Button></Alert></Stack>;
  const total = totalRecords(releases);
  const entries = Object.entries(releases.components) as [ComponentKey, ComponentRelease[]][];
  return (
    <Stack gap={40}>
      <PageHeader
        title="릴리스"
        description="PostgreSQL에 등록된 현재 배포 인벤토리입니다."
        actions={<Button variant="default" onClick={onRefresh}>새로고침</Button>}
      />
      <SimpleGrid cols={{ base: 1, xs: 2 }} spacing={32}>
        <Stack gap={6}><Text size="xs" c="dimmed" tt="uppercase" fw={700}>Desktop generation</Text><Text fz={28} fw={750}>#{releases.generation}</Text></Stack>
        <Stack gap={6}><Text size="xs" c="dimmed" tt="uppercase" fw={700}>Registry records</Text><Text fz={28} fw={750}>{total}개</Text></Stack>
      </SimpleGrid>
      {total === 0 ? <Alert color="gray" title="등록된 릴리스가 없습니다">릴리스 게시가 완료되면 구성 요소별 인벤토리가 여기에 표시됩니다.</Alert> : null}
      <Stack gap={32}>
        {entries.map(([key, items]) => <ComponentSection key={key} name={componentLabels[key]} releases={items} />)}
      </Stack>
    </Stack>
  );
}
