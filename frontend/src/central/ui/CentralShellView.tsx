import { useState, type ReactNode } from 'react';
import { ActionIcon, AppShell, Box, Burger, Divider, Group, NavLink, Stack, Text, Title, Tooltip } from '@mantine/core';
import {
  IconActivityHeartbeat,
  IconAdjustments,
  IconDatabase,
  IconLayoutSidebarLeftCollapse,
  IconLayoutSidebarRightCollapse,
  IconLogout,
  IconPackages,
  IconRadar,
  IconServer,
  IconUsers,
} from '@tabler/icons-react';
import type { Principal } from '@cineko/contracts/gen/ts/cineko/admin/admin_pb';

export type CentralPage = 'overview' | 'observations' | 'probes' | 'data' | 'releases' | 'users' | 'settings';

export interface CentralShellViewProps {
  page: CentralPage;
  session: Principal;
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
  const [navigationCollapsed, setNavigationCollapsed] = useState(false);
  return (
    <AppShell
      header={{ height: { base: 56, sm: 64 } }}
      navbar={{ width: navigationCollapsed ? 76 : 244, breakpoint: 'sm', collapsed: { mobile: !props.navigationOpen } }}
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
            <Tooltip label="로그아웃" events={{ hover: true, focus: true, touch: false }}>
              <ActionIcon aria-label="로그아웃" variant="subtle" color="gray" size={44} onClick={props.onLogout}>
                <IconLogout size={20} />
              </ActionIcon>
            </Tooltip>
          </Group>
        </Group>
      </AppShell.Header>
      <AppShell.Navbar bg="dark.9" withBorder p={navigationCollapsed ? 12 : 'md'}>
        <Stack gap={4} flex={1} align={navigationCollapsed ? 'center' : 'stretch'}>
          {navigation.map(({ page, label, icon: Icon }) => {
            const active = props.page === page;
            if (navigationCollapsed) {
              return (
                <Tooltip
                  key={page}
                  label={label}
                  position="right"
                  events={{ hover: true, focus: true, touch: false }}
                >
                  <ActionIcon
                    aria-label={label}
                    aria-current={active ? 'page' : undefined}
                    color="gray"
                    radius={0}
                    size={48}
                    variant={active ? 'filled' : 'subtle'}
                    onClick={() => props.onNavigate(page)}
                  >
                    <Icon size={20} stroke={1.8} />
                  </ActionIcon>
                </Tooltip>
              );
            }
            return (
              <NavLink
                key={page}
                component="button"
                type="button"
                aria-label={label}
                label={label}
                leftSection={<Icon size={20} stroke={1.8} />}
                active={active}
                onClick={() => props.onNavigate(page)}
                color="gray"
                variant="filled"
                mih={48}
              />
            );
          })}
        </Stack>
        <Divider my="sm" />
        <Group justify={navigationCollapsed ? 'center' : 'space-between'} wrap="nowrap">
          {navigationCollapsed ? null : <Text size="xs" c="dimmed">운영 설정은 배포 환경이 소유합니다.</Text>}
          <Tooltip
            label={navigationCollapsed ? '사이드바 펼치기' : '사이드바 접기'}
            position="right"
            events={{ hover: true, focus: true, touch: false }}
          >
            <ActionIcon
              aria-label={navigationCollapsed ? '사이드바 펼치기' : '사이드바 접기'}
              variant="subtle"
              color="gray"
              size={44}
              visibleFrom="sm"
              onClick={() => setNavigationCollapsed((value) => !value)}
            >
              {navigationCollapsed ? <IconLayoutSidebarRightCollapse size={20} /> : <IconLayoutSidebarLeftCollapse size={20} />}
            </ActionIcon>
          </Tooltip>
        </Group>
      </AppShell.Navbar>
      <AppShell.Main bg="dark.9">
        <Box maw={1680} mx="auto" px={{ base: 'md', sm: 40, xl: 56 }} py={{ base: 24, sm: 48 }}>
          {props.children}
        </Box>
      </AppShell.Main>
    </AppShell>
  );
}
