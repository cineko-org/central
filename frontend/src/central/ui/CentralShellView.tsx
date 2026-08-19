import type { ReactNode } from 'react';
import { AppShell, Box, Burger, Button, Divider, Group, NavLink, Stack, Text, Title } from '@mantine/core';
import { IconActivityHeartbeat, IconAdjustments, IconDatabase, IconPackages, IconRadar, IconServer, IconUsers } from '@tabler/icons-react';
import type { AdminSession } from '../types';

export type CentralPage = 'overview' | 'observations' | 'probes' | 'data' | 'releases' | 'users' | 'settings';

export interface CentralShellViewProps {
  page: CentralPage;
  session: AdminSession;
  navigationOpen: boolean;
  children: ReactNode;
  onNavigate: (page: CentralPage) => void;
  onToggleNavigation: () => void;
  onLogout: () => void;
}

const navigation = [
  { page: 'overview', label: '운영 상태', icon: IconActivityHeartbeat },
  { page: 'observations', label: '관측 정책', icon: IconRadar },
  { page: 'probes', label: 'Probe', icon: IconServer },
  { page: 'data', label: '수집 데이터', icon: IconDatabase },
  { page: 'releases', label: '릴리스', icon: IconPackages },
  { page: 'users', label: '사용자', icon: IconUsers },
  { page: 'settings', label: '배포 설정', icon: IconAdjustments },
] as const;

export function CentralShellView(props: CentralShellViewProps) {
  return (
    <AppShell
      header={{ height: 64 }}
      navbar={{ width: 244, breakpoint: 'sm', collapsed: { mobile: !props.navigationOpen } }}
      padding={0}
      bg="dark.9"
    >
      <AppShell.Header bg="dark.9" withBorder>
        <Group h="100%" px={{ base: 'md', sm: 'xl' }} justify="space-between" wrap="nowrap">
          <Group gap="md" wrap="nowrap">
            <Burger opened={props.navigationOpen} onClick={props.onToggleNavigation} hiddenFrom="sm" size="sm" />
            <Group gap="sm" wrap="nowrap">
              <Title order={2} fz="lg">Cineko</Title>
              <Text size="xs" c="dimmed" tt="uppercase" fw={700} lts="0.08em">Central</Text>
            </Group>
          </Group>
          <Group gap="md" wrap="nowrap">
            <Stack gap={0} align="flex-end" visibleFrom="sm">
              <Text size="sm" fw={600}>{props.session.displayName}</Text>
              <Text size="xs" c="dimmed">관리자</Text>
            </Stack>
            <Button variant="subtle" color="gray" size="compact-sm" onClick={props.onLogout}>로그아웃</Button>
          </Group>
        </Group>
      </AppShell.Header>
      <AppShell.Navbar bg="dark.9" withBorder p="md">
        <Stack gap={4} flex={1}>
          {navigation.map(({ page, label, icon: Icon }) => (
            <NavLink
              key={page}
              label={label}
              leftSection={<Icon size={18} stroke={1.7} />}
              active={props.page === page}
              onClick={() => props.onNavigate(page)}
              color="gray"
              variant="filled"
            />
          ))}
        </Stack>
        <Divider my="md" />
        <Box px="sm" pb="xs">
          <Text size="xs" c="dimmed">운영 설정은 배포 환경이 소유합니다.</Text>
        </Box>
      </AppShell.Navbar>
      <AppShell.Main bg="dark.9">
        <Box maw={1320} mx="auto" px={{ base: 'md', sm: 40, xl: 56 }} py={{ base: 28, sm: 48 }}>
          {props.children}
        </Box>
      </AppShell.Main>
    </AppShell>
  );
}
